package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"strconv"
	"time"

	"github.com/cclehui/server_on_gnet/grpc/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	port = ":50051"
)

func getGoroutineID() uint64 {
	b := make([]byte, 64)
	runtime.Stack(b, false)
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}

// server is used to implement helloworld.GreeterServer.
type helloServer struct {
}

// SayHello implements helloworld.GreeterServer
func (s *helloServer) SayHello(ctx context.Context, in *protobuf.HelloRequest) (*protobuf.HelloReply, error) {
	groutineId := getGoroutineID()
	log.Printf("groutineId:%d, Received: %v", groutineId, in.GetName())
	time.Sleep(time.Second * 5)
	log.Printf("groutineId:%d, Response: Hello %v", groutineId, in.GetName())
	return &protobuf.HelloReply{Message: "Hello " + in.GetName()}, nil
}

func startGrpcServer() {

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v, port:%v", err, port)
	}

	var kaep = keepalive.EnforcementPolicy{
		MinTime:             5 * time.Second, // If a client pings more than once every 5 seconds, terminate the connection
		PermitWithoutStream: true,            // Allow pings even when there are no active streams
	}

	var kasp = keepalive.ServerParameters{
		MaxConnectionIdle:     15 * time.Second, // If a client is idle for 15 seconds, send a GOAWAY
		MaxConnectionAge:      30 * time.Second, // If any connection is alive for more than 30 seconds, send a GOAWAY
		MaxConnectionAgeGrace: 5 * time.Second,  // Allow 5 seconds for pending RPCs to complete before forcibly closing connections
		Time:                  5 * time.Second,  // Ping the client if it is idle for 5 seconds to ensure the connection is still active
		Timeout:               1 * time.Second,  // Wait 1 second for the ping ack before assuming the connection is dead
	}

	server := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(kaep), grpc.KeepaliveParams(kasp))

	protobuf.RegisterGreeterServer(server, &helloServer{})

	log.Printf("grpc 服务启动, tcp port:%v\n", port)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

}

func main() {
	go func() { // client
		time.Sleep(time.Second * 2)

		clientTest()
	}()

	//启动 grpc server
	startGrpcServer()
}

/*******************/
func clientTest() {
	serverPort := ":50051"

	serverAddress := fmt.Sprintf("localhost%v", serverPort)

	/*
		keepaliveParam := keepalive.ClientParameters{}
		keepaliveParam.Time = time.Second * 10
		keepaliveParam.Timeout = time.Second * 2
		keepaliveParam.PermitWithoutStream = true
	*/
	var kacp = keepalive.ClientParameters{
		Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
		Timeout:             time.Second,      // wait 1 second for ping ack before considering the connection dead
		PermitWithoutStream: true,             // send pings even without active streams
	}

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
					log.Printf("client:%d, 调用rpc方法失败, %v\n", num, err2)
					return
				}

				log.Printf("client:%d, 调用rpc方法成功, server reply:%v\n", num, reply.GetMessage())
				time.Sleep(time.Second * 1)
			}
		}(i)
	}
	select {}
}
