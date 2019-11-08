package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cclehui/server_on_gnet/tcp_fixed_head"
	"github.com/panjf2000/gnet"
)

//网络编程
//tcp server 简单的 固定头部长度协议

func main() {

	var port int
	var multicore bool

	// Example command: go run main.go --port 2333 --multicore true
	flag.IntVar(&port, "port", 5000, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)

	go func() {
		for {
			fmt.Println("当前连接数量:", tcpServer.ConnNum)

			time.Sleep(time.Second * 1)
		}

	}()

	fmt.Printf("tttttt, %x\n", 825307185)

	go func() {
		for i := 0; i < 50000; i++ {
			go func() {
				tcpFHTestClient(port)
			}()
		}
	}()

	log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

}

func tcpFHTestClient(port int) {

	time.Sleep(time.Second * 3)

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	//_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))

	if err != nil {
		log.Printf("tcpFHTestClient, Dail error:%v\n", err)
	}

	for i := 1; i <= 10; i++ {
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

		protocal := tcp_fixed_head.NewTCPFixHeadProtocal()

		protocal.EncodeWrite(tcp_fixed_head.ACTION_PING, []byte(data), conn)

		time.Sleep(time.Second * 1)
	}

	time.Sleep(time.Second * 86400)

}
