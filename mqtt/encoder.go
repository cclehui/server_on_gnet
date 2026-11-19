package mqtt

import (
	"bytes"

	"github.com/eclipse/paho.mqtt.golang/packets"
)

// 下行编码全部走 paho 的 packets.Write, 不再手写固定头和 varint:
// 手写的那份正是最容易和 paho 的解析细节对不上的地方.

// EncodeConnack 编码 CONNACK. sessionPresent 恒为 false: 本 broker 不保存会话
// (见 Session 注释), 永远没有"恢复的旧会话"可以声明.
func EncodeConnack(code byte) []byte {
	packet := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
	packet.SessionPresent = false
	packet.ReturnCode = code

	return encodePacket(packet)
}

// EncodeSuback 编码 SUBACK. codes 每项对应一个订阅: 0x00=授予 QoS 0, 0x80=失败.
func EncodeSuback(packetID uint16, codes []byte) []byte {
	packet := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
	packet.MessageID = packetID
	packet.ReturnCodes = codes

	return encodePacket(packet)
}

func EncodeUnsuback(packetID uint16) []byte {
	packet := packets.NewControlPacket(packets.Unsuback).(*packets.UnsubackPacket)
	packet.MessageID = packetID

	return encodePacket(packet)
}

func EncodePingresp() []byte {
	return encodePacket(packets.NewControlPacket(packets.Pingresp))
}

// EncodePublish 编码一条下行 PUBLISH, 恒为 QoS 0.
// 带 packet id 的 QoS>0 版本不会从这里产出: 订阅端被授予的上限也是 QoS 0.
func EncodePublish(topic string, payload []byte, retain bool) []byte {
	packet := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
	packet.TopicName = topic
	packet.Payload = payload
	packet.Qos = 0
	packet.Retain = retain
	packet.Dup = false

	return encodePacket(packet)
}

func encodePacket(packet packets.ControlPacket) []byte {
	var buf bytes.Buffer

	// Write 只在内存 buffer 上失败的可能不存在, 忽略错误是安全的;
	// 真要失败也只会得到半帧, 而半帧在客户端侧会被 varint/长度校验挡掉.
	_ = packet.Write(&buf)

	return buf.Bytes()
}
