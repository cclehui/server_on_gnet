# MQTT 3.1.1 broker (QoS 0)

`mqtt/` is a broker, not a client. It implements MQTT 3.1.1 with **QoS 0 only**
and the state that survives a single connection: subscriptions, retained
messages, will messages, keepalive.

## Library boundary: paho owns the fields, this package owns the framing

Packet fields are decoded and encoded with
`github.com/eclipse/paho.mqtt.golang/packets` (v1.5.1). Do not hand-roll field
parsing again — `mqtt/protocol.go` only does what paho cannot do here:

| Step | Owner | Why |
|---|---|---|
| Find frame boundaries in a gnet buffer | `mqtt/protocol.go` | paho only offers blocking `ReadPacket(io.Reader)`; gnet's reactor is non-blocking |
| Field decode (`ConnectPacket`, `PublishPacket`, …) | paho `packets` | battle-tested against real clients |
| Field encode (`Connack`, `Suback`, `Publish`, …) | paho `packets` (via `mqtt/encoder.go`) | cannot drift from paho's own parser |
| Server-side policy (QoS>0, flags, direction) | `mqtt/protocol.go` | paho is a client library and accepts what a server must reject |

Two paho behaviours need compensating, and both are easy to "simplify" away:

- **`decodeBytes` (`packets/packets.go:317`) is a short-read.** It calls
  `b.Read(field)` and only checks `err`, so a declared length longer than the
  body yields a silently truncated topic/clientId. `strictReader` requires every
  read to fill its buffer and turns that into `io.ErrUnexpectedEOF`.
- **`Unpack` never reports leftover body bytes.** A frame whose declared length
  exceeds its fields is accepted. `convertPacket` compares the reader's consumed
  count against the body length and errors with `ErrTrailingBytes`.

paho's fixed header only exposes `Dup`/`Qos`/`Retain`, which is the same three
bits for every packet type. The low-nibble rules (0 for most types, `0b0010` for
PUBREL/SUBSCRIBE/UNSUBSCRIBE) are therefore re-derived in
`validateFixedHeaderFlags`. Direction checks (rejecting client-sent CONNACK /
SUBACK / PINGRESP) run *before* `Unpack` so the error names the real problem
instead of whatever `Unpack` tripped on first.

Do not call `packets.ReadPacket` on a gnet `Conn`: `Conn.Read` is
event-loop-only and its `SetDeadline` returns `ErrUnsupportedOp`, so it would
block the reactor. The demo's clients use the full `paho.Client`, which owns its
own connection.

## Wire format and the one framing trap

```
byte0: packet type (high 4 bits) | flags (low 4 bits)
byte1..: remaining length  — varint, 7 bits per byte, high bit = continuation
body:   type-dependent; PUBLISH = topic (2-byte length + UTF-8) + payload
```

The header length is **not** fixed: the varint is 1–4 bytes, so you cannot know
the frame size from a fixed offset. `Decode` therefore does two passes:

1. `decodeRemainingLength` **Peeks** bytes one at a time to read the varint. If
   the varint itself is not complete yet, it returns `ErrIncompletePacket`
   without consuming anything.
2. Only when `InboundBuffered() >= 1 + varintLen + remaining` does it call
   `Next(total)` and consume the whole frame.

Consuming first and parsing later is the bug this shape prevents: a half-arrived
frame would be eaten, and the next `OnTraffic` would parse a mid-frame byte as a
packet type. `Next(n)` with `n <= 0` still means "read everything" (the repo-wide
gnet trap) — that is why the early-return checks come before any `Next`.

## Where the packet state machine runs

Unlike `tcp_fixed_head`/`websocket`, packet handling is **not** submitted to the
ants pool. `OnTraffic` decodes and then calls `handlePacket` inline in the
reactor. Reasons:

- CONNECT / SUBSCRIBE / PUBLISH on one connection are strictly ordered, and ants
  preserves no submission order. Subscribing the protocol path would let a
  pipelined SUBSCRIBE run before its own CONNECT and tear the connection down.
- The work is bounded and non-blocking: subscription-tree matching, map lookups,
  and `AsyncWrite` enqueueing. No I/O, no sleeping.

Only the user-defined `ServerHandler` is submitted to the worker pool, and only
for PUBLISH. `Submit` errors (non-blocking pool, saturated) are logged as
`worker 池已满` instead of being dropped silently.

## Cross-connection state (three independent locks)

| State | Type | Guard |
|---|---|---|
| topic filter → clients | `*SubscriptionTree` | `sync.RWMutex` inside the tree |
| client id → connection | `*SessionRegistry` | `sync.RWMutex` |
| retained messages | `retainedStore` | `sync.RWMutex` |

