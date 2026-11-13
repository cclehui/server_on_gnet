# Testing and Verification

## What exists

| Layer | Command | Status |
|---|---|---|
| Format | `gofmt -l .` | must print nothing |
| Vet | `go vet ./...` | must pass |
| Build | `go build ./...` | must pass |
| Unit tests | `go test ./...` | **no test files in any package** |

That is the whole automated surface. There is no CI config, no mutation of
`go.mod` during tests, and no external service to stand up — everything is a
process plus a TCP/ws socket.

`golangci-lint` does not run here: it is not installed, and the vendored
`.golangci.yml` names linters that were removed upstream (`interfacer`,
`scopelint`, `maligned`, `deadcode`, `structcheck`, `varcheck`) and pins
`stylecheck` to a Go 1.16 language level. **Do not treat lint output as a
blocker and do not modernize the config as a side effect of a feature change.**
If lint becomes part of the workflow, that is its own task with its own
verification.

## Verifying behavior without tests

The examples are the tests. Each one starts a real server, drives a real
client, and logs both sides of the conversation.

### tcp_fixed_head

```bash
go run ./examples/tcp_fixed_head --port 7001 --multicore=true
```

Expected shape of the output (abridged):

```
[server] server started
[client-1] client 已连接, 本地端口 127.0.0.1:36610
[server] 连接数=1, 新连接: 127.0.0.1:36610
[server] 收到 PING, from:client-1(127.0.0.1:36610), data:"1abc"
[server] 回复 PONG, to:client-1(127.0.0.1:36610)
[client-1] 第 1/3 步: client 收到 version=0x8001 action=PONG len=10 data="pong, 1abc"
...
[client-2] 第 2/3 步: 故意写入 3 字节非法数据 "xxx" ...
[server] 解包失败, 关闭连接: PROTOCOL_VERSION error: got version=0x7878 action=0x7861, want version=0x8001
[client-2] 第 2/3 步: 连接被服务端断开 (read tcp ...: connection reset by peer)
[server] 演示结束: 4 个客户端均已退出 (client-2 是被协议校验主动断开的)
[server] 连接已全部回收, 当前连接数=0
```

Pass criteria:

- clients 1, 3, 4 each print three `client 收到 ... action=PONG` lines with
  `data="pong, Nabc"` for `N = 1,2,3`;
- client-2 gets exactly one PONG (step 1), then is disconnected by the server
  with a `PROTOCOL_VERSION error` whose `got version=0x7878` is the ASCII
  `"xxx"` plus the next header byte — that byte sequence is the demo's point;
- the final counts are 4 accepted connections and `当前连接数=0`.

Because the server logs on the reactor while clients log from four goroutines,
line order between sides is not stable; check per-client sequences, not global
order.

The process does not exit by itself: `gnet.Run` returns only when the engine
stops. Stop it with `Ctrl-C` (SIGINT) — the `Run` wrapper logs
`[server] 收到系统信号:interrupt` and calls `gnet.Stop`. Running it under
`timeout 20 go run ...` is the easiest non-interactive check, but note `timeout`
kills the `go run` wrapper, so the graceful-stop log may not appear.

### websocket

```bash
# terminal 1: ws listener on :8081 + browser page on :8080
go run ./examples/websocket

# terminal 2: scripted client (echo + a frame split across TCP segments +
# concurrent connections); exits non-zero if any connection fails
go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 8 -msgs 5
```

- success on the client: `ok=8 fail=0`, plus one `CONNECT` / `OPEN` / `SEND` /
  `RECV` / `DONE` / `CLOSE` line per connection by default (pass `-v=false`
  for only the summary). It sends one deliberately TCP-segmented frame per
  connection first (`-split=false` to skip), reported as a `SPLIT` line — that
  is the case the old gobwas implementation failed with `short buffer`.
- success in the server log: `[server] ws 协议升级成功, from:...`, then per message
  `[server] 收到消息, ..., type:1, payload:"..."` and
  `[server] echo 收到数据, ...`, and on client close
  `[server] 客户端关闭连接, ..., status:1000`.
- the demo also logs `[server] 当前连接数=N` every 2 seconds through
  `commonutil.GetLogger()`, so it appears as a JSON line like the rest.
- the browser page (`http://localhost:8080/`) auto-connects on load and logs
  `OPEN` / `SEND` / `RECV` / `CLOSE` with timestamps into the output pane, so a
  manual test shows something without opening the devtools console.
- both example flags are configurable: `--ws-port` / `--http-port` on the
  server, `-addr` / `-conns` / `-msgs` on the client, so a smoke test does not
  need the default ports.

A single successful echo is still a weak signal; use `-conns 20 -msgs 5` (and
`go run -race` for the server) when touching the read or write path. Signal
handling for `examples/websocket` is split across two servers (the gnet engine
and the `net/http` page server), so `timeout`/`Ctrl-C` kills the process rather
than draining it — that is the example, not the library.

### grpc and root

```bash
go run ./grpc     # :50051, self-contained client+server, prints goroutine ids
go run .          # protobuf marshal/unmarshal scratch demo, prints two lines
```

Both are reference material; a change there only needs `go build` plus a single
run showing the same output shape.

## What a new test should look like

If you add tests, they belong next to the code (`tcp_fixed_head/*_test.go`) and
must not need a socket or a port. The valuable, socket-free units here are:

- **Frame codec round-trip**: `EncodeData(action, body)` → `DecodeFrame` →
  compare fields; include an empty body and a body at `MaxDataLength`.
- **Header validation**: a table of `(version, action, length)` → expected
  sentinel (`ErrProtocolVersion`, `ErrUnknownAction`, `ErrDataLengthExceeded`).
- **`ActionName`** for all three actions plus an unknown one.
- **`isCorrectAction`** for the boundary values.

`Decode(c gnet.Conn)` needs a `gnet.Conn`; gnet does not ship a fake, so a unit
test there means writing a small stub implementing the interface. If you do
that, implement only the methods the decoder touches (`InboundBuffered`,
`Next`, `Context`, `SetContext`) and panic in the rest, so an accidental
dependency on another method fails loudly. Note gnet v2.10 changed
`SetContext` to `SetContext(any)`/`SetSafeContext(any)`; a stub written against
an older gnet will not compile.

Do not add a test that binds a port and races the demo. If an integration check
is needed, extend the example (it already has a deterministic pass/fail
narrative) rather than adding a second harness.

## Reproducing a reported bug

1. `go build ./... && go vet ./...` first — a compile error is the most common
   cause of "it stopped working".
2. Run the matching example and grep the tags:
   `go run ./examples/tcp_fixed_head 2>&1 | rg 'server]'`.
3. For decode problems, remember the three historical bugs this repo already
   fixed, because a regression looks identical: pipelined frames not looped
   over (responses lag one frame), `Next(0)` swallowing the next frame, and
   `ConnNum` read directly instead of through `GetConnNum()`.
