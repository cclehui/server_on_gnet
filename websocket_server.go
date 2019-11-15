package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/cclehui/server_on_gnet/websocket"
	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

func home(w http.ResponseWriter, r *http.Request) {
	websocket.ClientTemplate.Execute(w, "ws://"+r.Host+"/echo")
}

func main() {

	go func() {
		tcpServer := &WebsocketServer(port)

		log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", 8081), gnet.WithMulticore(multicore)))

	}()

	//var addr = flag.String("addr", "localhost:8080", "http service address")
	addr := "0.0.0.0:8080"

	http.HandleFunc("/", home)
	log.Fatal(http.ListenAndServe(addr, nil))

}

type WebsocketServer struct {
	*gnet.EventServer
	Port       int
	WorkerPool *ants.Pool

	ConnNum int
}

func (server *WebsocketServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("websocket server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)
	return
}

func (server *WebsocketServer) OnOpened(c gnet.Conn) (out []byte, action gnet.Action) {
	server.ConnNum = server.ConnNum + 1
	return
}

func (server *WebsocketServer) OnClosed(c gnet.Conn, err error) (action gnet.Action) {
	server.ConnNum = server.ConnNum - 1
	return
}

func (server *WebsocketServer) React(c gnet.Conn) (out []byte, action gnet.Action) {

	fmt.Printf("react, %v\n", c.Read())

	c.ResetBuffer()

	//在 reactor 协程中做解码操作
	/*
		protocal := NewTCPFixHeadProtocal()
		protocal.SetGnetConnection(c)

		protocalData, err := protocal.serverDecode()
		if err != nil {
			log.Printf("React WorkerPool Decode error :%v\n", err)
		}

		server.WorkerPool.Submit(func() {
			//具体业务在 worker pool中处理
			handlerData := &HandlerDataType{}
			handlerData.ProtocalData = protocalData
			handlerData.Conn = c
			handlerData.server = server
			server.Handler(handlerData)
		})
	*/
	return
}
