package mqtt

import (
	"golang.org/x/net/context"

	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
)

// ServerOption 是构造期配置, 和 tcp_fixed_head/websocket 保持同一种形态:
// func(*MQTTServer), 每个 WithX 在构造期校验, nil 直接 panic.
type ServerOption func(server *MQTTServer)

// WithProtoAddr 覆盖监听地址, 默认 tcp://:1883. 必须是 "scheme://host:port"
// 形式; 只给 ":1883" 会在 gnet.Run 里报 invalid network address, 这里先拒绝空串.
func WithProtoAddr(protoAddr string) ServerOption {
	if protoAddr == "" {
		panic("mqtt: WithProtoAddr got empty address")
	}

	return func(server *MQTTServer) {
		server.ProtoAddr = protoAddr
	}
}

// WithGnetOptions 追加 gnet 引擎选项; 多次调用累加.
func WithGnetOptions(opts ...gnet.Option) ServerOption {
	copied := make([]gnet.Option, len(opts))
	copy(copied, opts)

	return func(server *MQTTServer) {
		server.gnetOptions = append(server.gnetOptions, copied...)
	}
}

// WithHandler 设置业务处理函数. 不设置时用 defaultHandler(只打 Debug 日志);
// 协议层的 CONNACK/SUBACK/PINGRESP/订阅分发始终由 broker 自己处理.
func WithHandler(handler ServerHandler) ServerOption {
	if handler == nil {
		panic("mqtt: WithHandler got nil handler")
	}

	return func(server *MQTTServer) {
		server.Handler = handler
	}
}

// WithWorkerPool 用调用方自己的 ants 池替换默认池. nil 在构造期拒绝,
// 否则 OnTraffic 里的 Submit 会 panic.
func WithWorkerPool(workerPool *ants.Pool) ServerOption {
	if workerPool == nil {
		panic("mqtt: WithWorkerPool got nil worker pool")
	}

	return func(server *MQTTServer) {
		server.WorkerPool = workerPool
	}
}

// RetainSession 声明是否接受 cleanSession=0 的连接.
//
// 本 broker 不保存离线会话, 只能"接受并忽略"cleanSession=0. 默认 true:
// 大多数客户端库默认 cleanSession=true, 但少数库(Qt/部分嵌入式)会发 0,
// 直接拒绝会让它们完全连不上. 设为 false 时对 cleanSession=0 返回
// CONNACK 0x02(client identifier rejected), 语义更严格.
func RetainSession(accept bool) ServerOption {
	return func(server *MQTTServer) {
		server.acceptPersistentSessions = accept
	}
}

// WithContext 设置生命周期日志使用的 ctx, 不传时用 context.Background().
func WithContext(ctx context.Context) ServerOption {
	if ctx == nil {
		panic("mqtt: WithContext got nil context")
	}

	return func(server *MQTTServer) {
		server.ctx = ctx
	}
}
