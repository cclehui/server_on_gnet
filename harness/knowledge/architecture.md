# Architecture: reactor + worker pool

Repo-wide runtime model. Per-protocol detail lives in `tcp_fixed_head.md` and
`websocket.md`; this file is the part both share.

## The three layers

```
 client ──TCP──► gnet reactor ──decode──► ants worker pool ──► Handler
                    │  (OnTraffic)          (business logic)
                    │
                    └── AsyncWrite ◄──────── encoded response
```

1. **Reactor goroutine** — one per event loop (`gnet.WithMulticore(true)`
   starts `GOMAXPROCS` of them; the demo on a 128-core box logs `128
   event-loops`). All `gnet.EventHandler` callbacks run here. Work must be
   bounded and non-blocking; the only thing the reactor does is parse bytes off
   the socket and hand complete messages away.
2. **Worker pool** — `ants` (`panjf2000/ants/v2`), created per server in the
   constructor with `ExpiryDuration: 10s` and `Nonblocking: true`. Business
   handlers run here, so a slow handler delays other messages, not all sockets.
3. **Handler func** — the pluggable business entry point. Default is an echo /
   ping-pong implementation; the demo replaces it.

`Nonblocking: true` is the load-shedding policy: when the pool is exhausted,
`Submit` refuses the task instead of blocking the reactor. That is the right
trade for a network server (a stalled reactor freezes every connection it
owns), but it means a saturated pool silently drops messages today, because the
`Submit` error is not checked.

## Why per-connection state lives in `Conn` context

TCP is a byte stream, not a message stream: a single `OnTraffic` call may carry
half a frame, one frame, or five frames plus half a sixth. The decoder
therefore needs somewhere to remember "I read the header, I am waiting for N
more body bytes".

That place is `c.SetContext(...)` / `c.Context()`, the per-connection slot gnet
provides. It is scoped to one connection and only touched from that
connection's `OnTraffic`, so no lock is needed. The alternative — a
`map[fd]*state` owned by the server — needs its own synchronization and a
cleanup hook on close for no benefit.

Two consequences:

- **Never call `SetContext` from a worker goroutine.** gnet documents it as
  "not concurrency-safe, invoke within any method in EventHandler". Use
  `SetSafeContext` if you genuinely must, and understand the ordering you are
  creating.
- **Clear the context when the frame completes** (`c.SetContext(nil)`), so the
  next frame starts from a clean state. `tcp_fixed_head.Decode` does this; the
  websocket server's close path returns `gnet.Close` instead.

## The `OnTraffic` contract

```go
func (s *XServer) OnTraffic(c gnet.Conn) gnet.Action
```

| Return | Meaning |
|---|---|
| `gnet.None` | keep the connection, keep buffered bytes; next read resumes here |
| `gnet.Close` | protocol violation; close after this callback |
| `gnet.Shutdown` | stop the whole engine (not used by these packages) |

Rules that hold for both protocols:

- **Loop until the buffer is short.** One `OnTraffic` may contain several
  complete frames (pipelining / 粘包). Returning after the first one makes
  responses lag by one frame, which looks like a network delay and is easy to
  misdiagnose. `tcp_fixed_head` loops; the websocket server drains all messages
  `wsutil.ReadClientMessage` returns in one call.
- **Buffer bytes from `Next`/`Read` are borrowed.** `Next` hands you a slice of
  gnet's inbound ring buffer and advances the read pointer; the memory is
  reused after the callback. Anything that survives the callback — a decoded
  body passed to a worker, a payload stored on a struct — must be `copy`'d
  first. `tcp_fixed_head` does this explicitly with a `make` + `copy`.
- **`Next(n)` with `n <= 0` means "read everything buffered".** Do not rely on
  `Next(0)` being a no-op. Skip the call for a zero-length body, or the next
  frame in the same buffer is consumed as if it were part of this one.
- **Don't touch `c.Reader`/`c.Writer` methods from a worker.** `Read`, `Next`,
  `Peek`, `Discard`, `Write`, `Writev`, `Flush`, `LocalAddr`, `RemoteAddr` are
  all documented as event-loop-only. The exception is `AsyncWrite` /
  `AsyncWritev` / `Close` / `Wake`, which are concurrency-safe.

