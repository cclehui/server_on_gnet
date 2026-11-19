# server_on_gnet

[English](./README_en.md) | 简体中文

以 [gnet/v2](https://github.com/panjf2000/gnet/v2) 事件驱动网络框架为主的 Go 服务端实现集合。
每个协议一个独立包，开箱即用，并附带可直接运行的示例与测试客户端 —— 既适合学习 gnet 的用法，也适合作为新服务的起点。
gRPC 自带传输层，因此 `grpc` 包直接包装 `google.golang.org/grpc`，不经过 gnet。

- **模块路径**：`github.com/cclehui/server_on_gnet`
- **Go 版本**：1.24+
- **许可证**：MIT

## 特性

- **协议与业务分离**：`OnTraffic` 只做解码，业务处理统一投递到 ants 协程池，reactor 永不被阻塞。
- **支持半包 / 粘包**：解析状态挂在连接的 `Context` 上，一条 TCP 流可以跨多次回调逐步还原完整消息。
- **可替换的处理器**：gnet 协议暴露 `Handler` 函数类型与默认实现；gRPC 通过 `ServiceRegistrar` 注册服务。
- **完整生命周期**：gnet 协议实现 `gnet.EventHandle`，gRPC 包装层监听连接统计；两者都提供信号触发的优雅退出。
- **可验证**：示例自带服务端 + 客户端，跑起来就能观察完整的收发与错误路径。

## `支持的协议`

| 协议 | 包 | 状态 | 说明 |
|---|---|---|---|
| 固定头 TCP | [`tcp_fixed_head`](./tcp_fixed_head) | 可用 | 定长头 + 变长 body，头长度可配置（默认 8 字节），支持流式解码 |
| WebSocket | [`websocket`](./websocket) | 可用 | 基于 `coder/websocket`，含空闲连接回收 |
| gRPC | [`grpc`](./grpc) | 可用 | 包装 `google.golang.org/grpc`，独立监听（不经过 gnet） |
| MQTT | [`mqtt`](./mqtt) | 可用 | MQTT 3.1.1 broker，QoS 0、`+`/`#` 通配、retain、will、keepalive；编解码用 `paho.mqtt.golang/packets` |
| Protobuf | 根目录 `proto_temp.go` | 参考实现 | 序列化 / 反序列化小示例 |

## 快速开始

环境要求：Go 1.24 及以上（`mqtt` 依赖 `eclipse/paho.mqtt.golang` v1.5）。无数据库，起进程 + 连 socket 即可。

```bash
git clone https://github.com/cclehui/server_on_gnet.git
cd server_on_gnet

go build ./...   # 编译全部包
```

### 固定头 TCP 示例

服务端监听 `:7001`，同时拉起 4 个内置客户端，每个客户端发送 3 次 PING 并校验收到的 PONG。

```bash
go run ./examples/tcp_fixed_head --port 7001 --multicore=true
```

> `client-2` 会故意写入 3 个非法字节，服务端因此主动断开该连接。
> 日志里的 `PROTOCOL_VERSION error` 与 `connection reset` 是演示设计，不是故障。

### WebSocket 示例

示例同时启动两个监听，共用一个 `--ip` 参数：

- **ws 端口**（默认 `8081`）：gnet WebSocket 服务，只处理握手与帧。
- **http 端口**（默认 `8080`）：普通 `net/http` 服务，只输出浏览器测试页面。

```bash
# 浏览器打开 http://localhost:8080 即可测试
go run ./examples/websocket

# 更换监听地址与端口（0.0.0.0 表示所有网卡）
go run ./examples/websocket --ip 0.0.0.0 --ws-port 9081 --http-port 9080
```

页面里的 ws 地址按当前请求的 Host + ws 端口动态生成，无需手工修改。

### 脚本化 WebSocket 客户端

验证 echo 往返、跨 TCP 分片的帧与并发连接；默认逐条打印 `CONNECT/OPEN/SEND/RECV/DONE/CLOSE`，失败时以非 0 退出码结束。

```bash
# 需先启动上面的 WebSocket 服务端
go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 8 -msgs 5
```

### MQTT 示例

MQTT 3.1.1 broker，只实现 **QoS 0**（其他 QoS 在协议层明确拒绝，不静默降级）。
示例起一个 broker 和 4 个内置客户端，覆盖通配订阅、retain、自发布回显和 QoS 1 被拒绝：

```bash
# 默认端口 11883（1883 需要 root）
go run ./examples/mqtt --port 11883
```

> `client-c` 最后会故意用 `qos=1` 发布，服务端按设计断开该连接。
> 日志里的 `qos > 0 not supported` 是演示设计，不是故障。

### gRPC 示例

示例把服务端和内置客户端放在同一个进程里：起一个 `Greeter` 服务，按 `--conns` 并发建立连接，
每个客户端调用 `SayHello` `--msgs` 次；结束后等连接回收再 `GracefulStop`，进程自行退出。

```bash
# 默认 :50051, 3 条连接、每条 2 次调用, SayHello 每次延迟 5s
go run ./examples/grpc

# 换地址/并发数, 并把 handler 延迟设为 0 做快速冒烟测试
go run ./examples/grpc --addr 127.0.0.1:9000 --conns 2 --msgs 3 --greet-delay 0s
```

可用参数：`--addr`、`--conns`、`--msgs`、`--greet-delay`。服务端会打印
`[server] 连接数=N, 新连接: ...`、`[server] SayHello ...`，客户端打印 `[client-N] RECV ...`，
最后一条是 `[server] grpc server shutdown`。

### 其他示例

```bash
go run .   # protobuf 序列化小 demo
```

## 使用方式

### 固定头 TCP 服务

协议格式（大端）。头长度默认 8 字节，可用 `WithHeadLength` 调大；前 8 字节布局不变，
多出的字节作为保留字段（解码忽略、编码补零），收发两端需配置成同一长度：

```text
| Version uint16 (0x8001) | ActionType uint16 | DataLength uint32 | [reserved] | Data [DataLength]byte |
```

```go
tcpServer := tcp_fixed_head.NewTCPFixHeadServer(
    // 默认 tcp://:7001；只监听本机可传 "tcp://127.0.0.1:7001"
    tcp_fixed_head.WithProtoAddr("tcp://:7001"),
    tcp_fixed_head.WithHandler(func(ctx *tcp_fixed_head.HandlerContext) {
        // ctx.ProtocolData 是已解码的帧，ctx.Conn 用于下行写
        _ = ctx
    }),
    tcp_fixed_head.WithGnetOptions(
        gnet.WithReusePort(true),
        gnet.WithMulticore(true),
        gnet.WithTCPKeepAlive(90*time.Second),
    ),
)

if err := tcpServer.Run(); err != nil {
    log.Fatal(err)
}
```

可用 option：`WithProtoAddr`、`WithHandler`、`WithWorkerPool`、`WithProtocol`、`WithGnetOptions`、`WithContext`。
协议头长度用 `tcp_fixed_head.WithProtocol(tcp_fixed_head.NewTCPFixHeadProtocol(tcp_fixed_head.WithHeadLength(16)))` 配置；
业务侧回包用 `ctx.Protocol()` 编码，自动使用同一个头长度。
不传 `WithHandler` 时使用内置的 PING → PONG 回声处理器。

### WebSocket 服务

`gnet` 不是 `http.Server`，因此普通 HTTP 页面必须由另一个服务提供；ws 服务只负责协议升级与帧处理。

```go
server := websocket.NewServer("127.0.0.1:8081",
    websocket.WithHandler(myHandler),
    websocket.WithConnIdleTimeout(600), // 秒，默认 600
    websocket.WithAcceptOptions(&coderws.AcceptOptions{
        OriginPatterns: []string{"your.example.com"},
    }),
)

log.Fatal(gnet.Run(server, "tcp://"+server.Addr, gnet.WithMulticore(true)))
```

`NewEchoServer(addr, opts...)` 是带默认 echo 处理器的 `NewServer`，调用方的 `WithHandler` 依然可以覆盖它。
其余可用 option：`WithWorkerPool`、`WithConnCloseHandler`、`WithContext`。

处理函数收到的 `DataHandlerParam` 包含消息字节、消息类型、升级后的连接（`WSConn`）与 server 实例；
下行消息通过 `server.SendDownStreamMsg` 或 `WSConn.WriteMessage` 发送。

一个最小可用的 WebSocket 服务：

```go
server := websocket.NewServer("127.0.0.1:8081",
    websocket.WithHandler(func(param *websocket.DataHandlerParam) {
        _ = param.Server.SendDownStreamMsg(param.WSConn, param.MessageType, param.Request)
    }),
)
```

### gRPC 服务

`grpc` 直接包装 `google.golang.org/grpc`，不经过 gnet：gRPC 自带 HTTP/2 多路复用和
stream 生命周期，用 gnet 重写只会丢掉这些语义。这个包补的是监听、连接计数、日志和
信号驱动停机，服务本身通过 `WithServiceRegistrar` 注册。

```go
import (
    grpcgo "google.golang.org/grpc"
    "google.golang.org/grpc/keepalive"

    "github.com/cclehui/server_on_gnet/grpc"
    "github.com/cclehui/server_on_gnet/grpc/protobuf" // 本仓库的示例 proto
)

server := grpc.NewServer(
    grpc.WithAddr("127.0.0.1:50051"),
    grpc.WithServiceRegistrar(func(s *grpcgo.Server) {
        protobuf.RegisterGreeterServer(s, &helloServer{})
    }),
    grpc.WithKeepaliveParams(keepalive.ServerParameters{
        Time:    5 * time.Second,
        Timeout: time.Second,
    }),
)

if err := server.Run(); err != nil {
    log.Fatal(err)
}
```

可用 option：`WithAddr`、`WithServiceRegistrar`、`WithGrpcOptions`、`WithStatsHandler`、
`WithKeepaliveEnforcementPolicy`、`WithKeepaliveParams`、`WithContext`。
`Run` 阻塞到服务退出，并自动处理 `SIGINT/SIGTERM` 等信号；程序内要主动停机调用
`server.Stop()` 或 `server.GracefulStop()`。`server.GetConnNum()` 读取当前连接数，
`server.LocalAddr()` 返回实际监听地址（`WithAddr(":0")` 时用它取系统分配端口）。

## 架构

```text
client ──TCP──► gnet reactor ──decode──► ants worker pool ──► business Handler
                  (OnTraffic)  流式解码，            (echo / ping-pong /
                               状态存于 Conn Context 用户自定义)
                     └── AsyncWrite / Write ◄── 响应编码后写回
```

三条不可破坏的约束：

1. **reactor 不跑业务逻辑**：`OnTraffic` 只解码，其余工作交给 worker pool，禁止阻塞 I/O 与 `time.Sleep`。
2. **借用的 buffer 必须拷贝**：`Next` / `Read` 返回的内存只在回调内有效。
3. **`Next(n)` 的 `n` 必须大于 0**：`n <= 0` 在 gnet 中表示"读完整个缓冲区"，会吞掉下一条流水线帧。

完整的包地图、生命周期与已知限制见 [docs/architecture.md](./docs/architecture.md)。

gRPC 是上面这张图的例外：它直接包装 `google.golang.org/grpc`，由 gRPC 自己处理连接、
HTTP/2 分帧和多路复用，包装层只负责监听、连接计数、日志和优雅停机。详见
[harness/knowledge/grpc.md](./harness/knowledge/grpc.md)。

## 项目结构

```text
.
├── commonutil/          # 日志抽象与通用工具
├── tcp_fixed_head/      # 固定头 TCP 协议实现
├── websocket/           # WebSocket 服务实现（gnet + coder/websocket）
├── grpc/                # gRPC 服务封装（google.golang.org/grpc，非 gnet）
├── examples/            # 可直接运行的示例与测试客户端
├── harness/             # 编码规范与仓库知识（面向贡献者 / agent）
├── docs/                # 架构文档
└── proto_temp.go        # protobuf 序列化小 demo
```

## 文档

| 文档 | 内容 |
|---|---|
| [AGENTS.md](./AGENTS.md) | 协作约束、提交与验证规则 |
| [harness/coding_style.md](./harness/coding_style.md) | Go 代码风格与命名约定 |
| [harness/knowledge/architecture.md](./harness/knowledge/architecture.md) | reactor + worker pool 模型与生命周期 |
| [harness/knowledge/tcp_fixed_head.md](./harness/knowledge/tcp_fixed_head.md) | 固定头协议的线格式与解码陷阱 |
| [harness/knowledge/websocket.md](./harness/knowledge/websocket.md) | WebSocket 适配层、读协程与空闲回收 |
| [harness/knowledge/logging.md](./harness/knowledge/logging.md) | 日志格式与 `[server]` / `[client-N]` 约定 |
| [harness/knowledge/testing.md](./harness/knowledge/testing.md) | 如何运行示例并判断结果是否正确 |

## 质量检查

```bash
gofmt -l .      # 必须无输出
go vet ./...    # 必须通过
go build ./...  # 必须通过
go test ./...   # 当前各包均无测试文件（已知缺口）
```

仓库暂未配置单元测试。协议相关改动的验证方式是运行对应示例并检查 `[server]` / `[client-N]` 日志，
详见 [harness/knowledge/testing.md](./harness/knowledge/testing.md)。

## Roadmap

- [ ] 长连接维持平台

## License

[MIT](./LICENSE)
