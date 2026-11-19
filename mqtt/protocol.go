package mqtt

import (
	"fmt"
	"io"

	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/panjf2000/gnet/v2"
)

// Packet 是一帧已经解码、且已经和 gnet 缓冲区解耦的 MQTT 包.
//
// 解码用 eclipse/paho.mqtt.golang 的 packets 包: 那是 paho 客户端自己的
// 编解码实现, 也是本仓库唯一从外部引入的协议实现. 但 paho 只提供阻塞式的
// ReadPacket(io.Reader), 直接喂 gnet 的 Conn 会卡住 event loop
// (Conn.Read 是 event-loop-only, SetDeadline 也不支持). 所以这里仍由本包
// 负责"从 reactor 缓冲区里切出一个完整帧"这一步, 再交给 paho 解字段:
//
//	framing(本包, 非阻塞 Peek/Next)  ->  packets.NewControlPacketWithHeader + Unpack(paho)
type Packet struct {
	// Type 是控制包类型(CONNECT/PUBLISH/...), 取自 paho 的 FixedHeader
	Type PacketType

	// Dup / QoS / Retain 是 paho FixedHeader 里的标志位
	Dup    bool
	QoS    byte
	Retain bool

	// PacketID 只在 QoS>0 的 PUBLISH 与 SUBSCRIBE/UNSUBSCRIBE 里有效
	PacketID uint16

	Topic   string // PUBLISH 的 topic name
	Payload []byte // PUBLISH 的 body, 由 paho Unpack 新分配, 可以跨协程使用

	// CONNECT
	ClientID     string
	CleanSession bool
	KeepAlive    uint16
	WillFlag     bool
	WillTopic    string
	WillPayload  []byte
	WillQoS      byte
	WillRetain   bool
	Username     string
	Password     string

	// SUBSCRIBE / UNSUBSCRIBE
	Subscriptions []Subscription
	Filters       []string
}

// Subscription 是 SUBSCRIBE 里的一项: filter + 请求的 qos
type Subscription struct {
	Filter string
	QoS    byte
}

// Decode 从连接上取出一个完整包并解码.
// 返回 ErrIncompletePacket 时不动缓冲区, 调用方保留连接状态等下次 OnTraffic.
//
// MQTT 的帧长不是定长头: 固定头第 2 字节起是 varint 编码的 remaining length.
// 必须先把 varint 读完才知道整帧多长, 所以这里先 Peek 探长度, 确认整帧到齐后
// 再 Next 一次性消费 —— 先 Next 会把半帧吃掉, 后面的字节就错位了.
func Decode(c gnet.Conn) (*Packet, error) {
	buffered := c.InboundBuffered()
	if buffered < 2 {
		return nil, ErrIncompletePacket
	}

	remaining, lengthBytes, err := decodeRemainingLength(c, buffered)
	if err != nil {
		return nil, err
	}

	if remaining > MaxPacketSize {
		return nil, fmt.Errorf("%w: remaining length=%d, max=%d", ErrPacketTooLarge, remaining, MaxPacketSize)
	}

	total := 1 + lengthBytes + remaining
	if buffered < total {
		// 帧没到齐: 上面只 Peek 过, 没有消费任何字节
		return nil, ErrIncompletePacket
	}

	frame, err := c.Next(total)
	if err != nil {
		return nil, fmt.Errorf("%w: want %d bytes, buffered %d", ErrIncompletePacket, total, buffered)
	}

	// frame 是 gnet 的借用缓冲区, 只在本回调内有效; DecodeFrame 里 paho 的
	// Unpack 会把 topic 字符串和 payload 拷成新值, 不会留着这个 slice.
	return DecodeFrame(frame[0], frame[1+lengthBytes:])
}

