package tcp_fixed_head

import (
	"time"

	"github.com/panjf2000/gnet"
)

type HandlerContext struct {
	ProtocalData *ProtocalData

	Conn gnet.Conn

	server    *TCPFixHeadServer
	frameData []byte
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
