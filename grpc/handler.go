package grpc

import grpcgo "google.golang.org/grpc"

// ServiceRegistrar 把一个或多个 gRPC 服务注册到底层 server 上.
// generated 代码里的 protobuf.RegisterXxxServer 正好匹配这个签名:
//
//	grpc.WithServiceRegistrar(func(server *grpcgo.Server) {
//		protobuf.RegisterGreeterServer(server, &helloServer{})
//	})
//
// grpc 是 service/method 模型, 不像 tcp_fixed_head/websocket 那样有一条消息对应
// 一个业务 handler, 所以没有 ServerHandler; 注册函数就是扩展点.
type ServiceRegistrar func(server *grpcgo.Server)
