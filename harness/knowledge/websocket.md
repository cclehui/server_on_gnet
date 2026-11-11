# websocket

WebSocket server built by putting a `gobwas/ws` parser on top of a gnet event
loop. The interesting part is the adapter and the state machine, not the
protocol (the protocol is RFC 6455, implemented by the dependency).

## The adapter: `gnet.Conn` → `io.Reader` / `io.Writer`

`gobwas/ws`, `wsutil`, and `http`-style handshake parsing all want an
`io.Reader`/`io.Writer`. gnet v2's `Conn` already implements both (`Read`,
`Write`), so `GnetUpgraderConn` is a thin wrapper:

```go
type GnetUpgraderConn struct {
    GnetConn          gnet.Conn
    UniqId            string       // reserved, never populated
    LastActiveTs      int64        // unix seconds, for idle eviction
    IsSuccessUpgraded bool
    Upgrader          ws.Upgrader
}

func (u GnetUpgraderConn) Read(b []byte) (int, error)  { return u.GnetConn.Read(b) }
func (u GnetUpgraderConn) Write(b []byte) (int, error) { return u.GnetConn.Write(b) }
```

- The methods use **value receivers** on purpose: `wsutil.WriteServerMessage`
  takes an `io.Writer`, and a value satisfies it without an allocation. The
  struct embeds an interface, so copying it still points at the same connection.
- The hand-rolled `Read`/`Write` bodies are commented out below the delegating
  line. They existed before gnet v2 added `io.Reader`/`io.Writer`; do not
  restore them.
- Mutating fields (`LastActiveTs`, `IsSuccessUpgraded`) always goes through the
  **pointer** (`*GnetUpgraderConn`), which is what the connection context holds.

## Two upgraders

| Constructor | `Upgrader` | Use |
|---|---|---|
| `NewDefaultUpgrader(conn)` | `defaultUpgrader` — logs host/header/request, adds `X-Go-Version-CCLehui: <runtime.Version()>` to the handshake response | the server, via `OnTraffic` |
| `NewEmptyUpgrader(conn)` | `emptyUpgrader` (`ws.Upgrader{}`) | opt-out, not wired into the server |

`defaultUpgrader`'s `OnHost` accepts everything (it only logs unless the host is
`github.com`); **it does not authenticate or origin-check**. There is no
`Origin` validation and no `CheckOrigin`-equivalent hook here. If this server
is ever exposed to a browser on an untrusted origin, that policy has to be
added in `OnHeader`/`OnRequest` (compare against an allowlist) before the
upgrade completes.

## `OnTraffic` state machine

```
Context() == nil            → create NewDefaultUpgrader, c.SetContext(it)
Context() not *GnetUpgraderConn → log, return gnet.None   (defensive)
!IsSuccessUpgraded          → Upgrader.Upgrade(conn)
                                 error → log, return gnet.Close
                                 ok    → IsSuccessUpgraded = true,
                                         updateConnActiveTs(conn)
IsSuccessUpgraded           → wsutil.ReadClientMessage(conn, nil)
                                 error → log, return gnet.None
                                 ok    → for each message: dispatch (below)
```

Dispatch per message:

| Opcode | Action |
|---|---|
| `OpPing` | reply `OpPong` immediately (from the reactor), refresh `LastActiveTs` |
| `OpText` | `WorkerPool.Submit` → `Handler(&DataHandlerParam{...})`, refresh `LastActiveTs` |
| `OpClose` | log, `closeConn` (writes a close frame), return `gnet.Close` |
| other (`OpBinary`, `OpPong`, continuation, …) | log "unsupported" and ignore |

Notes that matter:

- **Handshake state lives in the connection context**, exactly like the TCP
  decoder's parse state. The first `OnTraffic` on a connection may carry only
  part of the HTTP upgrade request; `Upgrader.Upgrade` is a resumable
  `io.Reader`-based parse, so re-entering it on the next callback is correct.
  Do not try to buffer the handshake yourself.
- `wsutil.ReadClientMessage` returns **multiple** messages when the peer sent a
  fragmented message with interleaved control frames. The `for` loop over
  `messages` is required; taking `messages[0]` loses frames.
- `ReadClientMessage` sets `CheckUTF8: true`, so invalid UTF-8 in a text frame
  is an error, not a payload.
- A read error returns `gnet.None` — the connection stays open. That is
  intentional for transient conditions but means a genuinely broken stream is
  not closed; see "Known limitations" below.

## Handler contract

```go
type DataHandlerParam struct {
    Request []byte
    OpCode  ws.OpCode
    Writer  io.Writer          // the upgrader conn
    WSConn  *GnetUpgraderConn  // same object, typed
    Server  *WebSocketServer
}

type DataHandler func(param *DataHandlerParam)

var EchoDataHandler DataHandler = func(p *DataHandlerParam) { ... }
```

- `Writer` and `WSConn` point at the same connection; `Writer` is there so a
  handler can hand it to `wsutil.WriteServerMessage` without importing the
  concrete type.
- The handler runs on the worker pool, so it must not block indefinitely and
  must not assume the connection is still alive (check `WSConn.GnetConn`).
- The default echo replies with the same opcode and
  `"response is :<payload>, 当前时间:<ts>\n"`.

## Time wheel → idle eviction

`timewheel.go` is a single-level hashing wheel (idea from `ouqiang/timewheel`)
used only for idle-connection eviction.

