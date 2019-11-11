package tcp_fixed_head

import (
	"log"
	"time"

	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

type HandlerDataType struct {
	ProtocalData *ProtocalData

	Conn gnet.Conn

	server *TCPFixHeadServer
}
type ServerHandler func(handlerData *HandlerDataType)

var defaultHandler ServerHandler = func(handlerData *HandlerDataType) {
	if handlerData.ProtocalData == nil {
		return
	}

	protocal := NewTCPFixHeadProtocal()

	//cclehui_todo
	switch handlerData.ProtocalData.ActionType {
	case ACTION_PING:
		pongData, err := protocal.Encode(ACTION_PONG, nil)
		//log.Printf("server encode pong , %v, err:%v", pongData, err)

		if handlerData.Conn != nil {
			handlerData.Conn.AsyncWrite(pongData)
		}

	}

	log.Printf("完整协议数据1111, %v, data:%s\n", handlerData.ProtocalData, string(handlerData.ProtocalData.Data))
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

	ConnNum int

	Handler ServerHandler
}

func (tcpfhs *TCPFixHeadServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("Echo server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}

func (tcpfhs *TCPFixHeadServer) OnOpened(c gnet.Conn) (out []byte, action gnet.Action) {
	tcpfhs.ConnNum = tcpfhs.ConnNum + 1
	return
}

func (tcpfhs *TCPFixHeadServer) OnClosed(c gnet.Conn, err error) (action gnet.Action) {
	tcpfhs.ConnNum = tcpfhs.ConnNum - 1
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
		handlerData := &HandlerDataType{}
		handlerData.ProtocalData = protocalData
		handlerData.Conn = c
		handlerData.server = tcpfhs
		tcpfhs.Handler(handlerData)
	})
	return
}