// decodeRemainingLength 只做"探长度": 用 Peek 逐字节读 varint, 不消费.
// 单独拆出来是因为它和 body 解析是两件事: 前者在 buffer 不够时暂停,
// 后者面对的一定是完整帧.
func decodeRemainingLength(c gnet.Conn, buffered int) (value int, lengthBytes int, err error) {
	multiplier := 1

	for i := 0; i < 4; i++ {
		if buffered < 2+i {
			return 0, 0, ErrIncompletePacket
		}

		head, peekErr := c.Peek(2 + i)
		if peekErr != nil {
			return 0, 0, ErrIncompletePacket
		}

		digit := head[1+i]
		value += int(digit&0x7F) * multiplier

		if digit&0x80 == 0 {
			// 规范允许用超出最短长度的编码, 这里接受但不主动生成
			return value, i + 1, nil
		}

		multiplier *= 128
	}

	// 连续 4 字节都带续位: 编码非法, 不是"数据未到齐"
	return 0, 0, ErrMalformedLength
}

// DecodeFrame 解析一个已经切好的帧: header 是固定头第一字节, body 是
// remaining length 之后的内容. 字段解析交给 paho 的 packets 包,
// 本包只补 paho 不做或不适合服务端的两件事:
//   - 严格读: paho 的 decodeBytes 用 b.Read 且忽略短读, 声明长度超出 body
//     时会静默接受截断值; 这里用 io.ReadFull 包一层
//   - 服务端策略: QoS>0、非法 flags、收到客户端侧包(CONNACK 等)直接报错
func DecodeFrame(header byte, body []byte) (*Packet, error) {
	fixedHeader := packets.FixedHeader{
		MessageType:     header >> 4,
		Dup:             header&0x08 != 0,
		Qos:             (header >> 1) & 0x03,
		Retain:          header&0x01 != 0,
		RemainingLength: len(body),
	}

	// 方向校验放在 Unpack 之前: 客户端发 CONNACK/SUBACK/PINGRESP 属于对端
	// 协议栈错误, 没有任何理由再去解析它的 body. 放在后面的话, 一个字段长度
	// 也不对的 SUBACK 会先报"body 解析失败", 掩盖真正的错误.
	switch PacketType(fixedHeader.MessageType) {
	case PACKET_CONNACK, PACKET_SUBACK, PACKET_PINGRESP:
		return nil, fmt.Errorf("%w: server received %s",
			ErrUnknownPacketType, packetName(fixedHeader.MessageType))
	}

	controlPacket, err := packets.NewControlPacketWithHeader(fixedHeader)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknownPacketType, err)
	}

	// strictReader 让 paho 的短读变成错误: 越界声明(攻击者可控的
	// remaining length)不能在 Unpack 里被悄悄截断.
	reader := newStrictReader(body)
	if err = controlPacket.Unpack(reader); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedPacket, packetName(fixedHeader.MessageType), err)
	}

	return convertPacket(fixedHeader, controlPacket, reader.remaining())
}

