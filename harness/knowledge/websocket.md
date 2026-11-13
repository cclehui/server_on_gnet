# websocket

WebSocket server built on [coder/websocket](https://github.com/coder/websocket)
on top of a gnet event loop. The interesting part is the goroutine split, not
the protocol (RFC 6455 is implemented by the dependency).

## Why the reactor no longer decodes

`coder/websocket` is a **blocking, net.Conn-shaped** library:

- `Accept` needs an `http.ResponseWriter` that implements `http.Hijacker`;
- `Conn.Read`/`Write` do blocking `io.ReadFull` / buffered writes on the
  underlying `net.Conn`.

gnet's `Conn.Read` is non-blocking: it returns whatever is in the read buffer
and `io.ErrShortBuffer` when it is empty (`connection_unix.go`). Feeding it to
`io.ReadFull` is what broke the previous `gobwas/ws` implementation: a frame
split across two TCP segments fails mid-parse, the consumed header is lost, and
the next parse starts from the middle of a frame (`use of reserved op code`).

So the split is:

| Goroutine | Does |
|---|---|
| reactor (`OnTraffic`) | one `Conn.Read` copy into `inboundBuffer`; nothing else |
| per-conn read goroutine (`serveConn`) | handshake, then a blocking `conn.Read` loop |
| ants worker | `DataHandler` business logic |
| gnet event loop (via `AsyncWrite`) | all socket writes |

## The adapter: `WSConn` (`conn.go`)

```go
type WSConn struct {
    LastActiveTs int64   // must stay first: 64-bit atomic alignment on 32-bit
    GnetConn     gnet.Conn
    UniqId       string  // reserved, never populated
    inbound      *inboundBuffer
    localAddr, remoteAddr net.Addr // copied in OnOpen, event-loop-only getters
    wsConnMu     sync.RWMutex
    wsConn       *coderws.Conn     // published after a successful handshake
}
```

`*WSConn` implements `net.Conn`, which is what `coder/websocket` needs:

- `Read` → `inboundBuffer.Read`: **blocks** until the reactor appends bytes or
  the deadline/close fires. This is the resumable read the old code lacked.
- `Write` → copy, then `GnetConn.AsyncWrite(buf, nil)`. The copy is mandatory:
  `AsyncWrite` only runs on the event loop later, and gnet's borrowed buffers
  must not escape `OnTraffic`.
- `Close` → closes `inboundBuffer` (wakes the blocked reader) and `GnetConn`.
  It is called concurrently by `coder/websocket` (e.g. write timeout), so it
  must stay idempotent.
- `SetReadDeadline` is real (the handshake timeout relies on it);
  `SetWriteDeadline` is a no-op because writes are queued on the event loop and
  `coder/websocket` controls its own write timeouts with `context` instead.

`inboundBuffer` is a small mutex-protected byte queue with a cap-1 wake channel
and reusable backing array (freed when an emptied buffer holds more than 64 KiB).
`Append` never blocks; `Close` makes pending and future reads return `io.EOF`.

## The handshake

`handshakeResponseWriter` exists only to satisfy `coderws.Accept`:

- `WriteHeader` assembles `HTTP/1.1 <code> <text>` + headers + CRLF and sends it
  through `WSConn.Write` (so it goes out on the event loop like every write);
- `Hijack` returns `w.conn` and a `bufio.ReadWriter` whose reader is the **same**
  `*bufio.Reader` used to parse the request. `Accept` peeks and re-injects its
  buffered bytes, so a client that pipelines its first frame into the handshake
  packet does not lose it. Verified with a raw TCP client.

`runtime.Version()` is still advertised as `X-Go-Version-CCLehui`, preserving
the header of the previous implementation.

## `OnOpen` / `OnTraffic` / `serveConn`

```
OnOpen      → NewWSConn(c), c.SetContext(wsConn), go serveConn(wsConn)
OnTraffic   → ctx = c.Context().(*WSConn); copy c.Read buf; wsConn.appendInbound
              (bad context → gnet.Close; 0 bytes → gnet.None)
serveConn   → SetReadDeadline(now+HANDSHAKE_TIMEOUT)
              accept() ─── error → log, close GnetConn, return
              SetReadDeadline(zero), SetReadLimit(MAX_MESSAGE_BYTES)
              publish wsConn, updateConnActiveTs
              defer wsConnProto.Close(StatusNormalClosure, "")
              for { typ, msg, err := Read(ctx); err → logReadExit, return
                    dispatch(wsConn, typ, msg) }
```

Things that matter:

- **Handshake state is not in the conn context.** It is implicit in the blocked
  `http.ReadRequest` inside the read goroutine; the reactor keeps feeding bytes.
  Do not add a second parser in `OnTraffic`.
- **Control frames are the library's job.** Ping/Pong, close frames, UTF-8
  validation of text and fragmentation are all handled inside `Read`/`Write`.
  Binary messages are now delivered to `DataHandler` too (the old code logged
  them as unsupported).
- **`HANDSHAKE_TIMEOUT`** stops a half-sent HTTP request from parking a read
  goroutine and its connection forever.
- A read error ends the loop; `defer` then runs `Conn.Close`, which performs the
  close handshake (5s cap inside the library) — but only after the reader has
  stopped, which is exactly the library's documented usage.

## Constructor options

All constants live in `websocket/constants.go` and are `UPPER_SNAKE`; the
exported sentinel errors live in `websocket/exceptions.go` (`ErrNotUpgraded`).
There is no `config.go` in this package.

`NewServer(addr string, opts ...ServerOption)` wires everything the struct
exposes, so callers do not have to mutate fields after construction. The order
is **options first, defaults second**: each default is only filled in when the
corresponding field is still unset. A knob passed to `NewServer` is therefore
never built and thrown away.

| Option | Effect | Default (only when the option is absent) |
|---|---|---|
| `WithHandler(h)` | `Handler` business entry point | `nil` (messages logged and dropped); `NewEchoServer` passes `EchoDataHandler` first so your `WithHandler` overrides it |
| `WithWorkerPool(pool)` | caller-owned ants pool replaces the default | size `DEFAULT_ANTS_POOL_SIZE`, `Nonblocking`, 10s expiry |
| `WithAcceptOptions(opts)` | `coderws.AcceptOptions` for the handshake | `InsecureSkipVerify: true` |
| `WithConnCloseHandler(fn)` | `ConnCloseHandler`, run inside `OnClose` | `nil` |
| `WithContext(ctx)` | context for server lifecycle logs | `context.Background()` |
| `WithConnIdleTimeout(seconds)` | replaces `CONN_MAX_IDLE_SECONDS` for that server | `CONN_MAX_IDLE_SECONDS` (600s) |

Options apply in order, last wins. `WithWorkerPool`, `WithAcceptOptions` and
`WithContext` panic on nil, `WithConnIdleTimeout` panics on a non-positive
value. `nil` never means "use the default": to get the library's own handshake
policy (Origin checked) pass `&coderws.AcceptOptions{}`, which is distinct from
omitting the option (`InsecureSkipVerify: true`).

The default ants pool and the time wheel both allocate, so they are created
after the options, and only if still nil. The wheel's slot count is the final
idle timeout in seconds, so the timeout cannot be changed after `NewServer`.

`connTimeWheel` and `ctx` stay unexported; the idle timeout now lives in
`server.connIdleSeconds` and the wheel job reads it from there instead of the
package constant.

## Handler contract

```go
type DataHandlerParam struct {
    Request     []byte
    MessageType coderws.MessageType // MessageText / MessageBinary
    WSConn      *WSConn             // WriteMessage / SendDownStreamMsg target
    Server      *WebSocketServer
}

type DataHandler func(param *DataHandlerParam)
```

- `Request` is freshly allocated per `Read`, so handing it to the ants worker is
  safe (no copy needed, unlike gnet's borrowed inbound buffer).
- The handler runs on the worker pool; `WSConn.WriteMessage` is safe there
  because `coder/websocket` serializes writes and the bytes ultimately leave via
  `AsyncWrite`. It has `SEND_TIMEOUT` (10s) so a peer that stops reading cannot
  block a worker forever.
- The default echo replies with the same `MessageType` and
  `"response is :<payload>, 当前时间:<ts>\n"`.
- `Write()`/`DataHandlerParam.Writer` from the gobwas version is gone; use
  `SendDownStreamMsg(wsConn, msgType, msg)` or `wsConn.WriteMessage(...)`.

## Time wheel → idle eviction

Unchanged shape: 1s tick, one slot per second over `CONN_MAX_IDLE_SECONDS` (600s),
`jobParam` carries `{server, wsConn}`, and the job re-arms itself if the
connection was active in the meantime. Two changes:

- `LastActiveTs` is read with `atomic.LoadInt64` (it is written on the reader
  goroutine via `UpdateActiveTsToNow`, and read from the wheel goroutine);
- eviction calls `closeConn`, which runs `coderws.Conn.Close(StatusGoingAway,
  "idle timeout")` **in a goroutine** (the wheel callback must not block) and
  falls back to `GnetConn.Close()` for connections that never finished the
  handshake. Verified: the client receives status 1001, not a bare EOF.

## Known limitations

1. **One goroutine per connection.** Inherent to the blocking library. It is
   cheap (a blocked read on a channel), but a connection flood does cost
   goroutines; the reactor itself still does no protocol work.
2. **Origin check is disabled by default.** `NewServer` installs
   `InsecureSkipVerify: true` to preserve the old "accept any Host" behavior;
   pass `WithAcceptOptions(&coderws.AcceptOptions{OriginPatterns: ...})` (or set
   `server.AcceptOptions` before `gnet.Run`) to turn it back on. No auth and no
   subprotocol negotiation by default.
3. **`OnTraffic` allocates one buffer per callback.** It is a plain copy of
   `InboundBuffered()` bytes; a pooling scheme would need to know when the
   reader is done with it.
4. **Read limit is 16 MiB** (`MAX_MESSAGE_BYTES`), matching
   `tcp_fixed_head.MaxDataLength`; oversized messages close with
   `StatusMessageTooBig` (1009).
