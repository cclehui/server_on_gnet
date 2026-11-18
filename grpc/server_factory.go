package grpc

import "context"

// NewServer 构造 server. 先应用 opts, 再给没被 option 设置过的字段填默认值:
// option 里给了什么就用什么, 不会先建一个默认值再被覆盖掉(order: opts → defaults).
// 后传的 option 覆盖先传的.
//
// 构造只做 wiring, 不监听端口; 在 Run 之前用 WithServiceRegistrar 注册服务:
//
//	server := grpc.NewServer(
//		grpc.WithAddr("127.0.0.1:50051"),
//		grpc.WithServiceRegistrar(func(s *grpcgo.Server) {
//			protobuf.RegisterGreeterServer(s, &helloServer{})
//		}),
//	)
//	server.Run()
func NewServer(opts ...ServerOption) *Server {
	server := &Server{
		doneCh: make(chan struct{}),
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(server)
	}

	if server.Addr == "" {
		server.Addr = DefaultAddr
	}

	if server.ctx == nil {
		server.ctx = context.Background()
	}

	return server
}
