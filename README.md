# server_on_gnet
基于gnet网络框架编写的各种常见服务端server程序，可以用来学习和快速使用

目前支持的协议类型

## 固定协议头大小，消息体不定长协议支持
tcp_fixed_head_server.go 中是具体的使用demo, 还包含了具体的测试client, 协议是完整的, 可以直接run起来试试
```
port := 8081
tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)
log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

```

## websocket server
websocket_server.go 中是具体的使用demo

websocket 协议是基于 https://github.com/gobwas/ws 这个库解析的， 但是这个库目前不支持go mod, 各位看官可以把这个库自己做个go mod 或者down到gopath下使用

gnet 如何 使用websocket库的? 核心代码是把 gnet.Conn封装一个 io.ReaderWriter接口出来， 具体代码在websocket/upgrader.go中实现

```
port := 8081
tcpServer := websocket.NewEchoServer(port)
log.Fatal(gnet.Serve(tcpServer, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore)))

```
