package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cclehui/server_on_gnet/websocket"
	"github.com/panjf2000/gnet/v2"
)

func wsHome(w http.ResponseWriter, r *http.Request) {
	//websocket.ClientTemplate.Execute(w, "ws://"+r.Host+"/echo")
	websocket.ClientTemplate.Execute(w, "ws://172.16.9.216:8081")
}

func main() {

	go func() {

		//处理 websocket 协议的tcp服务监听在 8081端口上
		port := 8081

		tcpServer := websocket.NewEchoServer(port)

		go func() {
			for {
				fmt.Println("当前连接数量:", tcpServer.ConnNum)

				time.Sleep(time.Second * 2)
			}

		}()
		log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(true)))

	}()

	//var addr = flag.String("addr", "localhost:8080", "http service address")
	addr := "0.0.0.0:8080"

	log.Printf("http server for websocket client is listen at :%s\n", addr)

	http.HandleFunc("/", wsHome)
	log.Fatal(http.ListenAndServe(addr, nil))

}
