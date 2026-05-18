## ADDED Requirements

### Requirement: WebSocket Connection Lifecycle

The project SHALL expose a singleton `realtimeClient` in `src/services/ws.ts` that manages a single `WebSocket` connection to `VITE_WS_URL` with token-based auth (token appended as `?token=<jwt>` query param at connect time, matching `docs/ARCHITECTURE.md §5.2`).

The client SHALL:
- connect lazily on the first `subscribe()` call after app mount,
- automatically reconnect with exponential backoff (base 1s, factor 2, cap 30s, full jitter) on unexpected close,
- close with code 1000 on explicit `client.close()`,
- treat 4001 as "auth expired" and notify the auth store to clear the token (no auto-reconnect on 4001).

A `getConnectionState()` method MUST return one of `"idle" | "connecting" | "open" | "reconnecting" | "closed"`.

#### Scenario: Reconnect after unexpected close
- **WHEN** the underlying WebSocket closes with code 1006 (abnormal)
- **THEN** the client MUST transition to `"reconnecting"` and retry connection after `~1s` (with jitter), then `~2s`, capped at 30s

#### Scenario: 4001 close clears auth
- **WHEN** the underlying WebSocket closes with code 4001
- **THEN** the auth store token MUST be cleared, the router MUST navigate to `/login`, and the client MUST transition to `"closed"` without retrying

#### Scenario: Lazy connection
- **WHEN** the app mounts but no component calls `subscribe()`
- **THEN** no WebSocket connection MUST be opened

### Requirement: Topic Subscription API

The client SHALL expose `subscribe(topic, handler): () => void` where `topic` is a string of the form `task:<id>` or `version:<id>`, `handler` receives `{topic, kind, seq, ts, payload}` events, and the returned function unsubscribes.

Internally the client MUST:
- send a `{op:"subscribe", topics:[topic]}` frame upon the first subscription to a given topic,
- coalesce overlapping subscriptions (same topic, multiple handlers) into one server-side subscription,
- send `{op:"unsubscribe", topics:[topic]}` when the last handler for a topic detaches,
- re-send all currently-subscribed topics in a single `subscribe` frame after each reconnect.

#### Scenario: Multiple subscribers coalesce
- **WHEN** two components call `subscribe("task:t1", h1)` and `subscribe("task:t1", h2)`
- **THEN** exactly one `{op:"subscribe", topics:["task:t1"]}` frame MUST be sent to the server, AND both `h1` and `h2` MUST receive every event arriving on `task:t1`

#### Scenario: Reconnect re-subscribes
- **WHEN** the client reconnects with active subscriptions to `task:t1` and `version:v1`
- **THEN** a single `{op:"subscribe", topics:["task:t1","version:v1"]}` frame MUST be sent on the new connection before any handler receives events

#### Scenario: Unsubscribe of last handler closes server subscription
- **WHEN** the only handler for `task:t1` calls the unsubscribe function returned by `subscribe`
- **THEN** an `{op:"unsubscribe", topics:["task:t1"]}` frame MUST be sent

### Requirement: Sequence-Based Deduplication and Gap Detection

For each subscribed topic, the client SHALL track the highest `seq` it has delivered to handlers. Incoming events with `seq <= last_delivered_seq` for that topic MUST be silently dropped (idempotent replay). Incoming events whose `seq` exceeds `last_delivered_seq + 1` indicate a gap; the client MUST invoke a configured gap-fill callback (`onGap(topic, fromSeq, toSeq)`) so the app layer can fetch the missing events via REST (`GET /versions/{id}/events?after_id=...`).

The default `onGap` implementation in the scaffold MUST log at warn and emit `realtime_gap_total` (a counter exposed via a debug hook, not Prometheus — frontend-only).

#### Scenario: Duplicate seq is dropped
- **WHEN** a handler has already received `seq=42` for `task:t1` and the server delivers `seq=42` again
- **THEN** the handler MUST NOT be invoked a second time

#### Scenario: Gap triggers onGap
- **WHEN** the handler has received `seq=10` for `task:t1` and the next incoming event has `seq=13`
- **THEN** `onGap("task:t1", 11, 12)` MUST be invoked before the handler is called with `seq=13`

### Requirement: Heartbeat and Idle Close

The client SHALL send a `{op:"ping"}` frame every 25 seconds. If no message (event or pong) is received within 60 seconds, the client MUST close the underlying socket with code 1000 and enter the reconnect path. When the app moves to the background (`document.visibilityState === "hidden"`) for more than 5 minutes with no active subscriptions, the client MUST close the socket to conserve resources; on returning to foreground with an active subscriber it MUST reconnect.

#### Scenario: Stale connection is closed
- **WHEN** 60 seconds elapse with no inbound frame after the last ping
- **THEN** the client MUST close the socket and transition to `"reconnecting"`

#### Scenario: Idle background close
- **WHEN** the document is hidden for more than 5 minutes and there are no active subscriptions
- **THEN** the socket MUST be closed; on the next `subscribe()` call after the document is visible, a new connection MUST be opened

### Requirement: Integration Test via MSW WS Mock

The scaffold MUST include a Vitest integration test that exercises connect → subscribe → receive → dedup → gap-fill → reconnect → re-subscribe paths against an in-memory WS mock (via `msw` WebSocket handlers or an equivalent local server). The test MUST run in CI without external services.

#### Scenario: End-to-end scaffold test passes
- **WHEN** the realtime scaffold test runs in CI
- **THEN** all assertions on connect, subscribe, dedup, gap detection, and re-subscribe-on-reconnect MUST pass within 10 seconds
