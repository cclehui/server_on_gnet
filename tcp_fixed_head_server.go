package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

//网络编程
//tcp server 简单的 固定头部长度协议

const DefaultAntsPoolSize = 1024 * 1024

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
	out = c.Read()

	tcpfhs.WorkerPool.Submit(func() {
		protocal := &TCPFixHeadProtocal{Conn: c}
		protocal.decode(out)
	})
	return
}

type ProtocalData struct {
	Type       int8
	DataLength int
	data       []byte
}

type TCPFixHeadProtocal struct {
	Conn    gnet.Conn
	Handler func(pdata *ProtocalData)
}

func (tcpfhp *TCPFixHeadProtocal) decode(data []byte) (*ProtocalData, error) {
	if len(data) < 5 {
		return nil, errors.New("data not full")
	}

	fmt.Println(time.Now().Format("2006-01-02 15:04:05"), "\t", string(data))

	//if 如果是整个解析完成
	//tcpfhp.Conn.ResetBuffer()

	return nil, nil
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

	log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

}
