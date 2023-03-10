package tcp_fixed_head

import (
	"bytes"
	"sync"

	"github.com/pkg/errors"
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

var ErrProtocolVersion = errors.New("PROTOCOL_VERSION error close")
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
