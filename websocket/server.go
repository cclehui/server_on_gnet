package websocket

import (
	"log"
	"time"

	"github.com/gobwas/ws/wsutil"
	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

type WebSocketServer struct {
	*gnet.EventServer
	Port       int
	WorkerPool *ants.Pool

	ConnNum int

	Handler DataHandler
}

func NewEchoServer(port int) *WebSocketServer {

	server := newServer(port)

	server.Handler = EchoDataHandler

	return server
}

func newServer(port int) *WebSocketServer {

	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &WebSocketServer{}

	server.Port = port
	server.WorkerPool = defaultAntsPool

	return server
}

func (server *WebSocketServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("websocket server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}

func (server *WebSocketServer) OnOpened(c gnet.Conn) (out []byte, action gnet.Action) {
	server.ConnNum = server.ConnNum + 1
	return
}

func (server *WebSocketServer) OnClosed(c gnet.Conn, err error) (action gnet.Action) {
	server.ConnNum = server.ConnNum - 1
	return
}

func (server *WebSocketServer) React(c gnet.Conn) (out []byte, action gnet.Action) {

	//fmt.Printf("react, 当前全部数据, 1111, %v\n", c.Read())
	//fmt.Printf("react, 当前全部数据, 2222, %s\n", string(c.Read()))

	if c.Context() == nil {
		//初始化协议升级器
		upgraderConn := NewDefaultUpgrader(c)

		c.SetContext(upgraderConn)
	}

	if upgraderConn, ok := c.Context().(*GnetUpgraderConn); ok {

		if !upgraderConn.IsSuccessUpgraded {
			//协议升级过程
			_, err := upgraderConn.Upgrader.Upgrade(upgraderConn)

			if err != nil {
				log.Printf("react ws 协议升级异常， %v\n", err)

			} else {
				log.Printf("react ws 协议升级成功\n")
				upgraderConn.IsSuccessUpgraded = true
			}

		} else {
			//正常的数据处理过程

			msg, op, err := wsutil.ReadClientData(upgraderConn)
			//在 reactor 协程中做解码操作

			if err == nil {
				//log.Printf("本次收到的消息, op:%v,  msg:%v\n", op, msg)

				server.WorkerPool.Submit(func() {
					//具体业务在 worker pool中处理
					handlerParam := &DataHandlerParam{}
					handlerParam.OpCode = op
					handlerParam.Request = msg
					handlerParam.Writer = upgraderConn
					handlerParam.server = server

					server.Handler(handlerParam)
				})

			} else {
				log.Printf("本次收到的消息不完整, op:%v,  msg:%v, error:%v\n", op, msg, err)
			}

		}

	} else {
		log.Println("react contenxt 数据格式异常")
	}

	return
}
