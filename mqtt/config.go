package mqtt

// PacketTypeName 给日志用. 直接打印 uint8 在排查时无法区分包类型.
func PacketTypeName(packetType PacketType) string {
	switch packetType {
	case PACKET_CONNECT:
		return "CONNECT"
	case PACKET_CONNACK:
		return "CONNACK"
	case PACKET_PUBLISH:
		return "PUBLISH"
	case PACKET_PUBACK:
		return "PUBACK"
	case PACKET_PUBREC:
		return "PUBREC"
	case PACKET_PUBREL:
		return "PUBREL"
	case PACKET_PUBCOMP:
		return "PUBCOMP"
	case PACKET_SUBSCRIBE:
		return "SUBSCRIBE"
	case PACKET_SUBACK:
		return "SUBACK"
	case PACKET_UNSUBSCRIBE:
		return "UNSUBSCRIBE"
	case PACKET_UNSUBACK:
		return "UNSUBACK"
	case PACKET_PINGREQ:
		return "PINGREQ"
	case PACKET_PINGRESP:
		return "PINGRESP"
	case PACKET_DISCONNECT:
		return "DISCONNECT"
	default:
		return "UNKNOWN"
	}
}

// ConnackReason 把 CONNACK 返回码翻译成可读原因, 日志和 demo 共用.
func ConnackReason(code byte) string {
	switch code {
	case CONNACK_ACCEPTED:
		return "accepted"
	case CONNACK_REFUSED_PROTOCOL:
		return "unacceptable protocol version"
	case CONNACK_REFUSED_ID_REJECTED:
		return "client identifier rejected"
	case CONNACK_REFUSED_NOT_AUTH:
		return "not authorized"
	default:
		return "refused"
	}
}

// isKeepAliveExpired 判断在 1.5 倍 keepalive 内是否没有收到任何控制包.
// 用整数毫秒算: keepAlive 是 uint16 秒, 乘 1500 后仍是 int 范围内的安全值.
func isKeepAliveExpired(keepAlive uint16, idleMillis int64) bool {
	if keepAlive == 0 {
		// keepalive 为 0 表示客户端不要求保活探测
		return false
	}

	return idleMillis > int64(keepAlive)*1500
}
