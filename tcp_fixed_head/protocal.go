package tcp_fixed_head

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/pkg/errors"

	"github.com/panjf2000/gnet/v2"
)

type ProtocolData struct {
	Version    uint16 //协议版本标识
	ActionType uint16 //行为定义
	DataLength uint32
	Data       []byte

	//headDecode bool
	//Lock       sync.Mutex
}

// 协议头长度
func (p *ProtocolData) HeadLength() int {
	return DefaultHeadLength
}

type TCPFixHeadProtocol struct {
	// HeadLength 是协议头长度(字节). 默认 DefaultHeadLength, 由 NewTCPFixHeadProtocol 填;
	// 以后头里加字段时改这里, 不用再翻每个取长度的地方.
	HeadLength int
}

// 协议 option
type ProtocolOption func(protocol *TCPFixHeadProtocol)

// WithHeadLength 覆盖协议头长度. 只适用于头和默认布局兼容的变体,
// Decode/DecodeFrame 仍然按 Version/ActionType/DataLength 的顺序读固定字段.
func WithHeadLength(headLength int) ProtocolOption {
	if headLength < DefaultHeadLength {
		panic("tcp_fixed_head: WithHeadLength must be >= DefaultHeadLength")
	}

	return func(protocol *TCPFixHeadProtocol) {
		protocol.HeadLength = headLength
	}
}

// new protocal
func NewTCPFixHeadProtocol(opts ...ProtocolOption) *TCPFixHeadProtocol {
	protocol := &TCPFixHeadProtocol{HeadLength: DefaultHeadLength}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(protocol)
	}

	return protocol
}

// headLength 返回生效的头长度: 手写 &TCPFixHeadProtocol{} 时兜底成默认值,
// 避免零值把 header 全读没.
func (tcpfhp *TCPFixHeadProtocol) headLength() int {
	if tcpfhp.HeadLength <= 0 {
		return DefaultHeadLength
	}

	return tcpfhp.HeadLength
}

// server端 gnet input 数据 decode
func (tcpfhp *TCPFixHeadProtocol) Decode(c gnet.Conn) (*ProtocolData, error) {
	headLength := tcpfhp.headLength()

	var protocolData *ProtocolData

	if ctx := c.Context(); ctx != nil {
		parsed, ok := ctx.(*ProtocolData)
		if !ok {
			c.SetContext(nil)
			return nil, ErrContext
		}

		protocolData = parsed
	}

	if protocolData == nil {
		// 解析协议 header
		if c.InboundBuffered() < headLength { // 不够头长度
			return nil, ErrIncompletePacket
		}

		headData, err := c.Next(headLength)
		if err != nil {
			return nil, err
		}

		protocolData = &ProtocolData{}

		//数据长度
		bytesBuffer := bytes.NewBuffer(headData)
		if err := binary.Read(bytesBuffer, binary.BigEndian, &protocolData.Version); err != nil {
			return nil, err
		}

		if err := binary.Read(bytesBuffer, binary.BigEndian, &protocolData.ActionType); err != nil {
			return nil, err
		}

		if err := binary.Read(bytesBuffer, binary.BigEndian, &protocolData.DataLength); err != nil {
			return nil, err
		}

		// 错误信息里带上实际收到的值和期望值, 否则日志只有一句
		// "PROTOCOL_VERSION error", 分不清是对端发错、还是流已经错位
		if protocolData.Version != PROTOCOL_VERSION { //非正常协议数据
			return nil, fmt.Errorf("%w: got version=0x%04X action=0x%04X, want version=0x%04X",
				ErrProtocolVersion, protocolData.Version, protocolData.ActionType, PROTOCOL_VERSION)
		}

		if !isCorrectAction(protocolData.ActionType) {
			return nil, fmt.Errorf("%w: got action=0x%04X, want one of 0x%04X/0x%04X/0x%04X",
				ErrUnknownAction, protocolData.ActionType, ACTION_PING, ACTION_PONG, ACTION_DATA)
		}

		// DataLength 是 uint32, 不校验上限会让一条伪造的小包头把连接挂在等数据状态;
		// 且在 32 位平台上转 int 会溢出成负数
		if protocolData.DataLength > MaxDataLength {
			return nil, fmt.Errorf("%w: got %d bytes, max %d",
				ErrDataLengthExceeded, protocolData.DataLength, MaxDataLength)
		}

		c.SetContext(protocolData)
	}

	//解析协议数据
	frameDataLength := int(protocolData.DataLength)

	if c.InboundBuffered() < frameDataLength {
		return nil, ErrIncompletePacket
	}

	// 注意: gnet 的 Next(n) 在 n <= 0 时表示"把缓冲区里的数据全部读出来",
	// 零长 body 必须先跳过, 否则会把同一缓冲里的下一个包一起吃掉
	if frameDataLength > 0 {
		data, err := c.Next(frameDataLength)
		if err != nil {
			return nil, err
		}

		copyData := make([]byte, len(data)) // 复制, 不能引用 gnet 复用的缓冲区
		copy(copyData, data)

		protocolData.Data = copyData
	}

	c.SetContext(nil)

	return protocolData, nil
}

