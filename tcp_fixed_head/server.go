package tcp_fixed_head

import (
	"fmt"
	"log"
	"time"

	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

type ServerHandler func(pdata *ProtocalData)

var defaultHandler ServerHandler = func(pdata *ProtocalData) {
	if pdata != nil {
		fmt.Printf("完整协议数据1111, %v, data:%s\n", pdata, string(pdata.Data))
	}
}

func NewTCPFixHeadServer(port int) *TCPFixHeadServer {

	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &TCPFixHeadServer{}

	server.Port = port
	server.WorkerPool = defaultAntsPool
	server.Handler = defaultHandler

	return server
}

type TCPFixHeadServer struct {
	*gnet.EventServer
	Port       int
	WorkerPool *ants.Pool

	Handler ServerHandler
}

func (tcpfhs *TCPFixHeadServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("Echo server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}
func (tcpfhs *TCPFixHeadServer) React(c gnet.Conn) (out []byte, action gnet.Action) {

	//在 reactor 协程中做解码操作
	protocal := &TCPFixHeadProtocal{HeadLength: DefaultHeadLength, Conn: c}
	protocalData, err := protocal.decode()
	if err != nil {
		log.Printf("React WorkerPool Decode error :%v\n", err)
	}

	tcpfhs.WorkerPool.Submit(func() {
		//具体业务在 worker pool中处理
		tcpfhs.Handler(protocalData)
	})
	return
}
