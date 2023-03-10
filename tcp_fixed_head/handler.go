package tcp_fixed_head

import (
	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/panjf2000/gnet/v2"
)

type ServerHandler func(ctx *HandlerContext)

// 默认handler echo server
var defaultHandler ServerHandler = func(ctx *HandlerContext) {
	if ctx.ProtocolData == nil {
		return
	}

	protocol := NewTCPFixHeadProtocol()

	switch ctx.ProtocolData.ActionType {
	case ACTION_PING:
		pongData, err := protocol.EncodeData(ACTION_PONG, []byte("pong"))
		if err != nil {
			commonutil.GetLogger().Infof(ctx, "server encode pong , %v, err:%v", pongData, err)
		}

		if ctx.Conn != nil {
			ctx.Conn.AsyncWrite(pongData, func(c gnet.Conn, err error) error { return nil })
		}
	}

	commonutil.GetLogger().Infof(ctx, "服务端收到数据, data:%s", string(ctx.ProtocolData.Data))
}
