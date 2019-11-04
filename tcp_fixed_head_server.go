package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

//网络编程
//tcp server 简单的 固定头部长度协议

const DefaultAntsPoolSize = 1024 * 1024
const DefaultHeadLength = 6

func NewTCPFixHeadServer(port int) *TCPFixHeadServer {

	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &TCPFixHeadServer{}

	server.Port = port
	server.WorkerPool = defaultAntsPool

	return server
}

type TCPFixHeadServer struct {
	*gnet.EventServer
	Port       int
	WorkerPool *ants.Pool
}

func (tcpfhs *TCPFixHeadServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("Echo server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}
func (tcpfhs *TCPFixHeadServer) React(c gnet.Conn) (out []byte, action gnet.Action) {
	fmt.Println("rrrrrrrrrrrr")

	tcpfhs.WorkerPool.Submit(func() {
		protocal := &TCPFixHeadProtocal{HeadLength: DefaultHeadLength, Conn: c}

		if protocalData, err := protocal.decode(); protocalData != nil {

			fmt.Printf("完整协议数据1111, %v, error:%v\n", protocalData, err)

		}
	})
	return
}

type ProtocalData struct {
	Type       uint16
	DataLength uint32
	Data       []byte
}

type TCPFixHeadProtocal struct {
	HeadLength int
	Conn       gnet.Conn
	Handler    func(pdata *ProtocalData)
}

// input 数据 decode
func (tcpfhp *TCPFixHeadProtocal) decode() (*ProtocalData, error) {

	curConContext := tcpfhp.Conn.Context()

	if curConContext == nil {
		//解析协议 header
		if tempSize, headData := tcpfhp.Conn.ReadN(tcpfhp.HeadLength); tempSize == tcpfhp.HeadLength {

			fmt.Println("hhhhhhhhh,", headData)

			newConContext := ProtocalData{}
			//数据长度
			/*
				var length uint32
				lengthBytesBuffer := bytes.NewBuffer(headData[1:tcpfhp.HeadLength])
				err := binary.Read(lengthBytesBuffer, binary.BigEndian, &length)
				if err != nil {
					fmt.Println("eeee11111,", err)
					return nil, err
				}
				newConContext.DataLength = length
			*/

			newConContext.DataLength = binary.BigEndian.Uint32(headData[2:tcpfhp.HeadLength])

			//数据类型
			/*
				var dataType int8
				typeBytesBuffer := bytes.NewReader(headData[0:1])
				err2 := binary.Read(typeBytesBuffer, binary.BigEndian, &dataType)
				if err2 != nil {
					return nil, err2
				}
				newConContext.Type = dataType
			*/
			newConContext.Type = binary.BigEndian.Uint16(headData[0:2])

			fmt.Println("hhhhhhhhh, 2222222222,", newConContext)

			tcpfhp.Conn.SetContext(newConContext)

			return nil, nil

		} else {
			return nil, nil
		}

	} else {
		//解析协议数据
		if protocalData, ok := curConContext.(ProtocalData); !ok {
			tcpfhp.Conn.SetContext(nil)
			return nil, errors.New("context 数据异常")

		} else {
			dataLength := int(protocalData.DataLength)

			if dataLength < 1 {
				return &protocalData, nil
			}

			if tempSize, data := tcpfhp.Conn.ReadN(dataLength); tempSize == dataLength {
				protocalData.Data = data

				return &protocalData, nil

			} else {
				return nil, nil
			}
		}
	}
	return nil, nil
}

//output 数据编码
func (tcpfhp *TCPFixHeadProtocal) encode(output string) ([]byte, error) {

	var dataLength uint32 = uint32(len(output))

	var dataType uint16 = 0

	//result := []byte(fmt.Sprintf("0%d%s", dataLength, output))

	result := make([]byte, tcpfhp.HeadLength)
	binary.BigEndian.PutUint16(result[0:2], dataType)
	binary.BigEndian.PutUint32(result[2:tcpfhp.HeadLength], dataLength)
	result = append(result, []byte(output)...)

	return result, nil
}

type TCPFixHeadClient struct {
	Port int
}

func main() {

	var port int
	var multicore bool

	// Example command: go run main.go --port 2333 --multicore true
	flag.IntVar(&port, "port", 5000, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	tcpServer := NewTCPFixHeadServer(port)

	fmt.Printf("tttttt, %x\n", 825307185)

	go func() {
		tcpFHTestClient(port)
	}()

	log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

}

func tcpFHTestClient(port int) {

	time.Sleep(time.Second * 3)

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	//_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))

	if err != nil {
		log.Printf("tcpFHTestClient, Dail error:%v\n", err)
	}

	for i := 1; i <= 10; i++ {
		data := strings.Repeat(strconv.Itoa(i), i)

		protocal := &TCPFixHeadProtocal{HeadLength: DefaultHeadLength}

		if dataEncoded, err2 := protocal.encode(data); err2 == nil {
			fmt.Println(string(dataEncoded))
			conn.Write(dataEncoded)
		}

		fmt.Println(data)

		time.Sleep(time.Second * 1)
	}

}
