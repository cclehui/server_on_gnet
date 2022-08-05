package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/tcp_fixed_head"
	"github.com/panjf2000/gnet"
)

// 网络编程
// tcp server 简单的 固定头部长度协议
func main() {

	var port int
	var multicore bool

	// Example command: go run main.go --port 2333 --multicore true
	flag.IntVar(&port, "port", 5000, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)
	ctx := context.Background()

	go func() {
		for {
			commonutil.GetLogger().Infof(ctx, "当前连接数量:%d", tcpServer.ConnNum)

			time.Sleep(time.Second * 1)
		}
	}()

	go func() {
		for i := 0; i < 1; i++ {
			go func() {
				tcpFHTestClient(ctx, port)
			}()
		}
	}()

	options := []gnet.Option{gnet.WithReusePort(true), gnet.WithMulticore(multicore)}

	err := tcp_fixed_head.Run(tcpServer, fmt.Sprintf("tcp://:%d", port), options...)
	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "启动失败:%+v", err)
	}
}

//测试 client
func tcpFHTestClient(ctx context.Context, port int) {
	time.Sleep(time.Second * 3)

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	//_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))

	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "tcpFHTestClient, Dail error:%v", err)
	}

	protocal := tcp_fixed_head.NewTCPFixHeadProtocal()

	for i := 1; i <= 3; i++ {
		//for i := 1; i <= 2; i++ {
		data := strings.Repeat(strconv.Itoa(i), i)
		data = data + "abc"

		/*
			if i == 2 {
				badData := []byte("xxx")
				err2 := binary.Write(conn, binary.BigEndian, badData)
				fmt.Println("发送干扰数据, ", err2)
			}
		*/

		//fmt.Println("发送数据\t", data)

		//直接encode 并发送
		//protocal.EncodeWrite(tcp_fixed_head.ACTION_PING, []byte(data), conn)

		//返回encode 的数据， 然后发送
		encodedData, _ := protocal.EncodeData(tcp_fixed_head.ACTION_PING, []byte(data))
		commonutil.GetLogger().Infof(ctx, "client 发送数据,%s", data)

		conn.Write(encodedData)

		time.Sleep(time.Second * 1)
	}

	commonutil.GetLogger().Infof(ctx, "开始获取服务端返回的数据......")

	for {
		response, err := protocal.ClientDecode(conn)

		if err != nil {
			commonutil.GetLogger().Errorf(ctx, "获取服务端数据异常, %v", err)
		}

		commonutil.GetLogger().Infof(ctx, "服务端返回的数据, %v, data:%s", response, string(response.Data))
	}
}
