package grpc

import (
	"context"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/stats"
)

// ServerOption 在 NewServer 构造阶段修改 server 配置.
// 这些字段只在 Run 之前写, 启动后按只读使用.
type ServerOption func(server *Server)

// WithAddr 设置监听地址, 形如 "127.0.0.1:50051" / ":50051".
// 不传时 NewServer 使用 DefaultAddr.
func WithAddr(addr string) ServerOption {
	if addr == "" {
		panic("grpc: WithAddr got an empty address")
	}

	return func(server *Server) {
		server.Addr = addr
	}
}

// WithServiceRegistrar 追加一个服务注册函数. 可以传多次, 按传入顺序执行.
func WithServiceRegistrar(registrar ServiceRegistrar) ServerOption {
	if registrar == nil {
		panic("grpc: WithServiceRegistrar got nil")
	}

	return func(server *Server) {
		server.registrars = append(server.registrars, registrar)
	}
}

// WithGrpcOptions 追加 google.golang.org/grpc 的 ServerOption, 用于 TLS、
// interceptor 等本包没有单独封装的配置.
//
// 连接数统计依赖 StatsHandler; 需要自定义 stats handler 时用 WithStatsHandler,
// 不要在这里再传 grpc.StatsHandler, 否则会覆盖掉连接计数.
func WithGrpcOptions(opts ...grpcgo.ServerOption) ServerOption {
	for _, opt := range opts {
		if opt == nil {
			panic("grpc: WithGrpcOptions got a nil option")
		}
	}

	return func(server *Server) {
		server.grpcOptions = append(server.grpcOptions, opts...)
	}
}

// WithStatsHandler 设置自定义 stats handler. 本包会在它外面包一层连接计数,
// 两边的回调都会被调用.
func WithStatsHandler(handler stats.Handler) ServerOption {
	if handler == nil {
		panic("grpc: WithStatsHandler got nil")
	}

	return func(server *Server) {
		server.StatsHandler = handler
	}
}

// WithKeepaliveEnforcementPolicy 设置服务端对客户端 ping 的接受策略.
func WithKeepaliveEnforcementPolicy(policy keepalive.EnforcementPolicy) ServerOption {
	return func(server *Server) {
		server.grpcOptions = append(server.grpcOptions, grpcgo.KeepaliveEnforcementPolicy(policy))
	}
}

// WithKeepaliveParams 设置服务端 keepalive 参数.
func WithKeepaliveParams(params keepalive.ServerParameters) ServerOption {
	return func(server *Server) {
		server.grpcOptions = append(server.grpcOptions, grpcgo.KeepaliveParams(params))
	}
}

// WithContext 设置 server 生命周期日志使用的 context. 不传时 NewServer 用 context.Background().
func WithContext(ctx context.Context) ServerOption {
	if ctx == nil {
		panic("grpc: WithContext got nil context")
	}

	return func(server *Server) {
		server.ctx = ctx
	}
}
