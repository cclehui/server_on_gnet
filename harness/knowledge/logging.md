# Logging

One logger interface, one JSON line format, one prefix. Everything in the
library logs through `commonutil.GetLogger()`.

## Interface

```go
type Logger interface {
    Debugf(ctx context.Context, format string, args ...interface{})
    Infof(ctx context.Context, format string, args ...interface{})
    Warnf(ctx context.Context, format string, args ...interface{})
    Errorf(ctx context.Context, format string, args ...interface{})
}

func GetLogger() Logger            // current logger
func SetLogger(newLogger Logger)   // swap it (not called anywhere today)
```

- Four levels, all `f`-style (`Infof`, not `Info`). There is no `Fatal`: a
  library must not exit the process. The demos use `fmt.Println` for the banner
  and `log.Fatal` only when a listener or `gnet.Run` fails at startup.
- The first parameter is `ctx context.Context` but the default logger
  **ignores it**. It is there so a `HandlerContext` can be passed without
  adaptation, and so a future logger can pick up trace ids. Do not assume a
  value put into the context reaches the log line.
- `SetLogger` is not synchronized. Set it once at startup, before any server
  runs; changing it mid-flight races with in-flight `GetLogger()` calls.

## Output format

`DefaultLogger` writes one JSON object per line to `os.Stdout`:

```json
{"prefix":"SERVER_ON_GNET","time":"2026-11-11 12:10:23","level":"INFO","content":"[server] 收到 PING, from:client-3(127.0.0.1:36594), data:\"1abc\""}
```

- `prefix` is always `SERVER_ON_GNET` (`LogPrefix`); it is filled in by
  `EncodeToStr` when empty.
- `time` is local time formatted `2006-01-02 15:04:05` — **second precision, no
  timezone**. Two events in the same second are not orderable from the log
  alone; correlate with the message text (the peer address, action, step
  number).
- `level` is `DEBUG` / `INFO` / `WARN` / `ERROR` (uppercase).
- `content` is the fully `fmt.Sprintf`-formatted message. Newlines inside it are
  JSON-escaped, so one line stays one line.
- `json.Marshal` never fails for this struct; if it somehow did, the fallback
  prints `{"level":"...","content":"..."}` with `%q` formatting.

Because it is JSON per line, `jq -r .content` works on demo output, and
`rg 'server]'` / `rg 'client-'` still works on the raw text.

## Concurrency

`DefaultLogger` holds a `sync.Mutex` around the `fmt.Println`. That is not
gold-plating: the reactor goroutines, the ants worker pool, and the demo's
counting goroutine all log concurrently, and a single `Write` to stdout is not
guaranteed to be atomic relative to another goroutine's `Write` — without the
mutex two long lines interleave into garbage. Keep the lock; do not replace
`fmt.Println` with `json.NewEncoder(os.Stdout).Encode` "for correctness" (the
encoder still needs the same exclusion) or with an unlocked `fmt.Printf`.

The cost is a process-wide serialization point on every log line. That is fine
for debug logging in a demo; if a hot path ever logs per message at scale, the
fix is to make the logger asynchronous (buffered channel + writer goroutine),
not to remove the lock.

## Tag convention

Both directions of a conversation appear in the same terminal, so every log
line says which side produced it:

| Tag | Produced by |
|---|---|
| `[server] ` | library packages and the demo's server-side handler |
| `[client-N] ` | the demo's client wrapper (`examples/tcp_fixed_head`) |

- The tag is the **first** thing in `content`, before any other text, so
  `^`-anchored filtering works on `.content` and plain substring filtering
  works on the raw JSON line.
- The library always uses the literal `"[server] "` prefix; it never knows
  about clients. The demo builds `"[%s] "` with its client name.
- Untagged lines exist: the gnet framework's own logger prints
  `[gnet] ... INFO logging/logger.go Launching gnet with N event-loops` in a
  different format (tab-separated, RFC3339 timestamp). That is gnet's logger,
  not ours; do not try to reformat it.

## What to log where

- **Decode failure** → `Errorf`, include the observed values (`got version=...`)
  and the peer; the connection is about to be closed, so this is the only
  evidence of why.
- **Frame handled** → `Infof` with `ActionName`, peer, and `%q` payload. This is
  the demo's main narrative; a real service would probably drop it to `Debugf`.
- **Connection open/close** → `Infof` on open with the running count, `Debugf`
  on close.
- **Protocol upgrade** → `Infof` on success, `Errorf` on failure.
- **Handler internal error** (encode failure, write failure) → `Errorf`, then
  return; never panic.

## Removing or adding a logger

`Logger` is an interface with no framework coupling; a `zap`/`logrus` adapter is
a small struct. If you add one, keep the `ctx` parameter and the `f` naming, and
do not change the default — the demos and `docs` describe the JSON line format.
`.golangci.yml` bans direct `sirupsen/logrus` imports outside a log utils
package; the ban is inert today because lint is not run, but do not add logrus
usage to a protocol package.