func convertPacket(fixedHeader packets.FixedHeader, controlPacket packets.ControlPacket, trailing int) (*Packet, error) {
	packet := &Packet{
		Type:   PacketType(fixedHeader.MessageType),
		Dup:    fixedHeader.Dup,
		QoS:    fixedHeader.Qos,
		Retain: fixedHeader.Retain,
	}

	name := packetName(fixedHeader.MessageType)

	// paho 的 Unpack 不报告"body 没读完": 长度声明比实际字段长时, 多出来的
	// 字节会被静默忽略. 这种帧说明对端实现有问题, 继续按字段值处理可能
	// 得到一个看似正常但内容错位的包.
	if trailing != 0 {
		return nil, fmt.Errorf("%w: %s left=%d bytes", ErrTrailingBytes, name, trailing)
	}

	// 非 PUBLISH 包的固定头低 4 位是"必须为 0"的保留位(paho 把整个低 4 位
	// 当成 QoS/Dup/Retain 解出来), 这里按原始标志位校验.
	if err := validateFixedHeaderFlags(fixedHeader); err != nil {
		return nil, err
	}

	switch cp := controlPacket.(type) {
	case *packets.ConnectPacket:
		return convertConnect(fixedHeader, cp)
	case *packets.PublishPacket:
		if err := validateServerPublish(fixedHeader, cp); err != nil {
			return nil, err
		}

		packet.Topic = cp.TopicName
		packet.Payload = cp.Payload
		packet.PacketID = cp.MessageID
	case *packets.SubscribePacket:
		if cp.MessageID == 0 {
			return nil, fmt.Errorf("%w: SUBSCRIBE packet id=0", ErrInvalidFlags)
		}

		packet.PacketID = cp.MessageID
		for i, filter := range cp.Topics {
			if err := ValidateTopicFilter(filter); err != nil {
				return nil, fmt.Errorf("SUBSCRIBE filter: %w", err)
			}

			qos := cp.Qoss[i]
			if qos&0xFC != 0 || qos > 2 {
				return nil, fmt.Errorf("%w: SUBSCRIBE qos byte=0x%02X", ErrInvalidFlags, qos)
			}

			packet.Subscriptions = append(packet.Subscriptions, Subscription{Filter: filter, QoS: qos})
		}

		if len(packet.Subscriptions) == 0 {
			return nil, fmt.Errorf("%w: SUBSCRIBE has no topic filter", ErrMissingField)
		}
	case *packets.UnsubscribePacket:
		if cp.MessageID == 0 {
			return nil, fmt.Errorf("%w: UNSUBSCRIBE packet id=0", ErrInvalidFlags)
		}

		packet.PacketID = cp.MessageID
		for _, filter := range cp.Topics {
			if err := ValidateTopicFilter(filter); err != nil {
				return nil, fmt.Errorf("UNSUBSCRIBE filter: %w", err)
			}

			packet.Filters = append(packet.Filters, filter)
		}

		if len(packet.Filters) == 0 {
			return nil, fmt.Errorf("%w: UNSUBSCRIBE has no topic filter", ErrMissingField)
		}
	case *packets.PubackPacket:
		packet.PacketID = cp.MessageID
	case *packets.PubrecPacket:
		packet.PacketID = cp.MessageID
	case *packets.PubrelPacket:
		packet.PacketID = cp.MessageID
	case *packets.PubcompPacket:
		packet.PacketID = cp.MessageID
	case *packets.UnsubackPacket:
		packet.PacketID = cp.MessageID
	case *packets.PingreqPacket, *packets.DisconnectPacket:
		// 空 body
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnknownPacketType, controlPacket)
	}

	return packet, nil
}

func convertConnect(fixedHeader packets.FixedHeader, cp *packets.ConnectPacket) (*Packet, error) {
	if cp.ProtocolName != PROTOCOL_NAME {
		return nil, fmt.Errorf("%w: got %q", ErrProtocolName, cp.ProtocolName)
	}

	if cp.ProtocolVersion != PROTOCOL_LEVEL {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrProtocolLevel, cp.ProtocolVersion, PROTOCOL_LEVEL)
	}

	// bit0 是保留位, 规范要求为 0; 置位说明对端实现有问题, 不能猜着解析
	if cp.ReservedBit != 0 {
		return nil, ErrConnectFlagReserved
	}

	if cp.WillQos == 3 {
		return nil, fmt.Errorf("%w: CONNECT will qos=3", ErrInvalidFlags)
	}

	if !cp.WillFlag && (cp.WillQos != 0 || cp.WillRetain || cp.WillTopic != "") {
		return nil, fmt.Errorf("%w: CONNECT will fields set without will flag", ErrInvalidFlags)
	}

	if cp.PasswordFlag && !cp.UsernameFlag {
		return nil, fmt.Errorf("%w: CONNECT password without username", ErrInvalidFlags)
	}

	if cp.WillFlag {
		if err := ValidateTopicName(cp.WillTopic); err != nil {
			return nil, fmt.Errorf("CONNECT will topic: %w", err)
		}
	}

	return &Packet{
		Type:         PacketType(fixedHeader.MessageType),
		ClientID:     cp.ClientIdentifier,
		CleanSession: cp.CleanSession,
		KeepAlive:    cp.Keepalive,
		WillFlag:     cp.WillFlag,
		WillTopic:    cp.WillTopic,
		WillPayload:  cp.WillMessage,
		WillQoS:      cp.WillQos,
		WillRetain:   cp.WillRetain,
		Username:     cp.Username,
		Password:     string(cp.Password),
	}, nil
}

