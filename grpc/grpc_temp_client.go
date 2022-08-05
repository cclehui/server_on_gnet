package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cclehui/server_on_gnet/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	port = ":50051"
)

var kacp = keepalive.ClientParameters{
	Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
	Timeout:             time.Second,      // wait 1 second for ping ack before considering the connection dead
	PermitWithoutStream: true,             // send pings even without active streams
}

func clientTest(count int) {

	//serverAddress := fmt.Sprintf("localhost%v", port)
	serverAddress := fmt.Sprintf("172.16.9.216%v", port)

	/*
		keepaliveParam := keepalive.ClientParameters{}
		keepaliveParam.Time = time.Second * 10
		keepaliveParam.Timeout = time.Second * 2
		keepaliveParam.PermitWithoutStream = true
	*/

	conn, err := grpc.Dial(serverAddress, grpc.WithInsecure(), grpc.WithKeepaliveParams(kacp))
	if err != nil {
		log.Printf("连接服务端失败, %v\n", err)
		return
	}

	log.Printf("连接服务端成功, %v\n", conn.Target())
	defer conn.Close()

	for i := 1; i < 4; i++ {
		go func(num int) {
			name := fmt.Sprintf("cclehui_%d", num)
			for {
				client := protobuf.NewGreeterClient(conn)

				request := &protobuf.HelloRequest{Name: name}

				reply, err2 := client.SayHello(context.Background(), request)

				if err2 != nil {
					log.Printf("client:%d, 调用rpc方法失败, %v\n", i, err2)
					return
				}

				log.Printf("client:%d, 调用rpc方法成功, server reply:%v\n", i, reply.GetMessage())
				time.Sleep(time.Second * 1)
			}
		}(i)
	}
	select {}

	return
}

func main() {

	count := 1

	clientTest(count)

}
