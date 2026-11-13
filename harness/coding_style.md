# server_on_gnet Coding Style

Pattern constraints for this repository. It is a small library, not an
application framework: there is no DI container, no config struct, no service
layer. The unit of work is *one protocol package* that implements
`gnet.EventHandler` and exposes a handler function type.

Read this before writing or reviewing Go code here.

## Package and File Layout

| Artifact | Path |
|---|---|
| Shared helpers (logger, no-op codec) | `commonutil/*.go` |
| One protocol server | `<protocol>/` — e.g. `tcp_fixed_head/`, `websocket/` |
| Runnable demo + test client | `examples/<protocol>/` — one `package main` file |
| Reference code (not gnet) | `grpc/`, `proto_temp.go` |
| Design overview (user-facing) | `docs/architecture.md` |

Package names are the protocol, lowercase, with underscores when the protocol
name has parts: `tcp_fixed_head`, `websocket`. Do **not** invent a `pkg/`,
`internal/`, or `server/` wrapper — a new protocol is a top-level directory.

Files inside a protocol package follow a fixed set, in this order of
responsibility:

| File | Holds |
|---|---|
| `config.go` | tiny pure helpers (`ActionName`, `isCorrectAction`) |
| `constants.go` | the `const` block (pool size, frame limits, timeouts, header keys) |
| `exceptions.go` | the sentinel `Err*` values (`websocket.ErrNotUpgraded`, `tcp_fixed_head.Err*`) |
| `option.go` | the `ServerOption` func type + `With*` constructors; the constructor applies these before defaults |
| `protocal.go` / `server.go` | frame decode/encode; `server.go` holds the server struct **first**, then its `gnet.EventHandler` methods |
| `server_factory.go` | `New<Type>Server` constructor: apply options, then fill defaults (pool, ctx, protocol) |
| `handler.go` / `data_handler.go` | the exported handler func **type** + default implementation |
| `handler_context.go` | the per-message context struct handed to the handler |
| `timewheel.go` | the idle-eviction timer, only where a protocol needs one |
| `html_template.go` | the browser test page template (`html/template`), WebSocket only |

`tcp_fixed_head/protocal.go` is misspelled ("protocol"). Keep the name for now —
renaming it is churn in a repo with no tests and moves every `git blame` line.
If you do rename it, do it deliberately in its own change, not folded into a
behavior fix.

`examples/<protocol>/` is a single `main` file that starts the server *and* the
test clients, because running it is how the protocol is verified. Do not split
the demo across files; a reader should get the whole scenario from one screen.

### Two `main` packages, on purpose

`websocket/html_template.go` lives inside package `websocket` and exports
`ClientTemplate` instead of being a `main` — the demo's HTTP page needs it, and
a `main` package cannot be imported. Conversely `grpc/` and the repo root are
`main` packages because they are demos. `grpc/grpc_temp_client.go` is just
`package main` with no declarations: it keeps the file-name pair
(client/server) visible, and `go build ./...` folds it into the same package as
`grpc_temp_server.go`.

## Server Shape

Every protocol package exposes exactly this surface:

```go
// Constructor: applies opts, then fills in defaults (pool, handler, ctx).
func NewTCPFixHeadServer(opts ...ServerOption) *TCPFixHeadServer

// Runner: starts gnet and installs the signal-driven graceful shutdown.
// Listen address and gnet options are server config, set by options.
func (tcphs *TCPFixHeadServer) Run() error

// Business entry point: a func type, not an interface.
type ServerHandler func(ctx *HandlerContext)

// Default business logic, unexported; a demo replaces server.Handler.
var defaultHandler ServerHandler = func(ctx *HandlerContext) { ... }
```

- **Constructor does wiring only.** Apply `opts ...ServerOption` in order, then
  fill defaults (handler, `context.Background()`, ants pool, protocol), return
  the pointer. No listening, no goroutines. It lives in `server_factory.go`.
