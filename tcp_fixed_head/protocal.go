package tcp_fixed_head

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/panjf2000/gnet"
)

type ProtocalData struct {
	Version    uint16 //协议版本标识
	Type       uint16 //行为定义
	DataLength uint32
	Data       []byte

	//headDecode bool
	//Lock       sync.Mutex
}

type TCPFixHeadProtocal struct {
	HeadLength int
	Conn       gnet.Conn
}

func NewTCPFixHeadProtocal() *TCPFixHeadProtocal {
	return &TCPFixHeadProtocal{HeadLength: DefaultHeadLength}
}

// input 数据 decode
func (tcpfhp *TCPFixHeadProtocal) decode() (*ProtocalData, error) {

	curConContext := tcpfhp.Conn.Context()

	if curConContext == nil {
		//解析协议 header
		if tempSize, headData := tcpfhp.Conn.ReadN(tcpfhp.HeadLength); tempSize == tcpfhp.HeadLength {

			newConContext := ProtocalData{}
			//数据长度
			bytesBuffer := bytes.NewBuffer(headData)
			binary.Read(bytesBuffer, binary.BigEndian, &newConContext.Version)
			binary.Read(bytesBuffer, binary.BigEndian, &newConContext.Type)
			binary.Read(bytesBuffer, binary.BigEndian, &newConContext.DataLength)

			if newConContext.Version != PROTOCAL_VERSION {
				//非正常协议数据 重置buffer
				tcpfhp.Conn.ResetBuffer()
				return nil, errors.New("not normal protocal data, reset buffer")
			}

			tcpfhp.Conn.SetContext(newConContext)

		} else {
			return nil, nil
		}
	}

	//解析协议数据
	if protocalData, ok := tcpfhp.Conn.Context().(ProtocalData); !ok {
		tcpfhp.Conn.SetContext(nil)
		return nil, errors.New("context 数据异常")

	} else {
		dataLength := int(protocalData.DataLength)

		if dataLength < 1 {
			tcpfhp.Conn.SetContext(nil)
			return &protocalData, nil
		}

		if tempSize, data := tcpfhp.Conn.ReadN(dataLength); tempSize == dataLength {
			protocalData.Data = data

			tcpfhp.Conn.SetContext(nil)
			return &protocalData, nil

		} else {
			return nil, nil
		}
	}

	return nil, nil
}

//output 数据编码
func (tcpfhp *TCPFixHeadProtocal) EncodeWrite(actionType uint16, data []byte, conn net.Conn) error {

	if conn == nil {
		return errors.New("con 为空")
	}

	pdata := ProtocalData{}
	pdata.Version = PROTOCAL_VERSION
	pdata.Type = actionType
	pdata.DataLength = uint32(len(data))
	pdata.Data = data

	if err := binary.Write(conn, binary.BigEndian, &pdata.Version); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite version error , %v", err))
	}

	if err := binary.Write(conn, binary.BigEndian, &pdata.Type); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite type error , %v", err))
	}

	if err := binary.Write(conn, binary.BigEndian, &pdata.DataLength); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite datalength error , %v", err))
	}

	if err := binary.Write(conn, binary.BigEndian, &pdata.Data); err != nil {
		return errors.New(fmt.Sprintf("encodeWrite data error , %v", err))
	}

	return nil
}
