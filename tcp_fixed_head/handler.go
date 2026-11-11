package tcp_fixed_head

import (
	"fmt"

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

	// 日志统一带 [server] 前缀, 和 client 侧日志区分开
	commonutil.GetLogger().Infof(ctx, "[server] 收到 %s, from:%s, data:%q",
		ActionName(ctx.ProtocolData.ActionType), remoteAddr(ctx.Conn),
		string(ctx.ProtocolData.Data))

	switch ctx.ProtocolData.ActionType {
	case ACTION_PING:
		pongData, err := protocol.EncodeData(ACTION_PONG,
			[]byte(fmt.Sprintf("pong, %s", string(ctx.ProtocolData.Data))))
		if err != nil {
			commonutil.GetLogger().Errorf(ctx, "[server] 编码 PONG 失败, err:%v", err)
			return
		}

		if ctx.Conn != nil {
			ctx.Conn.AsyncWrite(pongData, func(c gnet.Conn, err error) error { return nil })
			commonutil.GetLogger().Infof(ctx, "[server] 回复 PONG, to:%s", remoteAddr(ctx.Conn))
		}
	}
}

func remoteAddr(c gnet.Conn) string {
	if c == nil || c.RemoteAddr() == nil {
		return "unknown"
	}

	return c.RemoteAddr().String()
}
