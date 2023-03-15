package tcp_fixed_head

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/context"
)

func NewTCPFixHeadServer(port int) *TCPFixHeadServer {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &TCPFixHeadServer{}

	server.Port = port
	server.WorkerPool = defaultAntsPool
	server.Handler = defaultHandler
	server.ctx = context.Background()

	return server
}

// 启动服务
func Run(server *TCPFixHeadServer, protoAddr string, opts ...gnet.Option) error {
	ctx := context.Background()

	go func() {
		// 监听系统信号量
		osSignal := make(chan os.Signal, 1)
		signal.Notify(osSignal, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)

		for {
			select {
			case s := <-osSignal:
				commonutil.GetLogger().Infof(ctx, "收到系统信号:%s", s.String())
				switch s {
				case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT,
					syscall.SIGHUP:
					err := gnet.Stop(ctx, protoAddr)
					if err != nil {
						commonutil.GetLogger().Errorf(ctx, "Stop error:%+v", err)
					}

					return
				default:
				}
			}
		}
	}()

	return gnet.Run(server, protoAddr, opts...)
}

type TCPFixHeadServer struct {
	Port       int
	WorkerPool *ants.Pool

	ConnNum int32

	Handler ServerHandler

	ctx context.Context
}

func (tcphs *TCPFixHeadServer) OnBoot(eng gnet.Engine) (action gnet.Action) {
	commonutil.GetLogger().Infof(tcphs.ctx, "server started.....")
	return
}

func (tcphs *TCPFixHeadServer) OnShutdown(eng gnet.Engine) {
	commonutil.GetLogger().Infof(tcphs.ctx, "server shutdown...... ")
}

func (tcphs *TCPFixHeadServer) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	totalNum := atomic.AddInt32(&tcphs.ConnNum, 1)
	commonutil.GetLogger().Infof(tcphs.ctx,
		"total connection:%d, new connection: %s", totalNum, c.RemoteAddr())

	return
}

func (tcphs *TCPFixHeadServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt32(&tcphs.ConnNum, -1)
	commonutil.GetLogger().Debugf(tcphs.ctx, "close connection: %s", c.RemoteAddr())

	return
}

func (tcphs *TCPFixHeadServer) OnTick() (delay time.Duration, action gnet.Action) {
	return
}

// 在 reactor 协程中做解码操作
func (tcphs *TCPFixHeadServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	protocol := NewTCPFixHeadProtocol()

	protocolData, err := protocol.Decode(c)

	if err != nil {
		if err == ErrIncompletePacket {
			return gnet.None
		}
		commonutil.GetLogger().Errorf(tcphs.ctx, "Protocol Decode error :%+v\n", err)

		return gnet.Close // 关闭连接
	}

	if protocolData == nil {
		return gnet.None
	}

	// 具体业务在 worker pool中处理
	tcphs.WorkerPool.Submit(func() {
		handlerData := &HandlerContext{}
		handlerData.ProtocolData = protocolData
		handlerData.Conn = c
		handlerData.server = tcphs

		tcphs.Handler(handlerData)
	})
	return
}
