package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/cclehui/server_on_gnet/protobuf"
	"google.golang.org/grpc"
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

func clientTest(count int) {

	serverAddress := fmt.Sprintf("localhost%v", port)

	conn, err := grpc.Dial(serverAddress, grpc.WithInsecure())
	if err != nil {
		log.Printf("连接服务端失败, %v\n", err)
		return
	}

	log.Printf("连接服务端成功, %v\n", conn.Target())
	defer conn.Close()

	name := fmt.Sprintf("cclehui_%d", count)

	client := protobuf.NewGreeterClient(conn)

	request := &protobuf.HelloRequest{Name: name}

	reply, err2 := client.SayHello(context.Background(), request)

	if err2 != nil {
		log.Printf("调用rpc方法失败, %v\n", err2)
		return
	}

	log.Printf("调用rpc方法成功, server reply:%v\n", reply.GetMessage())

	return
}

func main() {

	//启动 grpc server
	go func() {
		startGrpcServer()
	}()

	//client test
	count := 1

	for {
		time.Sleep(time.Second * 1)

		clientTest(count)

		count++
	}

}