With `WithMulticore(true)` each event loop is its own reactor goroutine, so all
three are genuinely concurrent — the locks are required, not decoration. Never
hold one across an `AsyncWrite`; the write is queued and failure is reported to
its callback.

`Session` fields split by ownership: `lastActiveMillis`/`closed` are atomics
(reactor writes, maintenance goroutine reads); the will message is behind
`willMu`; subscription state is never stored on the session — it lives only in
the tree, keyed by client id.

## Delivery rules that are easy to get wrong

- **RETAIN on live delivery is 0.** `publish(..., retain)` uses `retain` only to
  update the retained store. Messages forwarded to already-connected subscribers
  are encoded with RETAIN=0; only the replay performed inside
  `handleSubscribe` sets RETAIN=1 (spec 3.3.1.3).
- **Retain with an empty payload deletes.** `retained.Set(topic, nil)` removes
  the entry; it does not store an empty retained message.
- **Retained replay is de-duplicated by topic.** One SUBSCRIBE can carry several
  filters (`a/#` and `a/b`) that match the same retained topic.
- **QoS 0 echoes back to the publisher** if it subscribed to its own topic.
- **`+` and `#` do not match a leading `$`.** `#` must not deliver `$SYS/...`.
- **`sport/#` matches `sport` itself.** The one place a wildcard matches zero
  levels.
- **Empty topic levels are real levels.** `a//b` is three levels; split with
  `strings.Split`, never a function that collapses empties.
- **Requesting QoS 1/2 in SUBSCRIBE is granted as QoS 0**, and SUBACK says
  `0x00`. Publishing with QoS > 0 is different: the broker cannot answer with
  PUBACK, so the connection is closed rather than the message dropped.
- **cleanSession=1 must drop the previous subscriptions** for that client id, or
  the new connection receives topics it never subscribed to.
- **Will fires on every non-DISCONNECT close**, including keepalive expiry and
  client-id takeover. Both of those paths have already removed the session from
  the registry, so the will must not be gated on `Remove` returning true.
- **Client-id takeover closes the old connection**; `SessionRegistry.Add`
  returns the replaced session so `OnClose` does not delete the new one's
  subscriptions (`Remove` only deletes when the registered pointer matches).

## Not implemented (deliberate, and documented at the API)

- QoS 1/2 and their PUBACK/PUBREC/PUBREL/PUBCOMP flows.
- Offline sessions and queued messages. `cleanSession=0` is accepted (disable
  with `RetainSession(false)`) but state is process-lifetime only — there is no
  database in this repo. CONNACK always reports `sessionPresent=0`.
- MQTT 5 and 3.1 (`MQIsdp`/level 3): CONNACK `0x01`.
- TLS and ACLs. Username/password are parsed and exposed on the packet; nothing
  validates them. Do not expose the broker to an untrusted network as-is.

## Timeouts

- `ConnectTimeout` (10s) — a connection that never sends CONNECT is closed by
  the maintenance goroutine; otherwise it occupies a reactor-held connection
  indefinitely.
- Keepalive — any complete control packet refreshes the timer;
  expiry is 1.5 × keepalive, checked once per `MaintenanceInterval` (1s), so the
  real cutoff is up to 1s late.
- Kept in a plain goroutine + `time.Ticker`, not `OnTick`: the servers here never
  pass `WithTicker(true)`, so `OnTick` is dead code everywhere in this repo.

## Verification

```bash
go run ./examples/mqtt --port 11883   # broker + 4 scripted clients
```

Expected, per client: `client-a` receives `demo/room1/temp` but not
`demo/room2/humidity`, and receives its own `self-echo`; `client-d` receives the
retained `demo/status=online` although it subscribed afterwards; `client-c` is
disconnected with `qos > 0 not supported`. The final summary prints
`conns=0 sessions=0`.

When touching framing, locks, or the will/retain paths, run the server under
`-race` and drive it with concurrent clients; the reactor-inline design and the
three shared maps are the parts most likely to regress.

## Reading a connection's address

`gnet.Conn.RemoteAddr()` is event-loop-only, and reading it after the connection
has been released races with that release. `-race` caught exactly this on the
client-id-takeover path. Consequence: `Session.remoteAddr` and
`preAuthConn.remoteAddr` are string snapshots taken in `OnOpen` /
`handleConnect`, and every log line about a peer uses those, never the live
`Conn`. Do the same for any new field that outlives a callback.