// 数据反解
func (tcpfhp *TCPFixHeadProtocol) DecodeFrame(frame []byte) (*ProtocolData, error) {
	// 半帧直接返回裸的 ErrIncompletePacket: 它是控制流哨兵, 调用方用 == 比较,
	// 包一层会破坏这个约定
	if len(frame) < tcpfhp.headLength() {
		return nil, ErrIncompletePacket
	}

	data := &ProtocolData{}
	//数据长度
	bytesBuffer := bytes.NewBuffer(frame)

	if err := binary.Read(bytesBuffer, binary.BigEndian, &data.Version); err != nil {
		return nil, err
	}

	if err := binary.Read(bytesBuffer, binary.BigEndian, &data.ActionType); err != nil {
		return nil, err
	}

	if err := binary.Read(bytesBuffer, binary.BigEndian, &data.DataLength); err != nil {
		return nil, err
	}

	data.Data = frame[tcpfhp.headLength():]

	return data, nil
}

// client 端获取解包后的数据
func (tcpfhp *TCPFixHeadProtocol) ClientDecode(rawConn net.Conn) (*ProtocolData, error) {
	newPackage := ProtocolData{}

	headLength := tcpfhp.headLength()
	headData := make([]byte, headLength)

	n, err := io.ReadFull(rawConn, headData)
	if err != nil {
		return nil, err
	}
	if n != headLength {
		return nil, errors.New(fmt.Sprintf("read head error, want %d, got %d", headLength, n))
	}

	//数据长度
	bytesBuffer := bytes.NewBuffer(headData)
	if err := binary.Read(bytesBuffer, binary.BigEndian, &newPackage.Version); err != nil {
		return nil, err
	}

	if err := binary.Read(bytesBuffer, binary.BigEndian, &newPackage.ActionType); err != nil {
		return nil, err
	}

	if err := binary.Read(bytesBuffer, binary.BigEndian, &newPackage.DataLength); err != nil {
		return nil, err
	}

	if newPackage.DataLength > MaxDataLength {
		return nil, ErrDataLengthExceeded
	}

	if newPackage.DataLength < 1 {
		return &newPackage, nil
	}

	data := make([]byte, newPackage.DataLength)
	dataNum, err2 := io.ReadFull(rawConn, data)

	if uint32(dataNum) != newPackage.DataLength {
		return nil, errors.New(fmt.Sprintf("read data error, %v", err2))
	}

	newPackage.Data = data

	return &newPackage, nil
}

// output 数据编码
func (tcpfhp *TCPFixHeadProtocol) EncodeWrite(actionType uint16, data []byte, conn net.Conn) error {

	if conn == nil {
		return errors.New("con 为空")
	}

	pdata := ProtocolData{}
	pdata.Version = PROTOCOL_VERSION
	pdata.ActionType = actionType
	pdata.DataLength = uint32(len(data))
	pdata.Data = data

	if err := binary.Write(conn, binary.BigEndian, &pdata.Version); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite version error , %v", err))
	}

	if err := binary.Write(conn, binary.BigEndian, &pdata.ActionType); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite type error , %v", err))
	}

	if err := binary.Write(conn, binary.BigEndian, &pdata.DataLength); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite datalength error , %v", err))
	}

	if pdata.DataLength > 0 {
		if err := binary.Write(conn, binary.BigEndian, &pdata.Data); err != nil {
			return errors.New(fmt.Sprintf("encodeWrite data error , %v", err))
		}
	}

	return nil
}

func (tcpfhp *TCPFixHeadProtocol) Encode(c gnet.Conn, buf []byte) ([]byte, error) {
	return buf, nil
}

// 数据编码
func (tcpfhp *TCPFixHeadProtocol) EncodeData(actionType uint16, data []byte) ([]byte, error) {
	pdata := ProtocolData{}
	pdata.Version = PROTOCOL_VERSION
	pdata.ActionType = actionType
	pdata.DataLength = uint32(len(data))
	pdata.Data = data

	result := make([]byte, 0)

	buffer := bytes.NewBuffer(result)

	if err := binary.Write(buffer, binary.BigEndian, &pdata.Version); err != nil {
		return nil, errors.New(fmt.Sprintf("encode version error , %v", err))
	}

	if err := binary.Write(buffer, binary.BigEndian, &pdata.ActionType); err != nil {
		return nil, errors.New(fmt.Sprintf("encode type error , %v", err))
	}

	if err := binary.Write(buffer, binary.BigEndian, &pdata.DataLength); err != nil {
		return nil, errors.New(fmt.Sprintf("encode datalength error , %v", err))
	}

	if pdata.DataLength > 0 {
		if err := binary.Write(buffer, binary.BigEndian, &pdata.Data); err != nil {
			return nil, errors.New(fmt.Sprintf("encode data error , %v", err))
		}
	}

	return buffer.Bytes(), nil
}