- `newTimeWheel(1s, ConnMaxIdleSeconds, timeWheelJob)` — one slot per second
  over the max idle window (600 slots by default), started in `OnBoot`.
- All mutation goes through channels (`addTaskChannel`, `removeTaskChannel`,
  `stopChannel`), so `slots`, `timer` and `currentPos` are touched by exactly one
  goroutine. Do not add locking "for safety" — it would hide the real invariant.
- Tasks are keyed by `nil`, so there is no dedupe and `RemoveTimer` is unused.
  Each connection gets one timer chain: `updateConnActiveTs` adds a timer the
  first time it sees a connection whose `LastActiveTs` is stale (0 on a fresh
  connection), and `timeWheelJob` re-arms itself when it fires early.
- `timeWheelJob` compares `now - LastActiveTs` against `ConnMaxIdleSeconds`:
  still idle → `closeConn`; activity happened → re-arm for the remainder.
- `AddTimer` is a **channel send**, so it blocks if the wheel goroutine is busy.
  It is called from `OnTraffic` (reactor) — keep the job callback short.
- The wheel is never stopped (`Stop` is unused); it dies with the process.

## Sending server-initiated messages

`SendDownStreamMsg(wsConn, opcode, msg)` refreshes the idle timestamp and writes
one frame. It is the only public downlink entry point. There is **no**
broadcast/registry — the caller must already hold the `*GnetUpgraderConn`
(e.g. from `ConnCloseHandler`'s counterpart, a handler invocation).

## `client.go`

`ClientTemplate` is an `html/template` page (open/send/close buttons, output
pane) that the demo's HTTP server renders with a `ws://` URL. The demo
currently hard-codes `ws://192.168.126.139:8081`, so a fresh checkout shows
`OPEN`/`CLOSE` failing unless you edit that address to your host. That is a
demo wart, not a server bug.

## The demo

`examples/websocket/websocket_server.go`:

- gnet ws server on `:8081` (`WithReusePort`, `WithMulticore`), printing
  `GetConnNum()` every 2s;
- a plain `net/http` server on `0.0.0.0:8080` serving `ClientTemplate`;
- `log.Fatal(gnet.Run(...))` inside a goroutine, so a bind error exits the
  process.

There is no automated ws client, so verifying a change means opening the page
(or any ws client) and watching the `[server]` log lines.

## Known limitations

Recorded so the next reader does not mistake them for design. The first three
were reproduced on this machine (Go 1.26.2, gnet v2.10.0, gobwas/ws v1.1.0) with
a scratch client against a server started from this package — they are
observations, not guesses.

1. **`Read` does not buffer, so split frames desynchronize the connection.**
   gnet's `Conn.Read` is non-blocking: it returns whatever is currently in the
   read buffer and `io.ErrShortBuffer` when there is nothing (`connection_unix.go`).
   `wsutil.ReadClientMessage` → `wsutil.Reader` → `io.ReadFull` therefore fails
   whenever a WebSocket frame arrives in two TCP segments. The error path logs
   `读取客户端消息失败 ... err:short buffer` and returns `gnet.None`, but the
   frame header (and any payload bytes already read) are **gone**, so the next
   parse starts mid-frame and reports
   `err:use of reserved op code`. Reproduced deliberately by writing one masked
   text frame as `frame[:3]` then `frame[3:]` 300ms later: handshake succeeds,
   one `short buffer`, one `reserved op code`, no reply, client read times out.
   The two fragments must be joined before parsing.
2. **Round-trip failures happen on loopback too.** A 12-connection loop, one
   text message each, `ws://127.0.0.1`: two runs produced `ok=11 fail=1` and
   `ok=9 fail=3`, failing with an i/o timeout after the server logged
   `short buffer`. So "TCP usually delivers a small frame in one segment" is not
   a safe assumption even in a demo.
3. **Downlink writes run off the event loop and race.** `EchoDataHandler` and
   `SendDownStreamMsg` call `wsutil.WriteServerMessage` → `Conn.Write` from the
   ants worker, while gnet documents `Write` as event-loop-only (and
   `wsutil.WriteServerMessage` issues header and payload as two separate
   writes, which can interleave). `go run -race` against 8 concurrent echo
   clients reports `WARNING: DATA RACE`: `(*GnetUpgraderConn).Write` from
   `ants.(*goWorker).run` versus `updateConnActiveTs` writing `LastActiveTs` on
   the reactor (`server.go:183`). The value receiver on `Read`/`Write` copies
   the whole struct, so even a "read-only" write path races with the timestamp
   update.
   The safe shape is: encode into a local buffer with `ws.WriteFrame`, then
   `Conn.AsyncWrite(buf, cb)`; and take `GnetConn` out of the struct before
   encoding so the copy does not include mutable fields.
4. **No read limit.** `wsutil.ReadMessage` is built without `MaxFrameSize`, so a
   peer can declare a huge frame and make the server allocate it. The TCP
   protocol caps a frame at 16 MiB; this one has no cap.
5. **No origin check, no auth, no subprotocol negotiation.** `defaultUpgrader`
   accepts any `Host` and logs headers only.
6. **No close handshake on eviction.** `closeConn` writes a close frame but does
   not wait for the peer's close or drain pending writes.

Fixing 1-3 is a real change, not a docs tweak: it needs a per-connection reader
that accumulates `InboundBuffered()` bytes before parsing, and an outbound path
that only encodes on the reactor (or `AsyncWrite`s pre-encoded frames). Do not
paper over it with a bigger read timeout.