- **Options are `func(*Server)` values in `option.go`**, not a config struct. Both
  `websocket` and `tcp_fixed_head` expose them (`WithHandler`, `WithWorkerPool`,
  `WithContext`, plus package-specific knobs).
  Each `WithX` validates its argument and panics on nil/invalid input, so a bad
  wiring choice fails at construction instead of on the first connection; nil
  never means "use the default". Order in the constructor is options first,
  defaults second: every default is filled in only when the field is still
  unset, and anything derived from a knob (the time wheel's slot count) is
  built after that. This keeps allocating defaults (the ants pool) from being
  created just to be overwritten.
- **`Run` is a method on the server and takes no address** (`tcp_fixed_head`;
  the `websocket` demo still calls `gnet.Run` directly). The constructor is the
  only place to set knobs, including `WithProtoAddr` / `WithGnetOptions`; `Run`
  returns the `gnet.Run` error so `main` decides whether that is fatal. The
  listen address is the exported `ProtoAddr` field, not an int port — a bare
  port cannot express host/scheme — and it defaults to the package's
  `DefaultProtoAddr`.
- **The server struct is mutable at construction time and read-only after
  `Run`.** Exported knobs (`Port`, `Addr`, `WorkerPool`, `Handler`,
  `ConnCloseHandler`) are set through `With*` options before `Run`; internal
  state (`ctx`, `protocol`, `connTimeWheel`) is unexported and never touched
  from outside.
- **A handler is a `func` type with one struct parameter.** Adding a field to
  the request means adding it to the param struct (`HandlerContext`,
  `DataHandlerParam`), not widening the function signature — existing users
  keep compiling.
- **Default handler is a package-level `var`, not a method on the server.** The
  demo swaps `server.Handler = demoHandler`; there is no registration API.
- Do not introduce an `interface { Handle(...) }` abstraction. The func type is
  the extension point; a second one would only add indirection.

## Worker Pool

```go
options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
pool, _ := ants.NewPool(DEFAULT_ANTS_POOL_SIZE, ants.WithOptions(options)) // websocket
```

- The pool size is `1024 * 1024` and is declared **per protocol package**, not
  shared: `tcp_fixed_head.DefaultAntsPoolSize`, or
  `websocket.DEFAULT_ANTS_POOL_SIZE` in `websocket/constants.go`. Keep it that
  way: each protocol owns its pool and its own tuning.
- The constructor ignores the pool error. That is deliberate (the arguments are
  static and valid); do not add an `error` return just for this.
- `Nonblocking: true` is what makes `Submit` safe on the reactor: when the pool
  is saturated the task is dropped instead of stalling the event loop. If you
  need backpressure, that is a protocol decision to be made explicitly.
- `Submit`'s error is currently ignored in `OnTraffic`. Any change there must
  log through `commonutil.GetLogger()` and say which connection and which
  action was dropped.

## Error Conventions

Two error styles, with a hard boundary:

```go
// Sentinels: standard library, package-level, compared with == or errors.Is.
// pkg/errors.New captures a stack at package init, so a sentinel printed in a
// log shows init's stack instead of the failure site — useless.
var ErrIncompletePacket = errors.New("incomplete packet") // std "errors"

// Everything constructed elsewhere: pkg/errors, so Wrap/Wrapf carry a stack.
return errors.Wrap(err, "read frame head")
return errors.Errorf("read data error, %v", err)
```

- Sentinel names are `Err<What>` and are **never** re-created per call — a
  fresh `errors.New` per call breaks `errors.Is`.
- `ErrIncompletePacket` is control flow, not failure: `OnTraffic` compares it
  with `==` and returns `gnet.None`. Every other decode error closes the
  connection. A new "wait for more data" condition must reuse that sentinel.
- Protocol errors wrap the sentinel and add the observed values:
  `fmt.Errorf("%w: got version=0x%04X action=0x%04X, want version=0x%04X", ...)`.
  A bare "PROTOCOL_VERSION error" cannot distinguish a bad peer from a stream
  that has already lost sync.
- Do not use `errors.New(fmt.Sprintf(...))` in new code — it is the one wart in
  `protocal.go`. Use `pkg/errors.Errorf` for a formatted message.
- Handler-side failures are logged, never returned: a `ServerHandler` has no
  error channel, and a business error must not take down the reactor.

## Logging

- Always `commonutil.GetLogger().<Level>f(ctx, ...)`. Library packages must not
  use `fmt.Print*` or the standard `log` package; that is how the JSON line
  format and the log prefix stay uniform.
- Pass the context you already have: `HandlerContext` implements
  `context.Context`, so log calls inside a TCP handler use it directly.
  `server.ctx` is for lifecycle callbacks. (`websocket` logs with
  `context.Background()`; match the file you are editing.)
- Tag the side of the conversation: library server logs start with
  `"[server] "`, demo client logs with `"[client-N] "` (built by the demo, not
  the library). That is what makes `grep 'server]'` / `grep 'client-'` work.
- Use `%q` for payload bytes and `%+v` for errors; use `ActionName()` instead
  of printing a raw `uint16` action.
- No debug `fmt.Printf` left behind. If a log is only useful while developing,
  put it at `Debugf`.
- The default logger serializes writes with a mutex because the reactor, the
  worker pool, and demo goroutines all log concurrently; one `Write` to stdout
  is not atomic across a long line.

## Constants

- One `const` block per file, at the top, with short doc comments.
- Naming differs per protocol: `tcp_fixed_head` uses `UPPER_SNAKE` only for
  wire-level identifiers (`PROTOCOL_VERSION`, `ACTION_PING`, `ACTION_PONG`,
  `ACTION_DATA`) and mixed-case for tunables (`DefaultAntsPoolSize`,
  `MaxDataLength`). `websocket` keeps every constant in `constants.go` and
  names all of them `UPPER_SNAKE` (`DEFAULT_ANTS_POOL_SIZE`,
  `CONN_MAX_IDLE_SECONDS`, `MAX_MESSAGE_BYTES`, `HANDSHAKE_TIMEOUT`,
  `SEND_TIMEOUT`, `HANDSHAKE_BUFFER_SIZE`, `GO_VERSION_HEADER_KEY`). Follow the
  file you are editing; do not rename one protocol's constants to match the
  other as a drive-by change.
- Values only the package uses stay unexported (in `websocket` the
  package-private `HANDSHAKE_BUFFER_SIZE` and `GO_VERSION_HEADER_KEY` are still
  `UPPER_SNAKE` because that package names all of its constants that way — do
  not export them just for the capitalization). Delete a constant when nothing
  reads it instead of keeping it "for later".
  Do not add a `sync.Pool` for something that is not on a hot path with a
  measured allocation problem.
- Time values are typed: `int64` seconds where the name says seconds
  (`CONN_MAX_IDLE_SECONDS` in `websocket`, `ConnMaxIdleSeconds` in
  `tcp_fixed_head`), `time.Duration` once it is used as a duration
  (`HANDSHAKE_TIMEOUT`, `SEND_TIMEOUT`).
- Duplicating a value across two protocol packages (pool size, an idle timeout)
  is acceptable; hoisting it into `commonutil` is only worth it when both
  packages must change together.

## Naming and Receivers

- Receivers are short and stable per type: `tcphs` for `TCPFixHeadServer`,
  `server` for `WebSocketServer`, `tcpfhp` for `TCPFixHeadProtocol`, `tw` for
  `TimeWheel`, `u` for `GnetUpgraderConn`.
- Constructors are `New<Type>` and return the concrete pointer, never an
  interface. `NewEchoServer` is the special case that also installs
  `EchoDataHandler`.
- One function per concern, named after what it does: `Decode` (server-side
  streaming), `DecodeFrame` (already-split frame), `ClientDecode` (blocking
  client read), `EncodeData` (frame → bytes), `EncodeWrite` (frame →
  `io.Writer`). Do not collapse them; each has a different input shape and a
  different blocking behavior.
- Acronyms keep Go style: `UniqId`, `ProtocolData`, `connTimeWheel`. Match the
  file you are editing rather than introducing a second scheme.

## Formatting

`gofmt` is the formatter; `goimports` groups imports as stdlib / third-party /
this module, with a blank line between groups. The existing files follow that
split. Long signatures and long log calls are wrapped one argument per line
when they overflow ~140 columns (the `lll` linter's setting).

## Comments

Comments in this repo are mostly Chinese and explain **why**, not what. Keep
that voice when editing an existing block: a comment should record the trap
that was hit (the `Next(n<=0)` behavior, the buffer-reuse copy, the 32-bit
`uint32 → int` overflow), because those are exactly the lines a future reader
would "simplify" back into a bug.

Do not add comments that restate the code (`// 增加连接数`). Do not translate an
existing Chinese comment into English as a drive-by change.
