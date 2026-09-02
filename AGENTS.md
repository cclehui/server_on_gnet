# AGENTS.md

## Project

`server_on_gnet` — Go library of reusable server implementations built on the
**gnet** (`github.com/panjf2000/gnet/v2`) event-driven networking framework.
Module: `github.com/cclehui/server_on_gnet`, Go 1.19.

## Architecture

See [docs/architecture.md](docs/architecture.md) for the full design
(reactor + worker-pool pattern, per-connection streaming decode via
`Conn` context, package-by-protocol layout, time-wheel idle eviction).

## Key Keywords / Concepts

- **gnet** / **gnet v2** — event-driven, reactor-based networking framework
- **EventHandle** — server lifecycle interface: `OnBoot` / `OnShutdown` / `OnOpen` / `OnClose` / `OnTraffic` / `OnTick`
- **ants** (`panjf2000/ants/v2`) — worker goroutine pool for offloading business handling off the reactor
- **reactor goroutine** — does protocol **decode** only; business logic runs in the worker pool
- **Conn context** (`c.SetContext` / `c.Context()`) — per-connection parse state for **streaming / half-packet (粘包/拆包)** decoding
- **AsyncWrite** — non-blocking response write back on the event loop
- **tcp_fixed_head** — fixed 8-byte header protocol: `Version(0x8001) + ActionType + DataLength + Data`; actions `PING` / `PONG` / `DATA`; errors `ErrIncompletePacket` / `ErrProtocolVersion`
- **websocket** — WebSocket server via **gobwas/ws** + `wsutil`; `GnetUpgraderConn` adapts `gnet.Conn` to `io.Reader/Writer`; **handshake upgrade** state machine in `OnTraffic`
- **TimeWheel** — single-level time wheel for **idle connection eviction** (`ConnMaxIdleSeconds`, keepalive ping/pong)
- **Handler func type** — pluggable business entry point (`ServerHandler`, `DataHandler`) with default **echo** implementations
- **commonutil** — shared `Logger` interface (`GetLogger` / `SetLogger`, JSON logs, prefix `SERVER_ON_GNET`)
- **grpc / protobuf** — standalone gRPC keepalive demo + generated `.pb.go` (reference code, not gnet-based)
- **examples/** — runnable demos with test clients (`go run ./examples/tcp_fixed_head`, `go run ./examples/websocket`)
- **ReusePort / Multicore / TCPKeepAlive** — common gnet options used when starting servers
- graceful shutdown via OS signals (SIGHUP/QUIT/TERM/INT → `gnet.Stop`)

## Conventions for Agents

- New protocol server: own package, implement `gnet.EventHandle`, decode in
  `OnTraffic`, offload to ants, expose a `Handler` func type + `New*Server`
  constructor (follow `tcp_fixed_head`).
- Log through `commonutil.GetLogger()` in library packages, not `fmt`/`log`.
- Lint config: `.golangci.yml`; no tests exist yet — verify changes by running
  the examples.