## Response path

Two ways to write back, with different rules:

| API | Where it may be called | Behavior |
|---|---|---|
| `c.AsyncWrite(buf, cb)` | anywhere, including a worker | queues the write onto the owner event loop; the callback runs there |
| `c.Write(buf)` | reactor only | synchronous write |

Handlers run on the worker pool, so they use `AsyncWrite`. The write is a
*queue*, not a completion: after `AsyncWrite` returns, the bytes are not
necessarily on the wire, and the callback gets the real error. That is why the
fixed-head demo sets a read deadline on its client side instead of assuming the
reply is already there.

## Lifecycle and shutdown

`gnet.EventHandler` members used here: `OnBoot`, `OnShutdown`, `OnOpen`,
`OnClose`, `OnTraffic`, `OnTick`.

`OnTick` is effectively disabled: both packages return a zero delay, and gnet
only starts the ticker when `WithTicker(true)` was passed (and then only on
event-loop 0 — `engine_unix.go`). None of the demos pass that option. Periodic
work therefore does not belong in `OnTick` here; the idle eviction runs on the
ws time wheel and the connection-count printing on a plain goroutine.

Graceful shutdown is installed by `tcp_fixed_head.(*TCPFixHeadServer).Run`:

```go
signal.Notify(ch, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
for s := range ch { engine.Stop(ctx) }
```

Notes:

- It uses **`Engine.Stop`**, not the package-level `gnet.Stop`: the latter is
  deprecated in gnet v2 (it looks the engine up in a global map by
  `protoAddr`, so restarting on the same address with `WithReuseAddr`/
  `WithReusePort` can stop the wrong engine and leak it).
- The `Engine` value only exists inside `OnBoot`, so `OnBoot` hands it to the
  signal goroutine over a buffered channel; `OnShutdown` closes that channel
  (guarded by `sync.Once`) so a signal goroutine that never got an engine
  exits instead of blocking forever.
- The listen address lives in the exported `TCPFixHeadServer.ProtoAddr` field
  (`tcp://host:port`); the constructor fills `DefaultProtoAddr` (`tcp://:7001`)
  when `WithProtoAddr` was not used.
- The goroutine is started *before* `gnet.Run`, so a signal arriving during
  startup is not lost.
- The signal goroutine logs and exits; it never calls `os.Exit`, so deferred
  cleanup in `main` still runs.

## Connection counting

Both servers expose `GetConnNum()` and keep the counter as an `int32` (TCP) or
`int64` (ws) field mutated with `atomic.AddInt32` / `atomic.AddInt64` in
`OnOpen` / `OnClose`. Reading the field directly is a data race — the demo's
"wait until the count reaches 0" loop is exactly the code that would trip it.
Use the getter.

`OnClose` fires once per connection, including connections closed by a decode
error. Do not also decrement on a `gnet.Close` return, or the counter goes
negative.

## `commonutil`

- `log.go`: `Logger` interface (`Debugf/Infof/Warnf/Errorf` with a
  `context.Context`), JSON-line `DefaultLogger`, `SetLogger`/`GetLogger`.
  See `logging.md`.
- `codec_empty.go`: `CodecEmpty`, a no-op `Encode`/`Decode`. Nothing in the
  repo currently uses it; it is a scaffold for the "pb protocol" TODO. Do not
  wire it up speculatively.
- `gnet_option.go`: `package commonutil` with no declarations. It exists as a
  placement marker; options are passed as raw `[]gnet.Option` by the demos.

## What is deliberately missing

- No connection registry / broadcast: `websocket.SendDownStreamMsg` sends to
  one connection you already hold. "Push to all clients" is the
  `连接管理` / `长连接维持平台` TODO, not something to bolt on ad hoc.
- No reconnect or retry policy on the client side of the demos.
- No metrics, tracing, or config layer. `Port` / `Addr` are constructor
  arguments.
