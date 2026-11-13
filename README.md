# server_on_gnet
基于gnet网络框架编写的各种常见服务端server程序，可以用来学习和快速使用

目前支持的协议类型

## 固定协议头大小，消息体不定长协议支持
./examples/tcp_fixed_head/tcp_fixed_head_server.go 中是具体的使用demo, 还包含了具体的测试client, 协议是完整的, 可以直接run起来试试
```
port := 8081
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
