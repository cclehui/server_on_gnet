package tcp_fixed_head

import (
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/context"
)

// NewTCPFixHeadServer 构造 server. 先应用 opts, 再给没被 option 设置过的字段填默认值:
// option 里给了什么就用什么, 不会先建一个默认值再被覆盖掉(order: opts → defaults).
// 后传的 option 覆盖先传的.
//
// 监听地址默认是 DefaultProtoAddr(tcp://:7001, 所有网卡); 要换地址/端口就传 WithProtoAddr:
//
//	// 默认: 监听所有网卡的 7001
//	tcpServer := NewTCPFixHeadServer()
//	tcpServer.Run()
//
//	// 显式指定, 例如只监听本机回环
//	tcpServer = NewTCPFixHeadServer(
//		WithProtoAddr("tcp://127.0.0.1:7001"),
//	)
//	tcpServer.Run()
//
//	// 端口不是编译期常量时用 net.JoinHostPort 拼, 别手写冒号
//	addr := "tcp://" + net.JoinHostPort(ip, strconv.Itoa(port))
//	tcpServer = NewTCPFixHeadServer(WithProtoAddr(addr))
//	tcpServer.Run()
//
// 也可以构造后直接赋值 server.ProtoAddr(显式置空时 Run 会返回错误).
func NewTCPFixHeadServer(opts ...ServerOption) *TCPFixHeadServer {
	server := &TCPFixHeadServer{
		// buffered: OnBoot 在 reactor 里同步发送, 就算信号协程还没起来也不能阻塞事件循环
		engineCh: make(chan gnet.Engine, 1),
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(server)
	}

	// 下面每个默认值只在对应字段仍为空时生效.
	// 池会真正分配资源, 先建默认值再被覆盖就是纯泄漏(容量 1024*1024).
	if server.ProtoAddr == "" {
		server.ProtoAddr = DefaultProtoAddr
	}

	if server.Handler == nil {
		server.Handler = defaultHandler
	}

	if server.ctx == nil {
		server.ctx = context.Background()
	}

	if server.WorkerPool == nil {
		server.WorkerPool = newDefaultWorkerPool()
	}

	// 协议在构造时绑定: Decode 在 reactor 里跑, 每条 OnTraffic 再 new 一个没有任何意义
	if server.protocol == nil {
		server.protocol = NewTCPFixHeadProtocol()
	}

	return server
}

func newDefaultWorkerPool() *ants.Pool {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	// 构造函数忽略池错误, 参数是静态合法的
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	return defaultAntsPool
}
