# server_on_gnet

English | [简体中文](./README.md)

Ready-to-use Go server implementations built on the [gnet/v2](https://github.com/panjf2000/gnet/v2) event-driven networking framework.
Each protocol lives in its own package and ships with a runnable example plus a test client — useful both for learning how gnet works and as a starting point for new services.

- **Module path**: `github.com/cclehui/server_on_gnet`
- **Go version**: 1.20+
- **License**: MIT

## Highlights

- **Protocol and business logic stay separate**: `OnTraffic` only decodes; work is dispatched to an ants goroutine pool, so the reactor never blocks.
- **Partial and pipelined frames**: parse state lives in the connection `Context`, so a message can be reassembled across multiple callbacks.
- **Pluggable handlers**: every server exposes a handler function type with a default implementation, injected through options.
- **Full lifecycle**: implements `gnet.EventHandle` (`OnBoot` / `OnOpen` / `OnTraffic` / `OnClose` / `OnTick` / `OnShutdown`) with signal-driven graceful shutdown.
- **Verifiable**: each example starts a real server and drives a real client, so both the success and failure paths are observable.

## Supported Protocols

| Protocol | Package | Status | Notes |
|---|---|---|---|
| Fixed-header TCP | [`tcp_fixed_head`](./tcp_fixed_head) | Available | 8-byte fixed header + variable body, streaming decode |
| WebSocket | [`websocket`](./websocket) | Available | Built on `coder/websocket`, with idle-connection eviction |
| gRPC | [`grpc`](./grpc) | Reference | Standalone demo, not integrated with gnet |
| Protobuf | root `proto_temp.go` | Reference | Small marshal / unmarshal demo |

## Quick Start

Requires Go 1.20 or newer. No database, no external service: everything is a process plus a socket.

```bash
git clone https://github.com/cclehui/server_on_gnet.git
cd server_on_gnet

go build ./...   # build every package
```

### Fixed-Header TCP Demo

The server listens on `:7001` and starts 4 built-in clients; each client sends 3 PINGs and verifies the PONGs it receives.

```bash
go run ./examples/tcp_fixed_head --port 7001 --multicore=true
```

> `client-2` deliberately injects 3 junk bytes, so the server closes that connection.
> The `PROTOCOL_VERSION error` and `connection reset` lines in the log are the point of the demo, not a failure.

### WebSocket Demo

The example starts two listeners that share a single `--ip` flag:

- **ws port** (default `8081`): the gnet WebSocket server, handling upgrades and frames only.
- **http port** (default `8080`): a plain `net/http` server that serves the browser test page.

```bash
# Open http://localhost:8080 in a browser
go run ./examples/websocket

# Change the bind address and ports (0.0.0.0 means all interfaces)
go run ./examples/websocket --ip 0.0.0.0 --ws-port 9081 --http-port 9080
```

The page derives the ws URL from the request Host plus the ws port, so nothing needs editing.

### Scripted WebSocket Client

Covers echo round-trips, a frame split across TCP segments, and concurrent connections. It prints `CONNECT/OPEN/SEND/RECV/DONE/CLOSE` per connection and exits non-zero on failure.

```bash
# Start the WebSocket demo above first
go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 8 -msgs 5
```

### Other Demos

```bash
go run ./grpc   # gRPC keepalive reference demo with an in-process client and server, :50051
go run .        # protobuf marshal / unmarshal demo
```

## Usage

### Fixed-Header TCP Server

Wire format (big-endian):

```text
| Version uint16 (0x8001) | ActionType uint16 | DataLength uint32 | Data [DataLength]byte |
```

```go
tcpServer := tcp_fixed_head.NewTCPFixHeadServer(
    // Defaults to tcp://:7001; use "tcp://127.0.0.1:7001" to bind loopback only
    tcp_fixed_head.WithProtoAddr("tcp://:7001"),
    tcp_fixed_head.WithHandler(func(ctx *tcp_fixed_head.HandlerContext) {
        // ctx.ProtocolData holds the decoded frame; ctx.Conn handles downstream writes
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

Available options: `WithProtoAddr`, `WithHandler`, `WithWorkerPool`, `WithProtocol`, `WithGnetOptions`, `WithContext`.
Without `WithHandler`, the built-in PING → PONG echo handler is used.

### WebSocket Server

`gnet` is not an `http.Server`, so a plain HTTP page must be served by a separate server; the ws server only handles upgrades and frames.

```go
server := websocket.NewServer("127.0.0.1:8081",
    websocket.WithHandler(myHandler),
    websocket.WithConnIdleTimeout(600), // seconds, default 600
    websocket.WithAcceptOptions(&coderws.AcceptOptions{
        OriginPatterns: []string{"your.example.com"},
    }),
)

log.Fatal(gnet.Run(server, "tcp://"+server.Addr, gnet.WithMulticore(true)))
```

`NewEchoServer(addr, opts...)` is `NewServer` with the default echo handler prepended, so a caller-supplied `WithHandler` still wins.
Other available options: `WithWorkerPool`, `WithConnCloseHandler`, `WithContext`.

Handlers receive a `DataHandlerParam` containing the message bytes, the message type, the upgraded connection (`WSConn`), and the server instance.
Downstream messages go through `server.SendDownStreamMsg` or `WSConn.WriteMessage`.

A minimal WebSocket server:

```go
server := websocket.NewServer("127.0.0.1:8081",
    websocket.WithHandler(func(param *websocket.DataHandlerParam) {
        _ = param.Server.SendDownStreamMsg(param.WSConn, param.MessageType, param.Request)
    }),
)
```

## Architecture

```text
client ──TCP──► gnet reactor ──decode──► ants worker pool ──► business Handler
                  (OnTraffic)  streaming decode,       (echo / ping-pong /
                               state in Conn Context   user-defined)
                     └── AsyncWrite / Write ◄── response encoded back
```

Three invariants that must not be broken:

1. **The reactor never runs business logic.** `OnTraffic` only decodes; everything else goes to the worker pool. No blocking I/O, no `time.Sleep`.
2. **Borrowed buffers must be copied.** Memory returned by `Next` / `Read` is only valid inside the callback.
3. **`n` in `Next(n)` must be positive.** `n <= 0` means "read the whole buffer" in gnet and would swallow the next pipelined frame.

See [docs/architecture.md](./docs/architecture.md) for the full package map, lifecycle, and known limitations.

## Project Layout

```text
.
├── commonutil/          # logger abstraction and small utilities
├── tcp_fixed_head/      # fixed-header TCP protocol
├── websocket/           # WebSocket server (gnet + coder/websocket)
├── grpc/                # gRPC keepalive reference demo (not gnet-based)
├── examples/            # runnable demos and test clients
├── harness/             # coding style and repo knowledge for contributors / agents
├── docs/                # architecture docs
└── proto_temp.go        # protobuf marshal / unmarshal demo
```

## Documentation

| Document | Contents |
|---|---|
| [AGENTS.md](./AGENTS.md) | Collaboration, commit, and verification rules |
| [harness/coding_style.md](./harness/coding_style.md) | Go style and naming conventions |
| [harness/knowledge/architecture.md](./harness/knowledge/architecture.md) | Reactor + worker-pool model and lifecycle |
| [harness/knowledge/tcp_fixed_head.md](./harness/knowledge/tcp_fixed_head.md) | Wire format and decode pitfalls |
| [harness/knowledge/websocket.md](./harness/knowledge/websocket.md) | WebSocket bridge, per-conn reader, idle eviction |
| [harness/knowledge/logging.md](./harness/knowledge/logging.md) | Log format and `[server]` / `[client-N]` tags |
| [harness/knowledge/testing.md](./harness/knowledge/testing.md) | How to run the examples and judge the result |

## Quality Checks

```bash
gofmt -l .      # must print nothing
go vet ./...    # must pass
go build ./...  # must pass
go test ./...   # currently no test files in any package (known gap)
```

There are no unit tests yet. For protocol changes, verify by running the matching example and reading the `[server]` / `[client-N]` log lines;
see [harness/knowledge/testing.md](./harness/knowledge/testing.md).

## Roadmap

- [ ] MQTT protocol
- [ ] Connection management
- [ ] Protobuf over gnet
- [ ] Thrift protocol
- [ ] Long-connection maintenance platform

## License

[MIT](./LICENSE)
