package mqtt

import (
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"golang.org/x/net/context"
)

// NewMQTTServer 构造 broker. 先应用 opts, 再给没被 option 设置过的字段填默认值
// (order: opts → defaults), 后传的 option 覆盖先传的.
//
//	mqttServer := mqtt.NewMQTTServer()          // 默认 tcp://:1883
//	mqttServer := mqtt.NewMQTTServer(
//		mqtt.WithProtoAddr("tcp://127.0.0.1:1884"), // 1024 以下需要 root, 本地常用高位端口
//		mqtt.WithHandler(myHandler),
//	)
//	mqttServer.Run()
func NewMQTTServer(opts ...ServerOption) *MQTTServer {
	server := &MQTTServer{
		// buffered: OnBoot 在 reactor 里同步发送, 信号协程还没起来也不能阻塞事件循环
		engineCh: make(chan gnet.Engine, 1),
		preAuth:  make(map[gnet.Conn]preAuthConn),
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(server)
	}

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

	// 跨连接状态在构造期建好, Run 之后只读写不重建
	if server.subscriptions == nil {
		server.subscriptions = NewSubscriptionTree()
	}

	if server.sessions == nil {
		server.sessions = NewSessionRegistry()
	}

	if server.retained == nil {
		server.retained = newRetainedStore()
	}

	return server
}

func newDefaultWorkerPool() *ants.Pool {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	// 构造函数忽略池错误, 参数是静态合法的
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	return defaultAntsPool
}
