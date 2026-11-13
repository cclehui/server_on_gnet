package tcp_fixed_head

import "errors"

// 哨兵错误用标准库 errors.New: pkg/errors 在包初始化时就会固化调用栈,
// 日志里打印出来是 init 的栈, 对定位问题没有帮助
var (
	ErrProtocolVersion    = errors.New("PROTOCOL_VERSION error")
	ErrUnknownAction      = errors.New("unknown action type")
	ErrDataLengthExceeded = errors.New("data length exceeded")
	ErrIncompletePacket   = errors.New("incomplete packet")
	ErrContext            = errors.New("context error")
)
