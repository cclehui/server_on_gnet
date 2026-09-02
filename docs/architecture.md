# server_on_gnet Architecture

A Go library of ready-to-use server implementations built on top of the
[gnet](https://github.com/panjf2000/gnet/v2) event-driven networking framework.
Module path: `github.com/cclehui/server_on_gnet`, Go 1.19.

## High-Level Design

All gnet-based servers follow the same core pattern:

1. **Reactor goroutine (gnet event loop)** — `OnTraffic` performs protocol
   decoding only. Per-connection parse state is kept via `gnet.Conn` context
   (`c.SetContext` / `c.Context()`), enabling streaming (half-packet) decoding.
2. **Worker pool (ants)** — decoded messages are submitted to an
   [`ants`](https://github.com/panjf2000/ants/v2) goroutine pool
   (`DefaultAntsPoolSize = 1024*1024`, nonblocking, 10s expiry) so business
   handlers never block the reactor.
3. **Pluggable handler functions** — servers expose a handler function type
   (`ServerHandler` / `DataHandler`) with a default echo implementation, so
   users inject their own business logic.
4. **Lifecycle callbacks** — each server implements the `gnet.EventHandle`
   interface: `OnBoot` / `OnShutdown` / `OnOpen` / `OnClose` / `OnTraffic` /
   `OnTick`, and tracks live connections with an atomic counter (`ConnNum`).

```
 client ──TCP──► gnet reactor ──decode──► ants worker pool ──► business Handler
                    │  (OnTraffic)   (streaming, per-conn      (echo / ping-pong /
                    │                  state in Conn context)   user-defined)
                    └── AsyncWrite / Write ◄── response encoded back on the frame
```

## Package Layout

| Package | Purpose |
|---|---|
| `commonutil/` | Shared logger abstraction and small utilities |
| `tcp_fixed_head/` | Fixed-header / variable-body TCP protocol server |
| `websocket/` | WebSocket server (gnet + gobwas/ws) with idle-conn management |
| `grpc/` | Standalone gRPC + protobuf demo (not gnet-based, reference code) |
| `examples/` | Runnable demos with test clients for both server types |
| root (`proto_temp.go`) | Protobuf marshal/unmarshal scratch demo |

### commonutil

- `log.go`: `Logger` interface (`Debugf/Infof/Warnf/Errorf` taking a
  `context.Context`) with a JSON-line `DefaultLogger`; swap via
  `SetLogger` / `GetLogger` (log prefix `SERVER_ON_GNET`).
- `codec_empty.go`: `CodecEmpty` — no-op `gnet` codec helper.
- `gnet_option.go`: currently empty, placeholder for shared gnet options.

### tcp_fixed_head — fixed-head-length TCP protocol

Wire format, 8-byte big-endian header + variable body:

```
| Version uint16 (0x8001) | ActionType uint16 | DataLength uint32 | Data [DataLength]byte |
```

- `config.go`: constants — `DefaultHeadLength=8`, `PROTOCOL_VERSION=0x8001`,
  actions `ACTION_PING=0x0001` / `ACTION_PONG=0x0002` / `ACTION_DATA=0x00F0`;
  sentinel errors `ErrIncompletePacket` (keep buffering), `ErrProtocolVersion`,
  `ErrContext`.
- `protocal.go`: `TCPFixHeadProtocol` —
  - `Decode(gnet.Conn)`: server-side streaming decode; partial frames return
    `ErrIncompletePacket` → handler returns `gnet.None` to wait for more data;
    invalid header → `gnet.Close`.
  - `EncodeData` / `EncodeWrite`: frame encoding; `ClientDecode(net.Conn)`:
    blocking client-side decode using `io.ReadFull`.
- `server.go`: `TCPFixHeadServer` (implements `gnet.EventHandle`) + `Run()`
  which also installs an OS-signal goroutine (SIGHUP/QUIT/TERM/INT →
  `gnet.Stop`) for graceful shutdown.
- `handler_context.go`: `HandlerContext` carries `ProtocolData`, `gnet.Conn`,
  and the server, and implements `context.Context` so handlers can log with it.
- `handler.go`: `defaultHandler` answers `ACTION_PING` with an `ACTION_PONG`
  frame written back via `Conn.AsyncWrite`.

### websocket — WebSocket server over gnet

Built on [gobwas/ws](https://github.com/gobwas/ws). Key trick: adapt
`gnet.Conn` to `io.Reader`/`io.Writer` so standard ws parsing works inside a
gnet event loop (gnet v2 conn already satisfies Read/Write).

- `upgrader.go`: `GnetUpgraderConn` wraps `gnet.Conn` + `ws.Upgrader`,
  records `LastActiveTs` and `IsSuccessUpgraded`; `defaultUpgrader` hooks
  (`OnHost/OnHeader/OnRequest/OnBeforeUpgrade`) validate/observe the HTTP
  handshake.
- `server.go`: `WebSocketServer.OnTraffic` state machine:
  1. first bytes → store `GnetUpgraderConn` in conn context and run the ws
     handshake upgrade;
  2. after upgrade → `wsutil.ReadClientMessage`: `OpPing` → `OpPong` reply;
     `OpText` → dispatch to `DataHandler` on the ants pool; `OpClose` → send
     close frame and `gnet.Close`.
  `SendDownStreamMsg` pushes server-initiated frames; `ConnCloseHandler`
  hook for close callbacks.
- `timewheel.go`: single-level hashing time wheel (idea from
  `ouqiang/timewheel`) — 1s interval, one slot per second over the max idle
  window; used to evict connections idle longer than `ConnMaxIdleSeconds`
  (600s, see `config.go`). Active connections re-arm their timer on each
  read/write.
- `data_handler.go`: `DataHandlerParam` (request bytes, opcode, writer,
  upgraded conn, server) and `EchoDataHandler` default echo.
- `client.go`: `ClientTemplate` — a browser HTML/JS test client.

### grpc (reference / TODO)

Plain `google.golang.org/grpc` demo with keepalive policies
(`grpc_temp_server.go` includes an in-process client) and generated protobufs
(`protobuf/hello.proto`, `protobuf/temp.proto`). Not integrated with gnet —
kept as learning material for the "pb protocol over gnet" TODO.

## Running the Examples

```bash
# fixed-head TCP echo server (default port 5000) + 4 built-in test clients
go run ./examples/tcp_fixed_head --port 5000 --multicore=true

# WebSocket server on :8081 plus an HTTP page on :8080 to drive a browser client
go run ./examples/websocket
```

Common gnet options used by examples: `WithReusePort(true)`,
`WithMulticore(true)`, `WithTCPKeepAlive(90s)`.

## Extension Points / Conventions

- New protocol server = implement `gnet.EventHandle` in its own package,
  decode in `OnTraffic`, offload to ants, expose a `Handler` func type and a
  `New*Server` constructor (see `tcp_fixed_head` as the template).
- Streaming decode always uses the conn-context pattern: parse header once,
  stash expected length, wait for the full body.
- Logging goes through `commonutil.GetLogger()`, never `fmt`/`log` directly,
  in library packages.
- TODOs from README: MQTT, centralized connection management, pb-over-gnet,
  thrift, long-connection maintenance platform.

## Quality Tooling

`.golangci.yml` configures linting (go vet, errcheck-style checks); no test
files exist yet in the repo.
