package websocket

import (
	"context"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/panjf2000/ants/v2"
)

// NewEchoServer 是带默认 echo handler 的 NewServer.
// echo 作为第一个 option 传入, 所以调用方的 WithHandler 仍然能覆盖它.
func NewEchoServer(addr string, opts ...ServerOption) *WebSocketServer {
	echoOpts := make([]ServerOption, 0, len(opts)+1)
	echoOpts = append(echoOpts, WithHandler(EchoDataHandler))
	echoOpts = append(echoOpts, opts...)

	return NewServer(addr, echoOpts...)
}

// NewServer 构造 server. 先应用 opts, 再给没被 option 设置过的字段填默认值:
// option 里给了什么就用什么, 不会先建一个默认值再被覆盖掉(order: opts → defaults).
// 后传的 option 覆盖先传的.
//
// 监听地址是 "ip:port" 形式, 由调用方交给 gnet.Run:
//
//	server := websocket.NewServer(
//		"127.0.0.1:8081",
//		websocket.WithHandler(myHandler),
//	)
//	gnet.Run(server, server.Addr, gnet.WithMulticore(true))
func NewServer(addr string, opts ...ServerOption) *WebSocketServer {
	server := &WebSocketServer{}

	//ip 和端口
	server.Addr = addr

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(server)
	}

	// 下面每个默认值只在对应字段仍为空时生效.
	// 池和时间轮都会真正分配资源, 先建默认值再被覆盖就是纯泄漏(池容量 1024*1024).
	if server.AcceptOptions == nil {
		server.AcceptOptions = newDefaultAcceptOptions()
	}

	if server.ctx == nil {
		server.ctx = context.Background()
	}

	if server.connIdleSeconds == 0 {
		server.connIdleSeconds = CONN_MAX_IDLE_SECONDS
	}

	if server.WorkerPool == nil {
		server.WorkerPool = newDefaultWorkerPool() //业务处理协程池
	}

	// 时间轮的 tick 和槽数按最终的 connIdleSeconds 建, 必须在默认值都定下来之后
	if server.connTimeWheel == nil {
		server.connTimeWheel = newTimeWheel(time.Second, int(server.connIdleSeconds), timeWheelJob)
	}

	return server
}

func newDefaultWorkerPool() *ants.Pool {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	// 构造函数忽略池错误, 与 tcp_fixed_head 的写法保持一致
	defaultAntsPool, _ := ants.NewPool(DEFAULT_ANTS_POOL_SIZE, ants.WithOptions(options))

	return defaultAntsPool
}

// newDefaultAcceptOptions 保留 gobwas 版本"接受任何 Host"的行为:
// coder/websocket 默认校验 Origin, 不关掉会直接拒绝跨域握手.
// 对外暴露时请改成 OriginPatterns: []string{"your.example.com"}.
func newDefaultAcceptOptions() *coderws.AcceptOptions {
	return &coderws.AcceptOptions{
		InsecureSkipVerify: true,
	}
}
