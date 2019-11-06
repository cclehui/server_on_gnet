package main

import (
	"bytes"
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

const (
	DefaultAntsPoolSize = 1024 * 1024
	DefaultHeadLength   = 8

	PROTOCAL_VERSION = 0x8001 //协议版本

	ACTION_PING = 0x0001 // ping行为
)

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

	//在 reactor 协程中做解码操作
	protocal := &TCPFixHeadProtocal{HeadLength: DefaultHeadLength, Conn: c}
	protocalData, err := protocal.decode()
	if err != nil {
		log.Printf("React WorkerPool Decode error :%v\n", err)
	}

	tcpfhs.WorkerPool.Submit(func() {
		//具体业务在 worker pool中处理
		if protocalData != nil {
			fmt.Printf("完整协议数据1111, %v, data:%s\n", protocalData, string(protocalData.Data))
		}

	})
	return
}

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
	Handler    func(pdata *ProtocalData)
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

			fmt.Println("xxxxxxxxxx,", newConContext, ",     ", PROTOCAL_VERSION)

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
func (tcpfhp *TCPFixHeadProtocal) encodeWrite(actionType uint16, data []byte, conn net.Conn) error {

	pdata := ProtocalData{}
	pdata.Version = PROTOCAL_VERSION
	pdata.Type = actionType
	pdata.DataLength = uint32(len(data))
	pdata.Data = data

	fmt.Println("发出的数据:", pdata)

	fmt.Println("encodeWrite,", binary.Write(conn, binary.BigEndian, &pdata.Version))
	binary.Write(conn, binary.BigEndian, &pdata.Type)
	binary.Write(conn, binary.BigEndian, &pdata.DataLength)
	binary.Write(conn, binary.BigEndian, &pdata.Data)

	return nil
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

	badData := []byte("xxx")

	for i := 1; i <= 10; i++ {
		//for i := 1; i <= 2; i++ {
		data := strings.Repeat(strconv.Itoa(i), i)
		data = data + "abc"

		if i == 2 {
			err2 := binary.Write(conn, binary.BigEndian, badData)
			fmt.Println("发送干扰数据, ", err2)
		}

		protocal := &TCPFixHeadProtocal{HeadLength: DefaultHeadLength}

		protocal.encodeWrite(ACTION_PING, []byte(data), conn)

		fmt.Println(data)

		time.Sleep(time.Second * 1)
	}

	time.Sleep(time.Second * 86400)

}
