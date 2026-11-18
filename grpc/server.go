package grpc

import (
	"context"
	stderrors "errors"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/pkg/errors"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// Server 包装 google.golang.org/grpc.Server, 负责监听、生命周期日志和优雅停机.
// 用 NewServer 构造, 字段在 Run 之前可改, 启动后只读.
type Server struct {
	// Addr 是交给 net.Listen 的监听地址, 形如 "127.0.0.1:50051" / ":50051".
	// NewServer 默认填 DefaultAddr, 可用 WithAddr 覆盖或在构造后直接赋值;
	// 显式置空时 Run 返回错误.
	Addr string

	// ConnNum 是当前 gRPC 连接数, 由 stats handler 用 atomic 修改.
	ConnNum int64

	// StatsHandler 是调用方自定义的 stats handler; 本包会在外面加一层连接计数.
	StatsHandler stats.Handler

	registrars  []ServiceRegistrar
	grpcOptions []grpcgo.ServerOption
	ctx         context.Context

	mu         sync.RWMutex
	grpcServer *grpcgo.Server
	listener   net.Listener
	running    bool
	started    bool

	doneCh   chan struct{}
	doneOnce sync.Once
}

// Run 启动服务并阻塞到 Serve 返回. 监听地址和 grpc 选项都在构造期用 option 配好,
// 启动参数属于 server 配置的一部分, 和 StatsHandler 一样.
func (server *Server) Run() error {
	if server == nil || server.doneCh == nil || server.ctx == nil {
		return ErrNotConstructed
	}

	if server.Addr == "" {
		return ErrAddrRequired
	}

	server.mu.Lock()
	if server.started {
		server.mu.Unlock()
		return ErrAlreadyStarted
	}
	server.started = true
	server.mu.Unlock()

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		// 端口被占用之类的启动失败允许修好后重试, 不能被 started 永久挡住.
		server.mu.Lock()
		server.started = false
		server.mu.Unlock()

		return errors.Wrapf(err, "grpc: listen %s", server.Addr)
	}

	grpcOptions := make([]grpcgo.ServerOption, 0, len(server.grpcOptions)+1)
	grpcOptions = append(grpcOptions, server.grpcOptions...)
	// 最后追加, 保证连接计数一定生效; 自定义 handler 通过 WithStatsHandler 传入,
	// 由 connStatsHandler 负责转调.
	grpcOptions = append(grpcOptions, grpcgo.StatsHandler(newConnStatsHandler(server, server.StatsHandler)))

	grpcServer := grpcgo.NewServer(grpcOptions...)
	for _, registrar := range server.registrars {
		registrar(grpcServer)
	}

	server.mu.Lock()
	server.grpcServer = grpcServer
	server.listener = listener
	server.running = true
	server.mu.Unlock()

	go server.handleSignals()

	commonutil.GetLogger().Infof(server.ctx, "[server] grpc server started, listen:%s", listener.Addr())

	serveErr := grpcServer.Serve(listener)

	server.mu.Lock()
	server.running = false
	server.grpcServer = nil
	server.listener = nil
	server.mu.Unlock()
	server.closeDone()

	if serveErr != nil && !stderrors.Is(serveErr, grpcgo.ErrServerStopped) {
		return errors.Wrap(serveErr, "grpc: serve")
	}

	commonutil.GetLogger().Infof(server.ctx, "[server] grpc server shutdown")
	return nil
}

// Stop 立即关闭监听和所有连接, 未完成的 RPC 会被取消.
func (server *Server) Stop() error {
	return server.stop(false)
}

// GracefulStop 停止接受新 RPC, 等已开始的 RPC 完成后再退出.
func (server *Server) GracefulStop() error {
	return server.stop(true)
}

func (server *Server) stop(graceful bool) error {
	server.mu.RLock()
	grpcServer := server.grpcServer
	running := server.running
	server.mu.RUnlock()

	if grpcServer == nil || !running {
		return ErrNotRunning
	}

	// 先关闭 doneCh, 让信号协程从 select 里退出; 否则下面 GracefulStop 成功后
	// 它还会占着 signal.Notify 直到下次信号.
	server.closeDone()

	if graceful {
		grpcServer.GracefulStop()
		return nil
	}

	grpcServer.Stop()
	return nil
}

// LocalAddr 返回实际监听地址; Run 还没开始监听时返回 nil.
// 传 ":0" 时用它拿到系统分配的端口.
func (server *Server) LocalAddr() net.Addr {
	server.mu.RLock()
	defer server.mu.RUnlock()

	if server.listener == nil {
		return nil
	}

	return server.listener.Addr()
}

// GetConnNum 返回当前连接数. ConnNum 由 stats handler 用 atomic 修改,
// 外部读取必须走这个方法.
func (server *Server) GetConnNum() int64 {
	return atomic.LoadInt64(&server.ConnNum)
}

// handleSignals 收到系统信号后优雅停机. 同时监听 doneCh,
// 这样程序内调用 Stop/GracefulStop 时不会留下一个永远等信号的 goroutine.
func (server *Server) handleSignals() {
	osSignal := make(chan os.Signal, 1)
	signal.Notify(osSignal, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(osSignal)

	select {
	case <-server.doneCh:
		return
	case sig := <-osSignal:
		commonutil.GetLogger().Infof(server.ctx, "[server] 收到系统信号:%s", sig.String())

		server.mu.RLock()
		grpcServer := server.grpcServer
		server.mu.RUnlock()
		if grpcServer == nil {
			return
		}

		grpcServer.GracefulStop()
	}
}

func (server *Server) closeDone() {
	server.doneOnce.Do(func() {
		close(server.doneCh)
	})
}
