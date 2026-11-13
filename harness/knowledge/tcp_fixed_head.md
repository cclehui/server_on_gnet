# tcp_fixed_head

Fixed 8-byte header, variable body. This is the reference implementation of
"how to write a streaming protocol server on gnet here" — read it before
writing a new protocol package.

## Wire format

```
 offset 0        2        4                      8
        +--------+--------+----------------------+-----------------+
        |Version | Action | DataLength (uint32)  | Data[DataLength]|
        | uint16 | uint16 |      big-endian      |                 |
        +--------+--------+----------------------+-----------------+
```

- All fields are **big-endian**; the header is exactly
  `DefaultHeadLength = 8`, stored per instance in `TCPFixHeadProtocol.HeadLength`
  (set by `NewTCPFixHeadProtocol`, overridable with `WithHeadLength`).
- `Version` must be `PROTOCOL_VERSION = 0x8001` (32769).
- `ActionType` is one of `ACTION_PING = 0x0001`, `ACTION_PONG = 0x0002`,
  `ACTION_DATA = 0x00F0`. `ActionName` maps them to `PING` / `PONG` / `DATA`
  for logs and prints `UNKNOWN(0x%04X)` otherwise.
- `DataLength` is the body length only (header excluded) and is capped at
  `MaxDataLength = 16 MiB`.
- A body of `DataLength == 0` is legal (PING/PONG with no payload) and must not
  consume bytes.

## Three entry points, three jobs

| Function | Input | Blocking | Used by |
|---|---|---|---|
| `Decode(c gnet.Conn)` | gnet connection | no | server, inside `OnTraffic` |
| `DecodeFrame(frame []byte)` | one already-delimited frame | no | offline decoding; **unused today** |
| `ClientDecode(conn net.Conn)` | `net.Conn` | **yes** (`io.ReadFull`) | the demo client |
| `EncodeData(action, data)` | action + body | no | build a frame in memory |
| `EncodeWrite(action, data, conn)` | action + body + `io.Writer` | yes | stream a frame to a writer |
| `Encode(c, buf)` | gnet codec hook | no | returns `buf` unchanged (no-op); gnet v2 has no codec option, so nothing calls it |

`ClientDecode` blocks until a full frame arrives; call it from a goroutine with
a read deadline set (`conn.SetReadDeadline`), which is what the demo does.

## Server-side streaming decode

`Decode` is a two-phase state machine keyed on `c.Context()`:

```
Context() == nil
  └─ have >= 8 header bytes?
       no  → return ErrIncompletePacket  (caller returns gnet.None)
       yes → Next(8), parse Version/Action/DataLength
              validate version, action, length  ← any failure: return error
              c.SetContext(&ProtocolData{header only})
Context() != nil
  └─ have >= DataLength bytes?
       no  → return ErrIncompletePacket
       yes → Next(DataLength)  [skipped when DataLength == 0]
              copy the bytes
              c.SetContext(nil)
              return the frame
```

Three things are deliberate and easy to undo:

1. **Validation happens before `SetContext`.** If a bogus header were stored
   first, a rejected frame would leave the connection parked in "waiting for a
   body" with a length that will never arrive, and every later byte would be
   misinterpreted. Rejecting up front means a bad header is a hard close.
2. **The body is `copy`'d.** `Next` returns a slice of gnet's read buffer, which
   is reused after `OnTraffic` returns. The decoded `ProtocolData` is handed to
   a worker goroutine, so it must own its bytes.
3. **`Next` is not called for a zero-length body.** gnet's `Next(n)` treats
   `n <= 0` as "read the whole buffer", so calling it for an empty PING body
   would swallow the next pipelined frame and desynchronize the stream.

Also: the length check bound `MaxDataLength` is not cosmetic. `DataLength` is
`uint32`; converting an unvalidated value to `int` overflows to a negative on
32-bit platforms, which turns into `Next(negative)` and therefore "read
everything". 16 MiB also bounds how long a single forged header can pin a
connection's memory.

