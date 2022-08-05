package tcp_fixed_head

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/panjf2000/gnet"
)

type ProtocalData struct {
	Version    uint16 //协议版本标识
	ActionType uint16 //行为定义
	DataLength uint32
	Data       []byte

	//headDecode bool
	//Lock       sync.Mutex
}

// 协议头长度
func (p *ProtocalData) HeadLength() int {
	return DefaultHeadLength
}

type TCPFixHeadProtocal struct{}

//new protocal
func NewTCPFixHeadProtocal() *TCPFixHeadProtocal {
	return &TCPFixHeadProtocal{}
}

func (tcpfhp *TCPFixHeadProtocal) getHeadLength() int {
	return DefaultHeadLength
}

// server端 gnet input 数据 decode
func (tcpfhp *TCPFixHeadProtocal) Decode(c gnet.Conn) ([]byte, error) {
	curConContext := c.Context()
	ctx := context.Background()

	if curConContext == nil {
		//解析协议 header
		_, headData := c.ReadN(tcpfhp.getHeadLength())
		if len(headData) != tcpfhp.getHeadLength() {
			return nil, nil
		}

		newConContext := ProtocalData{}

		//数据长度
		bytesBuffer := bytes.NewBuffer(headData)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.Version)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.ActionType)
		binary.Read(bytesBuffer, binary.BigEndian, &newConContext.DataLength)

		if newConContext.Version != PROTOCAL_VERSION ||
			!isCorrectAction(newConContext.ActionType) {
			//非正常协议数据 重置buffer
			c.ResetBuffer()

			err := errors.New("not normal protocal data, reset buffer")
			commonutil.GetLogger().Errorf(ctx, "unknow protocal:%+v", err)

			return nil, err
		}

		c.SetContext(newConContext)
	}

	//解析协议数据
	if protocalData, ok := c.Context().(ProtocalData); !ok {
		c.SetContext(nil)
		c.ResetBuffer()

		return nil, errors.New("context 数据异常")
	} else {
		tempBufferLength := c.BufferLength() // 当前已有多少数据
		frameDataLength := tcpfhp.getHeadLength() + int(protocalData.DataLength)

		if tempBufferLength < frameDataLength {
			return nil, nil
		}

		// 数据够了
		_, data := c.ReadN(frameDataLength)

		c.SetContext(nil)
		c.ShiftN(frameDataLength) // 前移

		return data, nil
	}
}

// 数据反解
func (tcpfhp *TCPFixHeadProtocal) DecodeFrame(frame []byte) (*ProtocalData, error) {
	data := &ProtocalData{}
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
func (tcpfhp *TCPFixHeadProtocal) ClientDecode(rawConn net.Conn) (*ProtocalData, error) {
	newPackage := ProtocalData{}

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

//output 数据编码
func (tcpfhp *TCPFixHeadProtocal) EncodeWrite(actionType uint16, data []byte, conn net.Conn) error {

	if conn == nil {
		return errors.New("con 为空")
	}

	pdata := ProtocalData{}
	pdata.Version = PROTOCAL_VERSION
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

func (tcpfhp *TCPFixHeadProtocal) Encode(c gnet.Conn, buf []byte) ([]byte, error) {
	return buf, nil
}

//数据编码
func (tcpfhp *TCPFixHeadProtocal) EncodeData(actionType uint16, data []byte) ([]byte, error) {
	pdata := ProtocalData{}
	pdata.Version = PROTOCAL_VERSION
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
