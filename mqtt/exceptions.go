package mqtt

import "errors"

// 哨兵错误用标准库 errors.New: 包初始化时就固化的 pkg/errors 栈对定位没有帮助.
// ErrIncompletePacket 是唯一的"等更多数据"信号, OnTraffic 见到它就 return gnet.None.
var (
	ErrIncompletePacket      = errors.New("incomplete packet")
	ErrMalformedLength       = errors.New("malformed remaining length")
	ErrMalformedPacket       = errors.New("malformed packet body")
	ErrUnknownPacketType     = errors.New("unknown packet type")
	ErrInvalidFlags          = errors.New("invalid fixed header flags")
	ErrProtocolName          = errors.New("unsupported protocol name")
	ErrProtocolLevel         = errors.New("unsupported protocol level")
	ErrConnectFlagReserved   = errors.New("connect flag reserved bit set")
	ErrMissingField          = errors.New("missing required field")
	ErrTrailingBytes         = errors.New("trailing bytes after packet body")
	ErrPacketTooLarge        = errors.New("packet too large")
	ErrQoSNotSupported       = errors.New("qos > 0 not supported")
	ErrInvalidTopic          = errors.New("invalid topic name")
	ErrInvalidTopicFilter    = errors.New("invalid topic filter")
	ErrSessionNotEstablished = errors.New("packet received before CONNECT")
	ErrSessionClosed         = errors.New("session already closed")
	ErrSecondConnect         = errors.New("second CONNECT on one connection")
)
