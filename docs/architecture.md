# server_on_gnet Architecture

A Go library of ready-to-use server implementations built on top of the
[gnet](https://github.com/panjf2000/gnet/v2) event-driven networking framework.
Module path: `github.com/cclehui/server_on_gnet`, Go 1.20.

## High-Level Design

All gnet-based servers follow the same core pattern:

1. **Reactor goroutine (gnet event loop)** — `OnTraffic` performs protocol
   decoding only. Per-connection parse state is kept via `gnet.Conn` context
   (`c.SetContext` / `c.Context()`), enabling streaming (half-packet) decoding.
2. **Worker pool (ants)** — decoded messages are submitted to an
   [`ants`](https://github.com/panjf2000/ants/v2) goroutine pool
   (`DEFAULT_ANTS_POOL_SIZE = 1024*1024`, nonblocking, 10s expiry) so business
   handlers never block the reactor.
3. **Pluggable handler functions** — servers expose a handler function type
   (`ServerHandler` / `DataHandler`) with a default echo implementation, so
   users inject their own business logic.
4. **Lifecycle callbacks** — each server implements the `gnet.EventHandle`
   interface: `OnBoot` / `OnShutdown` / `OnOpen` / `OnClose` / `OnTraffic` /
   `OnTick`, and tracks live connections with an atomic counter (`ConnNum`,
   mutated with `atomic.AddInt32` — read it through `GetConnNum()`; reading the
   field directly is a data race).

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
| `websocket/` | WebSocket server (gnet + coder/websocket) with idle-conn management |
| `grpc/` | Standalone gRPC + protobuf demo (not gnet-based, reference code) |
| `examples/` | Runnable demos with test clients for both server types |
| root (`proto_temp.go`) | Protobuf marshal/unmarshal scratch demo |
| `harness/` | Coding style + repo knowledge for agents/contributors |

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

- `constants.go`: `DefaultProtoAddr="tcp://:7001"`, `DefaultHeadLength=8` (the default for
  `TCPFixHeadProtocol.HeadLength`), `MaxDataLength=16MiB`,
  `PROTOCOL_VERSION=0x8001`, actions `ACTION_PING=0x0001` /
  `ACTION_PONG=0x0002` / `ACTION_DATA=0x00F0`, plus pool size.
- `exceptions.go`: sentinel errors — `ErrIncompletePacket` (keep buffering),
  `ErrProtocolVersion`, `ErrUnknownAction`, `ErrDataLengthExceeded`,
  `ErrContext`. `config.go` keeps only `ActionName` / `isCorrectAction`.
- `server_factory.go`: `NewTCPFixHeadServer(opts...)` — options first, then
  defaults (handler, ctx, ants pool, bound protocol). The listen address
  defaults to `DefaultProtoAddr` (`tcp://:7001`) and can be overridden with
  `WithProtoAddr`.
  `Run` is a method on the server and takes no arguments: `tcpServer.Run()`.
  The exported `ProtoAddr` field holds the gnet listen address (default
  `DefaultProtoAddr`, overridable with `WithProtoAddr` or by assignment), and
  `WithGnetOptions(...)` holds the engine options.
- `protocal.go`: `TCPFixHeadProtocol` —
  - `Decode(gnet.Conn)`: server-side streaming decode; partial frames return
    `ErrIncompletePacket` → handler returns `gnet.None` to wait for more data;
    invalid header → `gnet.Close`. Bad version / unknown action / oversized
    `DataLength` are rejected before any state is stored, so a corrupt stream
    can never park a connection in "waiting for a body that will never come".
    Zero-length bodies skip `Conn.Next` entirely — `Next(n<=0)` means "read the
    whole buffer" in gnet and would swallow the next pipelined frame.
  - `EncodeData` / `EncodeWrite`: frame encoding; `ClientDecode(net.Conn)`:
    blocking client-side decode using `io.ReadFull`.
- `server.go`: `TCPFixHeadServer` (implements `gnet.EventHandle`) + `Run()`
  which also installs an OS-signal goroutine (SIGHUP/QUIT/TERM/INT →
  `Engine.Stop`, captured in `OnBoot`; `gnet.Stop` is deprecated) for graceful
  shutdown.
- `handler_context.go`: `HandlerContext` carries `ProtocolData`, `gnet.Conn`,
  and the server, and implements `context.Context` so handlers can log with it.
- Log convention: every log line from this package is prefixed with `[server]`,
  and the client-side example logs use `[client-N]`, so you can `grep 'server]'`
  or `grep 'client-'` to separate the two sides of a conversation. `ActionName`
  turns an `ActionType` into `PING` / `PONG` / `DATA` for readable logs.
- `handler.go`: `defaultHandler` answers `ACTION_PING` with an `ACTION_PONG`
  frame written back via `Conn.AsyncWrite`.

### websocket — WebSocket server over gnet

Built on [coder/websocket](https://github.com/coder/websocket). That library is
**blocking and goroutine-oriented** (its handshake needs `http.Hijacker`, its
framing needs blocking reads), while gnet's `Conn.Read` is non-blocking. So the
integration deliberately splits work by goroutine instead of decoding inside
the reactor:

```
reactor (OnTraffic)          read goroutine (1 per conn)        business goroutines
───────────────────          ──────────────────────────        ────────────────────
copy InboundBuffered()  ───► inboundBuffer (blocking io.Reader)
                                    │
                             http.ReadRequest ─► coder/websocket.Accept
                                    │
                             conn.Read loop ──► WorkerPool.Submit ──► DataHandler
                                                                          │
                             AsyncWrite on the event loop  ◄── coder/websocket Write
```

- `conn.go`: `WSConn` wraps `gnet.Conn` and implements `net.Conn`; the
  `inboundBuffer` is the reactor → read-goroutine queue (blocking `Read`, so TCP
  segmentation can no longer desynchronize the stream), and `Write` copies then
  hands the buffer to `AsyncWrite`, which gnet executes on the event loop. That
  is why handlers may write from any goroutine.
- `conn.go`: `handshakeResponseWriter` satisfies `http.ResponseWriter` +
  `http.Hijacker` so `coder/websocket.Accept` can write its 101 response and
  take over the connection. `Hijack` returns the same `*bufio.Reader` used to
  parse the request, so a frame pipelined behind the handshake is not lost.
- `server.go`: `OnTraffic` only copies bytes; `OnOpen` starts
  `serveConn` (handshake with `HANDSHAKE_TIMEOUT`, then a `conn.Read` loop that
  dispatches text/binary to `DataHandler`). Ping/Pong/Close framing and close
  handshakes are handled inside the library, not in our code.
  `SendDownStreamMsg` / `WSConn.WriteMessage` push server-initiated messages;
  `ConnCloseHandler` hook for close callbacks.
- `timewheel.go`: single-level hashing time wheel (idea from
  `ouqiang/timewheel`) — 1s interval, one slot per second over the max idle
  window; used to evict connections idle longer than `CONN_MAX_IDLE_SECONDS`
  (600s, see `constants.go`). Eviction goes through `Conn.Close` so the peer
  gets a `StatusGoingAway` close frame.
- `constants.go`: every `UPPER_SNAKE` constant (`DEFAULT_ANTS_POOL_SIZE`,
  `CONN_MAX_IDLE_SECONDS`, `MAX_MESSAGE_BYTES`, `HANDSHAKE_TIMEOUT`,
  `SEND_TIMEOUT`, `HANDSHAKE_BUFFER_SIZE`, `GO_VERSION_HEADER_KEY`).
- `exceptions.go`: exported sentinel errors (`ErrNotUpgraded`).
- `data_handler.go`: `DataHandlerParam` (message bytes, `MessageType`, upgraded
  conn, server) and `EchoDataHandler` default echo.
- `html_template.go`: `ClientTemplate` — a browser HTML/JS test client; the
  message pane prepends new frames so the newest response stays on top.

### grpc (reference / TODO)

Plain `google.golang.org/grpc` demo with keepalive policies
(`grpc_temp_server.go` includes an in-process client) and generated protobufs
(`protobuf/hello.proto`, `protobuf/temp.proto`). Not integrated with gnet —
kept as learning material for the "pb protocol over gnet" TODO.

## Running the Examples

```bash
# fixed-head TCP PING/PONG server (default port 7001) + 4 built-in test clients.
# The demo is a guided scenario: prints the wire format up front, tags every log
# line with [server] or [client-N], and has client-2 deliberately inject 3 junk
# bytes so you can watch the server reject the frame and close that connection.
go run ./examples/tcp_fixed_head --port 7001 --multicore=true

# WebSocket server on :8081 plus an HTTP page on :8080 to drive a browser client
# (the page derives the ws:// URL from the request Host, so no editing needed).
go run ./examples/websocket

# Scripted WS client: echo round-trips + a frame split across TCP segments +
# concurrent connections. Exits non-zero on failure.
go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 8 -msgs 5
```

Common gnet options used by examples: `WithReusePort(true)`,
`WithMulticore(true)`, `WithTCPKeepAlive(90s)`.

## Known Limitations

Recorded so they are not mistaken for intended behavior. Details live in
`harness/knowledge/websocket.md`.

- **One goroutine per WebSocket connection.** `coder/websocket` is blocking by
  design; the per-conn reader blocks in `inboundBuffer.Read`. This is the price
  of correct framing (and of close/ping handling inside the library). The GOMAXPROCS-bounded
  reactor still does no protocol work.
- **`c.SetContext` / `SetSafeContext` is not used for the wrapper.** The
  wrapper lives in `Context()`, which is only valid inside `EventHandler`
  callbacks; other goroutines only touch the wrapper's own mutex-protected
  fields, never `gnet.Conn`'s reader/writer methods.
- **Origin verification is off by default.** `NewServer` sets
  `AcceptOptions.InsecureSkipVerify = true` to preserve the previous
  "accept any Host" behavior; pass `websocket.WithAcceptOptions` with
  `OriginPatterns` (or flip the flag off) before exposing the server to
  browsers on untrusted origins. There is still no auth or subprotocol
  negotiation by default.
- **WebSocket server options.** `NewServer(addr, opts...)` /
  `NewEchoServer(addr, opts...)` accept `WithHandler`, `WithWorkerPool`,
  `WithAcceptOptions`, `WithConnCloseHandler`, `WithContext`, and
  `WithConnIdleTimeout`; see `harness/knowledge/websocket.md`.
- `.golangci.yml` is a copied config: the linter is not installed and several
  enabled linters no longer exist upstream. Formatting is `gofmt`/`goimports`.

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

`gofmt -l .`, `go vet ./...`, and `go build ./...` are the checks that run
here; `go test ./...` currently reports "no test files" for every package
(there are no unit tests). `.golangci.yml` is a copied config for an older
`golangci-lint` and is not runnable as-is (not installed, several linters
removed upstream) — do not modernize it as a side effect of another change.
Verification for protocol changes is running the matching example and reading
its `[server]` / `[client-N]` log lines; see
`harness/knowledge/testing.md`.
