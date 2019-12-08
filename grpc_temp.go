package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/cclehui/server_on_gnet/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	port = ":50051"
)

// server is used to implement helloworld.GreeterServer.
type helloServer struct {
}

// SayHello implements helloworld.GreeterServer
func (s *helloServer) SayHello(ctx context.Context, in *protobuf.HelloRequest) (*protobuf.HelloReply, error) {
	log.Printf("Received: %v", in.GetName())
	return &protobuf.HelloReply{Message: "Hello " + in.GetName()}, nil
}

func startGrpcServer() {

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v, port:%v", err, port)
	}
	server := grpc.NewServer()

	protobuf.RegisterGreeterServer(server, &helloServer{})

	log.Printf("grpc 服务启动, tcp port:%v\n", port)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

}

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

	//启动 grpc server
	go func() {
		//startGrpcServer()
	}()

	//client test
	count := 1

	for {

		clientTest(count)

		time.Sleep(time.Second * 1000)
		time.Sleep(time.Second * 1)

		count++
	}

}
