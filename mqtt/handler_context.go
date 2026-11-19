package mqtt

import (
	"time"
)

// HandlerContext 是一条已解码、已完成路由的 PUBLISH 交给业务的上下文.
// 字段只增不改: 以后加东西(比如原始字节)往这里加, 不动 ServerHandler 签名.
type HandlerContext struct {
	Packet *Packet

	Session *Session

	server *MQTTServer
}

// Server 返回所属 broker, 业务侧需要主动发消息(桥接上游等)时用它的 Publish.
func (ctx *HandlerContext) Server() *MQTTServer {
	if ctx == nil {
		return nil
	}

	return ctx.server
}

// Publish 以 broker 身份投递一条 QoS 0 消息(等价于一个客户端发布).
// 订阅匹配和 retain 语义与客户端发来的 PUBLISH 完全一致.
func (ctx *HandlerContext) Publish(topic string, payload []byte, retain bool) error {
	if ctx == nil || ctx.server == nil {
		return ErrInvalidTopic
	}

	return ctx.server.Publish(topic, payload, retain)
}

// 下面几个方法让 HandlerContext 实现 context.Context(tcp_fixed_head 的
// HandlerContext 同样如此), 这样业务里可以直接把它传给 logger.

func (ctx *HandlerContext) Deadline() (deadline time.Time, ok bool) {
	return
}

func (ctx *HandlerContext) Done() <-chan struct{} {
	return nil
}

func (ctx *HandlerContext) Err() error {
	return nil
}

func (ctx *HandlerContext) Value(key interface{}) interface{} {
	return nil
}