func validateServerPublish(fixedHeader packets.FixedHeader, cp *packets.PublishPacket) error {
	if fixedHeader.Qos == 3 {
		return fmt.Errorf("%w: PUBLISH qos=3", ErrInvalidFlags)
	}

	if fixedHeader.Qos > 0 {
		// 这个 broker 只做 QoS 0. 收到 QoS 1/2 不能"降级当 0 处理":
		// 对端会等 PUBACK/PUBREC, 永远等不到, 连接上表现为消息丢失.
		return fmt.Errorf("%w: PUBLISH qos=%d", ErrQoSNotSupported, fixedHeader.Qos)
	}

	// QoS 0 的 DUP 位规范要求为 0
	if cp.Dup {
		return fmt.Errorf("%w: PUBLISH dup=1 with qos=0", ErrInvalidFlags)
	}

	if err := ValidateTopicName(cp.TopicName); err != nil {
		return fmt.Errorf("PUBLISH topic: %w", err)
	}

	return nil
}

// strictReader 把 io.Reader 的短读变成错误. paho 的 decodeBytes 用的是
// b.Read(field) 并只看 err, 声明长度比 body 大时会返回部分数据且 err==nil,
// 那样会接受一个被截断的 topic / clientId. 这里要求每次读满, 读不满即报错.
//
// 它同时记录已消费字节数, 供调用方检查 paho 有没有留下未消费的 trailing bytes
// (paho 不暴露内部游标).
type strictReader struct {
	body  []byte
	moved int
	// strict 为 false 时退化成普通读取, 只给需要"读到 EOF"的收尾检查用
	strict bool
}

func newStrictReader(body []byte) *strictReader {
	return &strictReader{body: body, strict: true}
}

func (s *strictReader) Read(p []byte) (int, error) {
	// 零长度读必须返回 (0, nil): 空 payload 的 PUBLISH 走的就是这条路径,
	// 返回 EOF 会被 paho 当成解析失败.
	if len(p) == 0 {
		return 0, nil
	}

	if s.moved >= len(s.body) {
		return 0, io.EOF
	}

	n := copy(p, s.body[s.moved:])
	s.moved += n

	if s.strict && n != len(p) {
		return n, io.ErrUnexpectedEOF
	}

	return n, nil
}

func (s *strictReader) consumed() int {
	return s.moved
}

func (s *strictReader) remaining() int {
	return len(s.body) - s.moved
}

// validateFixedHeaderFlags 按包类型校验固定头低 4 位.
// 只有 PUBLISH(任意)、PUBREL/SUBSCRIBE/UNSUBSCRIBE(必须是 0b0010) 有非零标志;
// 其余包的低 4 位必须为 0.
func validateFixedHeaderFlags(fixedHeader packets.FixedHeader) error {
	// 还原成原始低 4 位, 免得在两个表示之间反复推导
	rawFlags := byte(boolToBit(fixedHeader.Dup)<<3 | fixedHeader.Qos<<1 | boolToBit(fixedHeader.Retain))

	switch PacketType(fixedHeader.MessageType) {
	case PACKET_PUBLISH:
		return nil
	case PACKET_PUBREL, PACKET_SUBSCRIBE, PACKET_UNSUBSCRIBE:
		if rawFlags != 0x02 {
			return fmt.Errorf("%w: %s flags=0x%X, want 0x2",
				ErrInvalidFlags, packetName(fixedHeader.MessageType), rawFlags)
		}

		return nil
	default:
		if rawFlags != 0 {
			return fmt.Errorf("%w: %s flags=0x%X, want 0x0",
				ErrInvalidFlags, packetName(fixedHeader.MessageType), rawFlags)
		}

		return nil
	}
}

func boolToBit(v bool) byte {
	if v {
		return 1
	}

	return 0
}

// packetName 用 paho 自己的名字表, 不用 PacketTypeName: 前者保证和
// packets 包认识的类型一致, 后者是本包给日志用的可读名.
func packetName(messageType byte) string {
	if name, ok := packets.PacketNames[messageType]; ok {
		return name
	}

	return "UNKNOWN"
}
