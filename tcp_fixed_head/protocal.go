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

type TCPFixHeadProtocol struct{}

// new protocal
func NewTCPFixHeadProtocol() *TCPFixHeadProtocol {
	return &TCPFixHeadProtocol{}
}

func (tcpfhp *TCPFixHeadProtocol) getHeadLength() int {
	return DefaultHeadLength
}

// server端 gnet input 数据 decode
func (tcpfhp *TCPFixHeadProtocol) Decode(c gnet.Conn) (*ProtocolData, error) {
	curConContext := c.Context()

	if curConContext == nil {
		//解析协议 header
		tempBufferLength := c.InboundBuffered()        // 当前已有多少数据
		if tempBufferLength < tcpfhp.getHeadLength() { // 不够头长度
			return nil, ErrIncompletePacket
		}

		headData, _ := c.Next(tcpfhp.getHeadLength())

		newConContext := &ProtocolData{}

		//数据长度
		bytesBuffer := bytes.NewBuffer(headData)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.Version)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.ActionType)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.DataLength)

		if newConContext.Version != PROTOCOL_VERSION ||
			!isCorrectAction(newConContext.ActionType) { //非正常协议数据
			return nil, ErrProtocolVersion
		}

		c.SetContext(newConContext)
	}

	//解析协议数据
	if protocolData, ok := c.Context().(*ProtocolData); !ok {
		c.SetContext(nil)

		return nil, ErrContext
	} else {
		tempBufferLength := c.InboundBuffered() // 当前已有多少数据
		frameDataLength := int(protocolData.DataLength)

		if tempBufferLength < frameDataLength {
			return nil, ErrIncompletePacket
		}

		// 数据够了
		data, _ := c.Next(frameDataLength)

		copyData := make([]byte, frameDataLength) // 复制
		copy(copyData, data)

		protocolData.Data = copyData

		c.SetContext(nil)

		return protocolData, nil
	}
}

// 数据反解
func (tcpfhp *TCPFixHeadProtocol) DecodeFrame(frame []byte) (*ProtocolData, error) {
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

	data.Data = frame[tcpfhp.getHeadLength():]

	return data, nil
}

// client 端获取解包后的数据
func (tcpfhp *TCPFixHeadProtocol) ClientDecode(rawConn net.Conn) (*ProtocolData, error) {
	newPackage := ProtocolData{}

	headData := make([]byte, tcpfhp.getHeadLength())

	n, err := io.ReadFull(rawConn, headData)
	if n != tcpfhp.getHeadLength() {
		return nil, err
	}

	//数据长度
	bytesBuffer := bytes.NewBuffer(headData)
	binary.Read(bytesBuffer, binary.BigEndian, &newPackage.Version)
	binary.Read(bytesBuffer, binary.BigEndian, &newPackage.ActionType)
	binary.Read(bytesBuffer, binary.BigEndian, &newPackage.DataLength)

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
