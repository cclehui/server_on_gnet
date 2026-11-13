package websocket

import "errors"

// 哨兵错误用标准库 errors.New, 具体原因在调用处用 %w/%+v 带出
var (
	// 连接还没握手成功就试图发消息
	ErrNotUpgraded = errors.New("websocket connection not upgraded")
)
