package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/websocket"
	"github.com/panjf2000/gnet/v2"
)

// wsHome 按页面的访问地址推导 ws 地址: 主机名取自请求 Host,
// 端口用 ws 服务的端口(页面和 ws 是两个端口, 不能直接用 r.Host 的端口)
func wsHome(wsPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		websocket.ClientTemplate.Execute(w, fmt.Sprintf("ws://%s", net.JoinHostPort(host, strconv.Itoa(wsPort))))
	}
}

func main() {
	ctx := context.Background()

	wsPort := flag.Int("ws-port", 8081, "websocket server port")
	httpPort := flag.Int("http-port", 8080, "browser client page port")
	flag.Parse()

	go func() {
		//处理 websocket 协议的tcp服务监听在 ws-port 端口上
		addr := fmt.Sprintf(":%d", *wsPort)

		tcpServer := websocket.NewEchoServer(addr)

		go func() {
			for {
				fmt.Println("当前连接数量:", tcpServer.GetConnNum())

				time.Sleep(time.Second * 2)
			}

		}()

		options := []gnet.Option{
			gnet.WithReusePort(true),
			gnet.WithMulticore(true),
		}
		log.Fatal(gnet.Run(tcpServer, fmt.Sprintf("tcp://:%d", *wsPort), options...))

	}()

	addr := fmt.Sprintf("0.0.0.0:%d", *httpPort)

	commonutil.GetLogger().Infof(ctx, "http server for websocket client is listen at :%s", addr)

	http.HandleFunc("/", wsHome(*wsPort))
	log.Fatal(http.ListenAndServe(addr, nil))
}
