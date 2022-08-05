package tcp_fixed_head

import (
	"github.com/cclehui/server_on_gnet/commonutil"
)

type ServerHandler func(ctx *HandlerContext)

// 默认handler echo server
var defaultHandler ServerHandler = func(ctx *HandlerContext) {
	if ctx.ProtocalData == nil {
		return
	}

	protocal := NewTCPFixHeadProtocal()

	switch ctx.ProtocalData.ActionType {
	case ACTION_PING:
		pongData, err := protocal.EncodeData(ACTION_PONG, []byte("pong"))
		if err != nil {
			commonutil.GetLogger().Infof(ctx, "server encode pong , %v, err:%v", pongData, err)
		}

		if ctx.Conn != nil {
			ctx.Conn.AsyncWrite(pongData)
		}
	}

	commonutil.GetLogger().Infof(ctx, "服务端收到数据, data:%s", string(ctx.ProtocalData.Data))
}
