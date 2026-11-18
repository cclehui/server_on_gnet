package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/grpc"
	"github.com/cclehui/server_on_gnet/grpc/protobuf"
)

// gRPC 示例: 演示 grpc.Server 的连接计数、keepalive 配置和优雅停机.
// 服务端与内置客户端在同一个进程里, 跑完 msgs 轮后客户端退出并调用 server.Stop.
//
//	go run ./examples/grpc
//	go run ./examples/grpc --addr 127.0.0.1:9000 --conns 2 --msgs 3
func main() {
	addr := flag.String("addr", grpc.DefaultAddr, "grpc listen address")
	conns := flag.Int("conns", 3, "concurrent clients")
	msgs := flag.Int("msgs", 2, "SayHello calls per client")
	greetDelay := flag.Duration("greet-delay", 5*time.Second, "server-side delay in SayHello")
	flag.Parse()

	ctx := context.Background()

	server := grpc.NewServer(
		grpc.WithAddr(*addr),
		grpc.WithContext(ctx),
		grpc.WithServiceRegistrar(func(s *grpcgo.Server) {
			protobuf.RegisterGreeterServer(s, &helloServer{delay: *greetDelay})
		}),
		grpc.WithKeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithKeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Second,
			MaxConnectionAge:      30 * time.Second,
			MaxConnectionAgeGrace: 5 * time.Second,
			Time:                  5 * time.Second,
			Timeout:               1 * time.Second,
		}),
	)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run()
	}()

	if err := waitForListener(ctx, server, serverErr); err != nil {
		commonutil.GetLogger().Errorf(ctx, "[server] 启动失败: %+v", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	for i := 0; i < *conns; i++ {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()
			runClient(ctx, server.LocalAddr().String(), idx, *msgs)
		}(i)
	}
	wg.Wait()

	waitForNoConns(ctx, server)
	commonutil.GetLogger().Infof(ctx, "[server] 客户端已全部结束, 当前连接数=%d", server.GetConnNum())
	if err := server.GracefulStop(); err != nil {
		commonutil.GetLogger().Errorf(ctx, "[server] GracefulStop error:%+v", err)
		os.Exit(1)
	}

	if err := <-serverErr; err != nil {
		commonutil.GetLogger().Errorf(ctx, "[server] Run error:%+v", err)
		os.Exit(1)
	}
}

func waitForListener(ctx context.Context, server *grpc.Server, serverErr <-chan error) error {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-serverErr:
			return err
		default:
		}

		if addr := server.LocalAddr(); addr != nil {
			commonutil.GetLogger().Infof(ctx, "[server] 实际监听地址:%s", addr)
			return nil
		}

		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("等待监听地址超时")
}

// 客户端 Close() 返回不代表 server 侧 ConnEnd 已经跑完, 等计数归零再停服,
// 这样示例最后打印的连接数和真实状态一致.
func waitForNoConns(ctx context.Context, server *grpc.Server) {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if server.GetConnNum() == 0 {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	commonutil.GetLogger().Warnf(ctx, "[server] 等待连接回收超时, 当前连接数=%d", server.GetConnNum())
}

func runClient(ctx context.Context, addr string, idx, msgs int) {
	tag := fmt.Sprintf("[client-%d]", idx)
	clientCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var kacp = keepalive.ClientParameters{
		Time:                10 * time.Second,
		Timeout:             time.Second,
		PermitWithoutStream: true,
	}

	conn, err := grpcgo.DialContext(clientCtx, addr, grpcgo.WithInsecure(), grpcgo.WithKeepaliveParams(kacp), grpcgo.WithBlock())
	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "%s 连接服务端失败: %+v", tag, err)
		return
	}
	defer conn.Close()

	client := protobuf.NewGreeterClient(conn)
	for i := 0; i < msgs; i++ {
		name := fmt.Sprintf("client-%d-msg-%d", idx, i)
		reply, err := client.SayHello(clientCtx, &protobuf.HelloRequest{Name: name})
		if err != nil {
			commonutil.GetLogger().Errorf(ctx, "%s 第 %d 次调用失败: %+v", tag, i+1, err)
			return
		}

		commonutil.GetLogger().Infof(ctx, "%s RECV %q", tag, reply.GetMessage())
	}
}

type helloServer struct {
	delay time.Duration
}

func (s *helloServer) SayHello(ctx context.Context, in *protobuf.HelloRequest) (*protobuf.HelloReply, error) {
	goroutineID := getGoroutineID()
	commonutil.GetLogger().Infof(ctx, "[server] SayHello goroutineId=%d, name=%q", goroutineID, in.GetName())

	if s.delay > 0 {
		time.Sleep(s.delay)
	}

	return &protobuf.HelloReply{Message: "Hello " + in.GetName()}, nil
}

func getGoroutineID() uint64 {
	b := make([]byte, 64)
	runtime.Stack(b, false)
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]

	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}
