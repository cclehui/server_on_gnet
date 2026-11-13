package tcp_fixed_head

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"github.com/pkg/errors"
	"golang.org/x/net/context"

	"github.com/cclehui/server_on_gnet/commonutil"
)

// TCPFixHeadServer 是固定头协议的 gnet EventHandler 实现.
// 用 NewTCPFixHeadServer 构造, 字段在 Run 之前可改, 启动后只读.
type TCPFixHeadServer struct {
	// ProtoAddr 是交给 gnet 的监听地址, 形如 "tcp://:7001" / "tcp://127.0.0.1:7001".
	// NewTCPFixHeadServer 默认填 DefaultProtoAddr, 可用 WithProtoAddr 覆盖或在构造后
	// 直接赋值; 显式置空时 Run 返回错误. Run 直接用它启动引擎.
	ProtoAddr string

	WorkerPool *ants.Pool

	ConnNum int32

	Handler ServerHandler

	// 协议解析器在构造时绑定, OnTraffic 不再每次 new 一个
	protocol *TCPFixHeadProtocol

	// 传给 gnet.Run 的引擎选项, 通过 WithGnetOptions 追加
	gnetOptions []gnet.Option

	// gnet 的 Engine 只在 OnBoot 里拿得到, 信号处理要用它来停服务,
	// 所以用这个 buffered channel 把 Engine 交给信号协程(gnet.Stop 已 deprecated).
	// OnShutdown 时关闭它, 让还没拿到 Engine 的信号协程立刻退出而不是永远阻塞.
	engineCh   chan gnet.Engine
	engineOnce sync.Once

	ctx context.Context
}

// Run 启动服务. 监听地址和 gnet 选项都在构造期用 option 配好,
// 这里不再接地址: 启动参数属于 server 配置的一部分, 和 Handler/Pool 一样.
func (tcphs *TCPFixHeadServer) Run() error {
	if tcphs.Handler == nil || tcphs.WorkerPool == nil || tcphs.protocol == nil {
		// 手写 &TCPFixHeadServer{} 而没走构造函数时, 给出明确错误而不是在第一条消息时 panic
		return errors.New("tcp_fixed_head: server not constructed by NewTCPFixHeadServer")
	}

	if tcphs.ProtoAddr == "" {
		return errors.New("tcp_fixed_head: Run needs a listen address (ProtoAddr / WithProtoAddr)")
	}

	go tcphs.handleSignals()

	return gnet.Run(tcphs, tcphs.ProtoAddr, tcphs.gnetOptions...)
}

// handleSignals 等 OnBoot 交出 Engine 后, 收到系统信号就优雅停机.
// 用 Engine.Stop 而不是 gnet.Stop: 后者在 gnet v2 里已 deprecated, 而且按地址
// 查全局表, 同一地址跑多个 engine 时会停错/泄漏.
func (tcphs *TCPFixHeadServer) handleSignals() {
	osSignal := make(chan os.Signal, 1)
	signal.Notify(osSignal, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(osSignal)

	// 只需要 range 就够: 这里注册的每个信号都要停服务, 没有其他分支要 select
	for s := range osSignal {
		commonutil.GetLogger().Infof(tcphs.ctx, "[server] 收到系统信号:%s", s.String())

		engine, ok := <-tcphs.engineCh
		if !ok {
			// OnBoot 没跑起来(Run 已经退出), 没东西可停
			return
		}

		if err := engine.Stop(tcphs.ctx); err != nil {
			commonutil.GetLogger().Errorf(tcphs.ctx, "[server] Stop error:%+v", err)
		}

		return
	}
}

// 当前连接数。ConnNum 是被 reactor 协程用 atomic 修改的, 外部读取必须走这个
// 方法, 直接读字段会和 atomic.AddInt32 构成 data race
func (tcphs *TCPFixHeadServer) GetConnNum() int32 {
	return atomic.LoadInt32(&tcphs.ConnNum)
}

func (tcphs *TCPFixHeadServer) OnBoot(eng gnet.Engine) (action gnet.Action) {
	// Engine 只能在这里拿到, 交给信号协程去调用 Engine.Stop
	if tcphs.engineCh != nil {
		tcphs.engineCh <- eng
	}

	commonutil.GetLogger().Infof(tcphs.ctx, "[server] server started")
	return
}

func (tcphs *TCPFixHeadServer) OnShutdown(eng gnet.Engine) {
	// 已经在停机的路径上: 关闭 channel 唤醒可能还在等 Engine 的信号协程
	tcphs.closeEngineCh()

	commonutil.GetLogger().Infof(tcphs.ctx, "[server] server shutdown")
}

// closeEngineCh 只关一次: 停机可能由 Engine.Stop 或 gnet.Run 返回两条路径触发.
func (tcphs *TCPFixHeadServer) closeEngineCh() {
	tcphs.engineOnce.Do(func() {
		close(tcphs.engineCh)
	})
}

func (tcphs *TCPFixHeadServer) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	totalNum := atomic.AddInt32(&tcphs.ConnNum, 1)
	commonutil.GetLogger().Infof(tcphs.ctx,
		"[server] 连接数=%d, 新连接: %s", totalNum, c.RemoteAddr())

	return
}

func (tcphs *TCPFixHeadServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt32(&tcphs.ConnNum, -1)
	commonutil.GetLogger().Debugf(tcphs.ctx, "[server] 关闭连接: %s", c.RemoteAddr())

	return
}

func (tcphs *TCPFixHeadServer) OnTick() (delay time.Duration, action gnet.Action) {
	return
}

// 在 reactor 协程中做解码操作
func (tcphs *TCPFixHeadServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	// 一次 OnTraffic 里缓冲区可能存在多个完整包(粘包), 必须循环解包直到数据不足,
	// 否则缓冲区里的旧包要等下一次 OnTraffic 才被处理, 表现为响应"滞后一包"
	for {
		protocolData, err := tcphs.protocol.Decode(c)

		if err != nil {
			if err == ErrIncompletePacket {
				return gnet.None // 半包, 保留上下文等待后续数据
			}
			commonutil.GetLogger().Errorf(tcphs.ctx, "[server] 解包失败, 关闭连接: %v", err)

			return gnet.Close // 协议错误, 关闭连接
		}

		// 具体业务在 worker pool中处理
		tcphs.WorkerPool.Submit(func() {
			handlerData := &HandlerContext{}
			handlerData.ProtocolData = protocolData
			handlerData.Conn = c
			handlerData.server = tcphs

			tcphs.Handler(handlerData)
		})
	}
}
