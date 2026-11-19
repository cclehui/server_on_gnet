package mqtt

import "time"

// PacketType 是 MQTT 固定头高 4 位, 3.1.1 只定义了 1..14
type PacketType byte

const (
	PACKET_CONNECT     PacketType = 1
	PACKET_CONNACK     PacketType = 2
	PACKET_PUBLISH     PacketType = 3
	PACKET_PUBACK      PacketType = 4
	PACKET_PUBREC      PacketType = 5
	PACKET_PUBREL      PacketType = 6
	PACKET_PUBCOMP     PacketType = 7
	PACKET_SUBSCRIBE   PacketType = 8
	PACKET_SUBACK      PacketType = 9
	PACKET_UNSUBSCRIBE PacketType = 10
	PACKET_UNSUBACK    PacketType = 11
	PACKET_PINGREQ     PacketType = 12
	PACKET_PINGRESP    PacketType = 13
	PACKET_DISCONNECT  PacketType = 14
)

const (
	// 只实现 3.1.1: 协议名 "MQTT", 协议级别 4.
	// 3.1 的 "MQIsdp"/3 会在 CONNACK 里明确拒绝, 而不是按 3.1.1 猜着解析.
	PROTOCOL_NAME  = "MQTT"
	PROTOCOL_LEVEL = 4
)

// CONNACK 返回码, 只有 ACCEPTED(0) 表示连接成功
const (
	CONNACK_ACCEPTED            byte = 0x00
	CONNACK_REFUSED_PROTOCOL    byte = 0x01
	CONNACK_REFUSED_ID_REJECTED byte = 0x02
	CONNACK_REFUSED_NOT_AUTH    byte = 0x05
)

const (
	// 业务处理协程池的默认容量, 和其他协议包一致(每个协议自己持有池)
	DefaultAntsPoolSize = 1024 * 1024

	// 单包上限(含固定头). remaining length 的 varint 最大能表示 256MB,
	// 但不设上限时一个伪造的长声明就能把连接的内存吊住; 和 tcp_fixed_head 的
	// MaxDataLength 取同一个量级.
	MaxPacketSize = 16 * 1024 * 1024

	// remaining length varint 的物理上限: 4 字节 / 最多 268435455
	maxRemainingLength = 268435455

	// 标准 MQTT 端口. 1024 以下需要 root, 本地跑示例时用 --port 换掉.
	DefaultProtoAddr = "tcp://:1883"

	// 建连后必须在这么长时间内发 CONNECT, 否则关连接:
	// 否则一个只发半截 CONNECT 的客户端能永久占住一个 goroutine/连接
	ConnectTimeout time.Duration = 10 * time.Second

	// keepalive 的宽限倍数: 规范要求服务端在 1.5 倍 keepalive 内没收到任何
	// 控制包就断开. 这里用整数毫秒算, 避免 duration 乘法溢出.
	keepAliveGrace = 3 // 1.5 = keepAlive*keepAliveGrace/2

	// 空闲扫描周期. 用普通 goroutine + ticker 而不是 OnTick:
	// 本仓库没有 WithTicker, OnTick 不会被调用(见 harness/knowledge/architecture.md)
	MaintenanceInterval time.Duration = time.Second
)
