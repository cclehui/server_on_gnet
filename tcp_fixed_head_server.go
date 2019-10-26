package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/panjf2000/gnet"
)

//网络编程
//tcp server 简单的 固定头部长度协议

type TCPFixHeadServer struct {
	*gnet.EventServer
	Port int
}

func (tcpfhs *TCPFixHeadServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("Echo server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}
func (tcpfhs *TCPFixHeadServer) React(c gnet.Conn) (out []byte, action gnet.Action) {
	out = c.Read()
	c.ResetBuffer()
	return
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

	tcpServer := new(TCPFixHeadServer)
	tcpServer.Port = port

	log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

}
