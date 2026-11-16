package tcp_fixed_head

import (
	"time"

	"github.com/panjf2000/gnet/v2"
)

type HandlerContext struct {
	ProtocolData *ProtocolData

	Conn gnet.Conn

	server *TCPFixHeadServer
}

// Protocol 返回 server 构造时绑定的协议实例, 业务侧回包时用它编码,
// 这样 EncodeData/EncodeWrite 的头长度和服务端配置保持一致.
// 手写 HandlerContext 且没有 server 时退回默认协议.
func (ctx *HandlerContext) Protocol() *TCPFixHeadProtocol {
	if ctx == nil || ctx.server == nil || ctx.server.protocol == nil {
		return NewTCPFixHeadProtocol()
	}

	return ctx.server.protocol
}

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
