package websocket

import (
	"context"
	"fmt"
	"time"

	coderws "github.com/coder/websocket"

	"github.com/cclehui/server_on_gnet/commonutil"
)

type DataHandlerParam struct {
	Request []byte

	MessageType coderws.MessageType

	WSConn *WSConn //升级后的连接, 发下行消息用它(WriteMessage / Server.SendDownStreamMsg)

	Server *WebSocketServer
}

type DataHandler func(param *DataHandlerParam)

// 简单的echo server
func EchoDataHandler(param *DataHandlerParam) {
	commonutil.GetLogger().Infof(context.Background(), "[server] echo 收到数据, type:%d, data:%q",
		param.MessageType, string(param.Request))

	response := fmt.Sprintf("response is :%s, 当前时间:%s\n", string(param.Request), time.Now().Format("2006-01-02 15:04:05"))

	if err := param.Server.SendDownStreamMsg(param.WSConn, param.MessageType, []byte(response)); err != nil {
		commonutil.GetLogger().Errorf(context.Background(), "[server] echo 回写失败, err:%+v", err)
	}
}
