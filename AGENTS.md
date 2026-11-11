# server_on_gnet

Go library of reusable server implementations built on the **gnet**
(`github.com/panjf2000/gnet/v2`) event-driven networking framework. Module
`github.com/cclehui/server_on_gnet`, Go 1.20 (toolchain here is 1.26). One
package per protocol, plus runnable demos with real test clients.

This file is the short contract. Everything else is split into focused docs —
read the one that matches the task instead of loading all of them.

| Read this | When |
|---|---|
| [harness/coding_style.md](./harness/coding_style.md) | Before writing or reviewing Go code: package layout, file naming, error/log/constant conventions, naming, comments. |
| [harness/knowledge/architecture.md](./harness/knowledge/architecture.md) | Reactor + ants worker-pool model, `OnTraffic` decode loop, per-conn `Conn` context, lifecycle callbacks, graceful shutdown. |
| [harness/knowledge/tcp_fixed_head.md](./harness/knowledge/tcp_fixed_head.md) | Touching the 8-byte-header protocol: wire format, streaming decode state, the three decode traps, handler contract. |
| [harness/knowledge/websocket.md](./harness/knowledge/websocket.md) | Touching the WebSocket server: gobwas/ws usage, upgrader conn adapter, message loop, time-wheel idle eviction. |
| [harness/knowledge/logging.md](./harness/knowledge/logging.md) | Logging through `commonutil.GetLogger()`, the JSON line format, the `[server]` / `[client-N]` tag convention, concurrency. |
| [harness/knowledge/grpc_reference.md](./harness/knowledge/grpc_reference.md) | The `grpc/` and `proto_temp.go` reference code: why it is not gnet-based and where it is allowed to drift. |
| [harness/knowledge/testing.md](./harness/knowledge/testing.md) | Before claiming a change works: what can be verified, how to run the examples, what has no coverage. |
| [docs/architecture.md](./docs/architecture.md) | User-facing design overview (package map + running the demos). Keep it in sync with the knowledge files. |

## Environment

- Go 1.20 in `go.mod`; the local toolchain is newer. Do not bump the `go`
  directive as a side effect of an unrelated change.
- No database, no external service: everything here is a process you start and
  a socket you connect to. `go build ./...` and `go test ./...` are enough to
  prove the tree compiles.
- Dependencies are few and intentional: `gnet/v2`, `ants/v2`, `gobwas/ws`,
  `pkg/errors`; `x/net` only supplies the legacy `context` used by
  `tcp_fixed_head`/`websocket` and by the generated protobuf code; `grpc` +
  `protobuf` exist for the reference demo. Adding one needs a reason.

```bash
# fixed-head TCP demo: server on :7001 and 4 built-in clients (client-2 is
# expected to be disconnected — that is the demo, not a failure)
go run ./examples/tcp_fixed_head --port 7001 --multicore=true

# WebSocket demo: ws server on :8081, browser client page on :8080
go run ./examples/websocket

# grpc keepalive reference demo (self-contained client + server, :50051)
go run ./grpc

# protobuf scratch demo in the repo root
go run .
```

## Commits Require An Explicit Request

Never run `git commit`, `git push`, or `git add` unless the user asks for it in
that same request (e.g. "commit", "commit and push"). Finishing a task or
passing a build is **not** permission to commit.

- Leave changes in the working tree and report the status instead.
- Permission covers exactly one commit; the next task needs its own request.
- Never sweep `AGENTS.md` or `docs/current_agent_tasks.md` into a commit.
- Git read operations (`status`, `diff`, `log`, `show`) are always fine.

## Approval Policy

No approval needed: `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt`,
`goimports`, file inspection (`cat`, `rg`, `find`), `git status/diff/log`, and
running an example on a high port for a few seconds.

Approval required: destructive operations (`rm -rf`, `git reset --hard`),
changes under `$GOPATH`/`$GOMODCACHE`, `go get`/`go mod tidy` that move versions,
binding the default demo ports (`:7001`, `:8080`, `:8081`, `:50051`) for more
than a smoke test, and anything outside the workspace or `/tmp`.

## Verification

```bash
gofmt -l .                 # must print nothing
go vet ./...               # must pass
go build ./...             # must pass
go test ./...              # currently: "no test files" for every package
```

There are **no unit tests**. That is a known gap, not permission to change
behavior blind: for anything non-trivial, run the matching example and read the
log lines it prints (see `harness/knowledge/testing.md`). Say in your final
answer which example you ran and what you observed.

`.golangci.yml` is a copied config, not a working local setup: `golangci-lint`
is not installed here and several of the enabled linters
(`interfacer`, `scopelint`, `maligned`, `deadcode`/`structcheck`/`varcheck`) no
longer exist upstream. Do not "fix" the repo to satisfy linters that cannot be
run; match the existing style instead.

## docs/current_agent_tasks.md Is The User's File

It records the jobs the user is about to ask for, and they edit it while work
is in progress.

- Read it, never write it: no edits, no reformatting, no "DONE" markers, no
  summary appended.
- Never stage or commit it, and never restore it with `git checkout --`.
- Report completion in the chat answer instead.

## Invariants

These are the rules a future change must not quietly break; the linked
knowledge files explain the mechanics.

- **The reactor never runs business logic.** `OnTraffic` decodes; anything
  user-defined goes to the ants pool. No blocking I/O, no `time.Sleep`, no
  handler call inline.
- **Buffers from `Next`/`Read` are borrowed.** Copy before handing data to a
  worker goroutine or storing it past the callback.
- **`Next(n)` with `n <= 0` means "read everything".** Never call it with a
  zero-length or negative length; skip it instead, or the next pipelined frame
  is swallowed.
- **A bad header closes the connection.** Version / action / length are
  validated before any per-conn state is stored, so a corrupt stream can never
  park a connection forever waiting for a body.
- **`Conn` is not safe to use outside the event loop** for the `Reader` /
  `Writer` methods. Writes from a worker go through `AsyncWrite`.
- **Connection counters are atomic.** Mutate with `atomic.AddInt32/Int64`; read
  through `GetConnNum()`, never the field directly.
- **Library packages log through `commonutil.GetLogger()`**, never `fmt` /
  `log`, and tag the side of the conversation (`[server]` / `[client-N]`).
- **Examples stay runnable.** `go run ./examples/...` is the only executable
  documentation this repo has; do not move an example's behavior into a test
  helper it no longer exercises.
