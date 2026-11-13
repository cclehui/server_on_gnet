package tcp_fixed_head

import (
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/context"
)

// ServerOption 在 NewTCPFixHeadServer 构造阶段修改 server 配置.
// 这些字段只在 Run 之前写, 启动后按只读使用.
type ServerOption func(server *TCPFixHeadServer)

// WithProtoAddr 覆盖 gnet 的监听地址, 默认是 DefaultProtoAddr(tcp://:7001).
// scheme 和地址写法跟 gnet.Run 一致, 例如:
//
//	tcp_fixed_head.NewTCPFixHeadServer() // 默认 tcp://:7001
//
//	tcp_fixed_head.NewTCPFixHeadServer(
//		tcp_fixed_head.WithProtoAddr("tcp://127.0.0.1:7001"), // 只监听本机
//	)
//
//	tcp_fixed_head.NewTCPFixHeadServer(
//		tcp_fixed_head.WithProtoAddr("tcp://"+net.JoinHostPort(ip, strconv.Itoa(port))),
//	)
//
// 必须是 "scheme://host:port" 形式: 只给 "7001" 或 ":7001" 会在 gnet.Run 里报
// invalid network address, 这里直接拒绝空串, 让配置错误尽量在构造期暴露.
func WithProtoAddr(protoAddr string) ServerOption {
	if protoAddr == "" {
		panic("tcp_fixed_head: WithProtoAddr got empty address")
	}

	return func(server *TCPFixHeadServer) {
		server.ProtoAddr = protoAddr
	}
}

// WithGnetOptions 追加传给 gnet.Run 的引擎选项 (WithReusePort / WithMulticore 等).
// 多次调用会累加, 和 gnet.Run 收变参的语义一致: 同名选项后传的生效.
func WithGnetOptions(opts ...gnet.Option) ServerOption {
	copied := make([]gnet.Option, len(opts))
	copy(copied, opts)

	return func(server *TCPFixHeadServer) {
		server.gnetOptions = append(server.gnetOptions, copied...)
	}
}

// WithHandler 设置业务处理函数. nil 表示不处理消息(defaultHandler 只做 echo).
func WithHandler(handler ServerHandler) ServerOption {
	return func(server *TCPFixHeadServer) {
		server.Handler = handler
	}
}

// WithWorkerPool 用调用方自己的 ants 池替换默认池.
// 传了就不再用 NewTCPFixHeadServer 里的默认池; nil 在构造期就拒绝,
// 否则 OnTraffic 里的 Submit 会 panic.
func WithWorkerPool(workerPool *ants.Pool) ServerOption {
	if workerPool == nil {
		panic("tcp_fixed_head: WithWorkerPool got nil worker pool")
	}

	return func(server *TCPFixHeadServer) {
		server.WorkerPool = workerPool
	}
}

// WithProtocol 换掉默认的协议解析器.
// 协议实例在构造时绑定到 server, OnTraffic 直接复用, 不再每条流量 new 一个.
// nil 在这里就拒绝, 否则 Run 会一直判成"没走构造函数".
func WithProtocol(protocol *TCPFixHeadProtocol) ServerOption {
	if protocol == nil {
		panic("tcp_fixed_head: WithProtocol got nil protocol")
	}

	return func(server *TCPFixHeadServer) {
		server.protocol = protocol
	}
}

// WithContext 设置 server 生命周期日志使用的 ctx. 不传时用 context.Background().
func WithContext(ctx context.Context) ServerOption {
	if ctx == nil {
		panic("tcp_fixed_head: WithContext got nil context")
	}

	return func(server *TCPFixHeadServer) {
		server.ctx = ctx
	}
}
