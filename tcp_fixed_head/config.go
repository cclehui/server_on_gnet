package tcp_fixed_head

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

const (
	DefaultAntsPoolSize = 1024 * 1024

	DefaultHeadLength = 8

	// 单帧 body 的长度上限。DataLength 是 uint32, 不设上限时一个伪造的超大长度
	// 会让连接一直等数据; 且 uint32 -> int 在 32 位平台会溢出成负数,
	// 变成 c.Next(负数), 而 gnet 的 Next(n<=0) 表示"读完整个缓冲区"
	MaxDataLength = 16 * 1024 * 1024

	PROTOCOL_VERSION = 0x8001 //协议版本 dec 32769

	socketRingBufferSize = 1024

	//协议行为定义
	ACTION_PING = 0x0001 // ping行为
	ACTION_PONG = 0x0002 // pong行为
	ACTION_DATA = 0x00F0 // 业务行为

)

// 哨兵错误用标准库 errors.New: pkg/errors 在包初始化时就会固化调用栈,
// 日志里打印出来是 init 的栈, 对定位问题没有帮助
var ErrProtocolVersion = errors.New("PROTOCOL_VERSION error")
var ErrUnknownAction = errors.New("unknown action type")
var ErrDataLengthExceeded = errors.New("data length exceeded")
var ErrIncompletePacket = errors.New("incomplete packet")
var ErrContext = errors.New("context error")

var bytesBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// ActionName 把 ActionType 转成日志里可读的名字
func ActionName(actionType uint16) string {
	switch actionType {
	case ACTION_PING:
		return "PING"
	case ACTION_PONG:
		return "PONG"
	case ACTION_DATA:
		return "DATA"
	default:
		return fmt.Sprintf("UNKNOWN(0x%04X)", actionType)
	}
}

func isCorrectAction(actionType uint16) bool {
	switch actionType {
	case ACTION_PING, ACTION_PONG, ACTION_DATA:
		return true
	default:
		return false
	}
}
