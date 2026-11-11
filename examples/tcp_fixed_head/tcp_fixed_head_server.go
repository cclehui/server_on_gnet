package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/tcp_fixed_head"
	"github.com/panjf2000/gnet/v2"
)

// 网络编程
// tcp server 简单的 固定头部长度协议
// go run examples/tcp_fixed_head/tcp_fixed_head_server.go
func main() {

	var port int
	var multicore bool

	// Example command: go run tcp_fixed_head_server.go --port 2333 --multicore true
	flag.IntVar(&port, "port", 7001, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)
	ctx := context.Background()

	go func() {
		for {
			commonutil.GetLogger().Infof(ctx, "当前连接数量:%d", tcpServer.GetConnNum())

			time.Sleep(time.Second * 1)
		}
	}()

	go func() {
		for i := 0; i < 4; i++ {
			go func() {
				tcpFHTestClient(ctx, port)
			}()
		}
	}()

	options := []gnet.Option{
		gnet.WithReusePort(true),
		gnet.WithMulticore(multicore),
		gnet.WithTCPKeepAlive(time.Second * 90),
	}

	err := tcp_fixed_head.Run(tcpServer, fmt.Sprintf("tcp://:%d", port), options...)
	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "启动失败:%+v", err)
	}
}

// 测试 client
func tcpFHTestClient(ctx context.Context, port int) {
	time.Sleep(time.Second * 3)

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	//_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))

	if err != nil {
		// 之前只打日志不 return, conn 为 nil, 后面 conn.Write 会 panic
		commonutil.GetLogger().Errorf(ctx, "tcpFHTestClient, Dail error:%v", err)
		return
	}

	defer conn.Close()

	protocol := tcp_fixed_head.NewTCPFixHeadProtocol()

	for i := 1; i <= 3; i++ {
		//for i := 1; i <= 2; i++ {
		data := strings.Repeat(strconv.Itoa(i), i)
		data = data + "abc"

		if i == 2 {
			badData := []byte("xxx")
			err2 := binary.Write(conn, binary.BigEndian, badData)
			fmt.Println("发送干扰数据, ", err2)
		}

		//fmt.Println("发送数据\t", data)

		//直接encode 并发送
		//protocal.EncodeWrite(tcp_fixed_head.ACTION_PING, []byte(data), conn)

		//返回encode 的数据， 然后发送
		encodedData, err := protocol.EncodeData(tcp_fixed_head.ACTION_PING, []byte(data))
		if err != nil {
			commonutil.GetLogger().Errorf(ctx, "client encode error:%v", err)
			return
		}

		commonutil.GetLogger().Infof(ctx, "client 发送数据,%s", data)

		if _, err := conn.Write(encodedData); err != nil {
			commonutil.GetLogger().Errorf(ctx, "client write error:%v", err)
			return
		}

		time.Sleep(time.Second * 1)
	}

	commonutil.GetLogger().Infof(ctx, "开始获取服务端返回的数据......")

	for {
		time.Sleep(time.Second * 1)
		response, err := protocol.ClientDecode(conn)

		if err != nil {
			// EOF / 连接被服务端关闭(比如协议错误)后连接已不可用, 退出读取循环
			commonutil.GetLogger().Errorf(ctx, "获取服务端数据异常, %v", err)
			return
		}

		commonutil.GetLogger().Infof(ctx, "服务端返回的数据, %v, data:%s", response, string(response.Data))

	}
}
