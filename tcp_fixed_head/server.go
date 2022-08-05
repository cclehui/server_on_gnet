package tcp_fixed_head

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet"
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
	opts = append(opts, gnet.WithCodec(&TCPFixHeadProtocal{}))
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

	return gnet.Serve(server, protoAddr, opts...)
}

type TCPFixHeadServer struct {
	*gnet.EventServer
	Port       int
	WorkerPool *ants.Pool

	ConnNum int

	Handler ServerHandler

	ctx context.Context
}

func (tcpfhs *TCPFixHeadServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	commonutil.GetLogger().Infof(tcpfhs.ctx,
		"server is listening on %s (multi-cores: %t, loops: %d)",
		srv.Addr.String(), srv.Multicore, srv.NumEventLoop)
	return
}

func (tcpfhs *TCPFixHeadServer) OnShutdown(srv gnet.Server) {
	commonutil.GetLogger().Infof(tcpfhs.ctx, "server shutdown on %s", srv.Addr.String())
}

func (tcpfhs *TCPFixHeadServer) OnOpened(c gnet.Conn) (out []byte, action gnet.Action) {
	tcpfhs.ConnNum = tcpfhs.ConnNum + 1
	commonutil.GetLogger().Debugf(tcpfhs.ctx, "new connection: %s", c.RemoteAddr())

	return
}

func (tcpfhs *TCPFixHeadServer) OnClosed(c gnet.Conn, err error) (action gnet.Action) {
	tcpfhs.ConnNum = tcpfhs.ConnNum - 1
	commonutil.GetLogger().Debugf(tcpfhs.ctx, "close connection: %s", c.RemoteAddr())

	return
}

func (tcpfhs *TCPFixHeadServer) PreWrite() {}

func (tcpfhs *TCPFixHeadServer) Tick() (delay time.Duration, action gnet.Action) {
	return
}

// 在 reactor 协程中做解码操作
func (tcpfhs *TCPFixHeadServer) React(frame []byte, c gnet.Conn) (out []byte, action gnet.Action) {
	protocal := NewTCPFixHeadProtocal()

	protocalData, err := protocal.DecodeFrame(frame)

	if err != nil {
		commonutil.GetLogger().Errorf(tcpfhs.ctx, "React WorkerPool Decode error :%v\n", err)

		return nil, gnet.None
	}

	if protocalData == nil {
		return nil, gnet.None
	}

	// 具体业务在 worker pool中处理
	tcpfhs.WorkerPool.Submit(func() {
		handlerData := &HandlerContext{}
		handlerData.ProtocalData = protocalData
		handlerData.Conn = c
		handlerData.server = tcpfhs
		handlerData.frameData = frame

		tcpfhs.Handler(handlerData)
	})
	return
}
