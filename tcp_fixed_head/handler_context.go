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