### Context type mismatch

If `c.Context()` is set but is not a `*ProtocolData`, the decoder clears it and
returns `ErrContext`. That turns "someone else wrote our context slot" into a
closed connection instead of a panic in a reactor goroutine (a panic there
takes down the whole process — there is no recover).

## `OnTraffic` loop

```go
for {
    protocolData, err := protocol.Decode(c)
    if err != nil {
        if err == ErrIncompletePacket {
            return gnet.None            // half packet: keep buffering
        }
        log.Errorf(...)                 // includes the offending values
        return gnet.Close
    }
    pool.Submit(func(){ handler(&HandlerContext{ProtocolData: protocolData, Conn: c, server: s}) })
}
```

- The `for` loop is what handles 粘包 (several frames in one read). Removing it
  makes every response lag one frame behind its request.
- `protocol` is constructed once per `OnTraffic` call, not per server; it holds
  no state (all state is in the connection context), so this is cheap.
- The handler closure captures `protocolData` and `c`. Both are per-iteration
  values, so each submitted task is independent.

## Handler contract

```go
type ServerHandler func(ctx *HandlerContext)

type HandlerContext struct {
    ProtocolData *ProtocolData
    Conn         gnet.Conn
    server       *TCPFixHeadServer
}
```

- `HandlerContext` implements `context.Context` (`Deadline`/`Done`/`Err`/`Value`
  all return zero values) purely so it can be passed as the `ctx` argument to
  `commonutil.GetLogger()` without allocating. It carries no cancellation —
  do not build cancellation semantics on it.
- The handler runs **on the worker pool**, so: no `Next`/`Read`/`Write` on
  `Conn`; write replies with `Conn.AsyncWrite`.
- `ProtocolData` may be `nil` in a hand-constructed context; the default
  handler returns early in that case. Keep that guard in new handlers.
- `remoteAddr(c)` exists because `c` (and `c.RemoteAddr()`) can be nil in a
  synthetic context. Use it rather than `ctx.Conn.RemoteAddr()` in log lines.

Default handler behavior: log every frame, and answer `ACTION_PING` with
`ACTION_PONG` whose payload is `"pong, " + <ping payload>`. `ACTION_DATA` is
accepted by the decoder but the default handler ignores it — a real service
replaces `server.Handler`.

## The demo

`examples/tcp_fixed_head/tcp_fixed_head_server.go` is the executable spec:

- starts the server with `WithReusePort(true)`, `WithMulticore(multicore)`,
  `WithTCPKeepAlive(90s)`;
- dials 4 clients concurrently, each sending 3 PINGs and reading 3 PONGs;
- `client-2` writes 3 junk bytes (`"xxx"`) before its second PING, so the
  server parses a bad header, logs `PROTOCOL_VERSION error` and closes that
  connection. **That disconnect is the expected output**, not a failure;
- after all clients end, waits (up to 3s) for `GetConnNum()` to reach 0 to show
  `OnClose` ran.

Every log line is tagged: `[server] ` from the library/demo handler and
`[client-N] ` from the demo client wrapper, so `grep 'server]'` /
`grep 'client-'` separates the two sides. A `sync.RWMutex`-guarded
`local addr → client-N` map lets the server log name the peer, since four
connections arrive in arbitrary order.

## Changing it

- New action type: add the constant, add it to `isCorrectAction`, add it to
  `ActionName`, and decide explicitly whether the default handler answers it.
  Forgetting `isCorrectAction` makes the new frame a protocol error; forgetting
  `ActionName` makes logs print `UNKNOWN`.
- New header field: increase `DefaultHeadLength` (the per-instance
  `HeadLength` defaults to it), update `Decode`,
  `DecodeFrame`, `ClientDecode`, and `EncodeData`/`EncodeWrite` together, and
  update the demo banner and `docs/architecture.md`. A header length change is
  a breaking wire change — say so in the answer.
- Raising `MaxDataLength` changes the worst-case memory a single connection can
  pin; raise it only with a buffer strategy in mind.
