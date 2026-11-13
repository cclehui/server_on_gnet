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
// 端口用 ws 服务的端口(页面和 ws 是两个端口, 不能直接用 r.Host 的端口).
func wsHome(wsPort int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		websocket.ClientTemplate.Execute(w, fmt.Sprintf("ws://%s", net.JoinHostPort(host, strconv.Itoa(wsPort))))
	}
}

// WebSocket 示例: 同时起两个监听, 所以有两个端口.
//
//  1. ws-port (默认 8081): gnet 的 websocket server, 只处理 websocket 握手和帧.
//     它不是 http.Server, 普通浏览器 GET / 会拿到 426 Upgrade Required.
//  2. http-port (默认 8080): 普通 net/http server, 只负责吐出测试页面
//     (websocket.ClientTemplate). 浏览器打开 http://localhost:8080 就能测.
//
// 页面里的 ws 地址由 wsHome 生成: host 取自页面请求, 端口用 ws-port,
// 因为页面所在的 8080 并不是 websocket 服务监听的端口.
//
// go run ./examples/websocket
// go run ./examples/websocket --ws-port 9081 --http-port 9080
func main() {
	ctx := context.Background()

	wsPort := flag.Int("ws-port", 8081, "websocket server port (gnet, websocket protocol only)")
	httpPort := flag.Int("http-port", 8080, "browser client page port (plain http, serves the demo page)")
	flag.Parse()

	fmt.Printf("websocket demo: ws 监听 :%d, 测试页面 http://localhost:%d/\n", *wsPort, *httpPort)
	fmt.Printf("也可以不用页面, 直接跑脚本客户端: go run ./examples/websocket_client -addr ws://127.0.0.1:%d\n", *wsPort)

	go func() {
		//处理 websocket 协议的tcp服务监听在 ws-port 端口上
		addr := fmt.Sprintf(":%d", *wsPort)

		tcpServer := websocket.NewEchoServer(addr)

		// 连接数走统一的 logger(JSON 行 + [server] 前缀), 不要用 fmt.Println,
		// 否则它会和并发打印的日志行在终端里交错
		go func() {
			for {
				commonutil.GetLogger().Infof(ctx, "[server] 当前连接数=%d", tcpServer.GetConnNum())

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

	commonutil.GetLogger().Infof(ctx, "测试页面已就绪: http://localhost:%d/ (页面里的 ws 地址连 :%d)", *httpPort, *wsPort)

	http.HandleFunc("/", wsHome(*wsPort))
	log.Fatal(http.ListenAndServe(addr, nil))
}
