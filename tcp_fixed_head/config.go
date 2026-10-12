package tcp_fixed_head

import (
	"bytes"
	"errors"
	"sync"
)

const (
	DefaultAntsPoolSize = 1024 * 1024

	DefaultHeadLength = 8

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
var ErrIncompletePacket = errors.New("incomplete packet")
var ErrContext = errors.New("context error")

var bytesBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

func isCorrectAction(actionType uint16) bool {
	switch actionType {
	case ACTION_PING, ACTION_PONG, ACTION_DATA:
		return true
	default:
		return false
	}
}
