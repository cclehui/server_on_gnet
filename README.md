# server_on_gnet
基于gnet网络框架编写的各种常见服务端server程序，可以用来学习和快速使用

目前支持的协议类型

## 快速开始（运行示例）

每个协议都是一个可以直接 `go run` 的 demo, 服务端和测试客户端都在里面:

```bash
# 固定头 TCP: 服务端监听 :7001, 同时跑 4 个内置客户端
# (client-2 会被服务端断开, 这是示例本身, 不是失败)
go run ./examples/tcp_fixed_head --port 7001 --multicore=true

# WebSocket: ws 服务端 :8081 + 浏览器测试页面 :8080(打开 http://localhost:8080)
go run ./examples/websocket
# 也可以换端口
go run ./examples/websocket --ws-port 9081 --http-port 9080

# 脚本化 WebSocket 客户端: echo 往返、跨 TCP 分片的帧、并发连接,
# 每条连接都会打印 CONNECT/OPEN/SEND/RECV/DONE/CLOSE, 失败时以非 0 退出码结束
# (需要先启动上面的 ws 服务端; -v=false 只看汇总)
go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 8 -msgs 5

# grpc keepalive 参考 demo, 自带 client + server, 监听 :50051
go run ./grpc

# 仓库根目录的 protobuf 序列化小 demo
go run .
```

## 固定协议头大小，消息体不定长协议支持
./examples/tcp_fixed_head/tcp_fixed_head_server.go 中是具体的使用demo, 还包含了具体的测试client, 协议是完整的, 可以直接run起来试试
```
port := 7001
tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)
options := []gnet.Option{
	gnet.WithReusePort(true),
	gnet.WithMulticore(multicore),
	gnet.WithTCPKeepAlive(time.Second * 90),
}

err := tcp_fixed_head.Run(tcpServer, fmt.Sprintf("tcp://:%d", port), options...)

```

## websocket server
./examples/websocket/websocket_server.go 中是具体的使用demo, 页面里的 ws 地址按当前请求的 Host 生成,
默认连 `ws://localhost:8081`; ./examples/websocket_client 是一个脚本化客户端, 用来验证 echo、TCP 分片和并发连接

示例同时起两个监听, 所以有两个端口: ws 服务(gnet, 只处理 websocket, 默认 8081)和
普通 http 页面服务(只吐测试页面, 默认 8080). gnet 不是 http.Server, 页面必须由另一个
http 服务提供; 浏览器打开 http://localhost:8080 即可, 页面会自己去连 8081

websocket 协议是基于 https://github.com/coder/websocket 这个库解析的，

gnet 如何 使用websocket库的? 这个库是阻塞式 io(net.Conn 形态), 而 gnet 的 Conn.Read 是非阻塞的,
所以协议解析不能放在 reactor 里: reactor(OnTraffic) 只把字节拷贝给每连接的读协程, 读协程里完成握手和解帧,
下行写统一走 AsyncWrite(在事件循环执行). 适配代码在 ./websocket/conn.go 中

```
port := 8081
addr := fmt.Sprintf(":%d", port)
// NewServer/NewEchoServer 都接受 option: WithHandler / WithWorkerPool /
// WithAcceptOptions / WithConnCloseHandler / WithContext / WithConnIdleTimeout
tcpServer := websocket.NewEchoServer(addr,
	websocket.WithConnIdleTimeout(600),
	websocket.WithAcceptOptions(&coderws.AcceptOptions{
		OriginPatterns: []string{"your.example.com"},
	}),
)
options := []gnet.Option{
	gnet.WithReusePort(true),
		gnet.WithMulticore(true),
}
log.Fatal(gnet.Run(tcpServer, fmt.Sprintf("tcp://:%d", port), options...))

```

## 文档

- 协作与验证约束：[AGENTS.md](./AGENTS.md)
- 编码规范：[harness/coding_style.md](./harness/coding_style.md)
- 架构与模型：[harness/knowledge/architecture.md](./harness/knowledge/architecture.md)、[docs/architecture.md](./docs/architecture.md)
- 协议细节：[harness/knowledge/tcp_fixed_head.md](./harness/knowledge/tcp_fixed_head.md)、[harness/knowledge/websocket.md](./harness/knowledge/websocket.md)
- 日志约定：[harness/knowledge/logging.md](./harness/knowledge/logging.md)
- 验证方式：[harness/knowledge/testing.md](./harness/knowledge/testing.md)

## todo
1. mqtt协议
2. 连接管理
3. pb协议
4. thrift协议
5. 长连接维持平台
