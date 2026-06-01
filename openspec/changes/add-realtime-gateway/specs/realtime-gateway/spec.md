## ADDED Requirements

### Requirement: WebSocket Endpoint and Connection Lifecycle

The API SHALL expose `GET /api/v1/ws` as a WebSocket endpoint matching the wire contract the `web-realtime-client` capability already targets. The connection URL carries the auth token as a `?token=<jwt>` query parameter. A connection opened with a missing or empty `token` MUST be closed with WebSocket close code `4001` (the client's "auth expired" signal) and MUST NOT be added to the connection set. Until real JWT extraction lands, caller identity resolves through the existing auth stub (`DevTenantID` / `DevUserID`), consistent with every REST handler.

The `4001` check gates only the *presence* of a token — it is NOT an authorization boundary. The actual access control is the subscribe-time ownership check (see "Owner-Scoped Subscriptions"): a connection only ever receives events for topics it both owns and subscribed to. Real per-user isolation arrives with a future auth change; today every connection resolves to the same stub identity.

The endpoint participates in the standard request-id / tracing middleware but does NOT use the `{code, message, data, trace_id}` REST envelope — WS frames have their own shape.

#### Scenario: Connect without token is rejected
- **WHEN** a client opens `GET /api/v1/ws` with no `token` query parameter (or an empty one)
- **THEN** the server MUST close the WebSocket with code `4001` and MUST NOT register the connection or deliver any event

#### Scenario: Connect with token registers the connection
- **WHEN** a client opens `GET /api/v1/ws?token=<non-empty>`
- **THEN** the WebSocket handshake MUST succeed and the connection MUST be tracked with the resolved caller identity, with zero topic subscriptions until the client sends a `subscribe` frame

### Requirement: Handshake Origin Validation

The WebSocket handshake SHALL validate the `Origin` header against a configurable allowlist (the SPA origin(s)). Because the token rides in the query string rather than a custom header, a browser would attach it to a cross-site WS open; rejecting disallowed origins closes the cross-site-WebSocket-hijacking (CSWSH) vector. A handshake whose `Origin` is not allowed MUST be rejected at upgrade time. The allowed origins MUST be operator-configurable (env), with the dev SPA origin documented.

#### Scenario: Cross-origin handshake is rejected
- **WHEN** a WebSocket upgrade arrives with an `Origin` not in the configured allowlist
- **THEN** the handshake MUST be rejected (no connection registered, no events delivered)

#### Scenario: Same-origin / allowlisted handshake succeeds
- **WHEN** the upgrade `Origin` matches the configured allowlist (or the request is same-origin)
- **THEN** the handshake MUST proceed to the token check

### Requirement: Subscription Protocol

The gateway SHALL accept client→server text frames `{op:"subscribe", topics:[...]}`, `{op:"unsubscribe", topics:[...]}`, and `{op:"ping"}`. Topics are strings of the form `task:<uuid>` or `version:<uuid>`. A `subscribe` adds each authorized topic to the connection's subscription set (a per-connection set keyed by topic — subscribing to an already-subscribed topic is idempotent and MUST NOT double-count subscriptions or double-deliver). `unsubscribe` removes them. `ping` is a liveness signal that MUST NOT alter subscriptions.

In response to `{op:"ping"}` the gateway MUST send an **application-level** `{op:"pong"}` text frame (not merely a protocol-level pong). The browser `WebSocket` API does not surface protocol pongs to `onmessage`, so the client's inbound-liveness timer would not reset on an idle connection and the client would needlessly reconnect; an app-level text frame keeps idle connections alive.

The server→client event frame shape MUST be exactly `{topic, kind, seq, ts, payload}` where `kind ∈ {status, log, step, artifact, error}`, `seq` is the event's monotonic sequence, `ts` is an RFC3339 UTC timestamp, and `payload` is the event body verbatim.

An unrecognized `op`, or a topic that is not a well-formed `task:<uuid>` / `version:<uuid>`, elicits an `error`-kind frame and MUST NOT change the subscription set or drop the connection. Error frames are diagnostic and primarily surface in server logs/metrics: the current web client ignores any frame that is not a well-formed event (it requires `topic`+`kind`+`seq`+`ts`), so a malformed-topic error is not rendered in the UI — it MUST still be logged/counted server-side.

#### Scenario: Subscribe then receive matching events
- **GIVEN** a connection that has sent `{op:"subscribe", topics:["task:T1"]}` for an owned task `T1`
- **WHEN** an event for `T1` arrives on the fan-out consumer
- **THEN** the connection MUST receive one `{topic:"task:T1", kind, seq, ts, payload}` frame, and a connection NOT subscribed to `task:T1` MUST NOT receive it

#### Scenario: Duplicate subscribe is idempotent
- **GIVEN** a connection already subscribed to `task:T1`
- **WHEN** it sends `{op:"subscribe", topics:["task:T1"]}` again (e.g. the client re-sends its full topic set after reconnect)
- **THEN** the subscription set and the active-subscriptions gauge MUST NOT double-count, and each `T1` event MUST still be delivered exactly once for the `task:T1` topic

#### Scenario: Unsubscribe stops delivery
- **GIVEN** a connection subscribed to `task:T1`
- **WHEN** it sends `{op:"unsubscribe", topics:["task:T1"]}` and a later `T1` event arrives
- **THEN** the connection MUST NOT receive that event

#### Scenario: Ping elicits an app-level pong and changes nothing
- **WHEN** a connection sends `{op:"ping"}`
- **THEN** the server MUST send an `{op:"pong"}` text frame and the connection's subscription set MUST be unchanged

#### Scenario: Malformed topic or op is a soft, logged error
- **WHEN** a connection sends `{op:"subscribe", topics:["garbage"]}` or `{op:"bogus"}`
- **THEN** the server MUST send an `error`-kind frame, MUST NOT add any subscription, MUST keep the connection open, and MUST log/count the rejection server-side

### Requirement: Owner-Scoped Subscriptions

A `subscribe` to a `task:<id>` / `version:<id>` topic MUST be authorized against the caller's `(tenant_id, user_id)` through an application-layer ownership port (the gateway MUST NOT import `domain/task` directly — layering per AGENTS.md §4.1). The port exposes task and version ownership checks that return the existing `ErrTaskNotFound` / `ErrVersionNotFound` sentinels so that "not found" and "not owned" are indistinguishable. A topic whose task/version does not exist OR is owned by a different caller MUST NOT be added to the subscription set, MUST elicit an `error`-kind frame, and MUST NOT reveal whether the resource exists. A connection MUST never receive events for a topic it is not authorized for, even if such events flow through the fan-out consumer.

Ownership is resolved ONCE per topic at subscribe time, not per event — ownership of a task/version does not change in the MVP (no transfer), so the fan-out hot path trusts the subscription set with no per-event DB lookup.

#### Scenario: Subscribing to an unowned task is denied
- **GIVEN** a task `T2` owned by a different user
- **WHEN** the connection sends `{op:"subscribe", topics:["task:T2"]}`
- **THEN** the server MUST send an `error`-kind frame, MUST NOT add `task:T2` to the subscription set, and MUST NOT deliver any `T2` event — regardless of whether `T2` exists

#### Scenario: Events for unauthorized topics are never delivered
- **GIVEN** the fan-out consumer receives an event for a task the connection does not own and is not subscribed to
- **THEN** that connection MUST NOT receive the event

### Requirement: Per-Instance Event Fan-Out

Each gateway process SHALL consume task events for fan-out via its OWN exclusive, auto-delete, non-durable queue bound to the existing `task.events` topic exchange. The worker publishes events with routing key `event.<task_type>.<kind>` (3 segments; ARCHITECTURE §5.3), so the binding MUST be `event.#` (a `#` wildcard — `event.*` would NOT match a 3-segment key). This is independent of the shared, durable `q.task.events` work queue used for DB ingest: every gateway instance receives every event (no competing consumption), and the ingest path is unaffected. The gateway MUST NOT write to any database table — it only reads from the exchange and pushes to sockets.

Because the queue is exclusive/auto-delete it is bound to the AMQP *connection* and is destroyed if that connection drops. The fan-out consumer MUST therefore **re-declare and re-bind** its queue on every (re)connect — not merely re-`Consume` — so delivery resumes after a connection blip (a deliberate divergence from the existing durable-queue `Consumer`, which only re-subscribes).

For each delivery, the gateway decodes the worker event envelope, derives the topics `task:<task_id>` and `version:<version_id>`, and delivers `{topic, kind, seq, ts, payload}` to every locally-connected, authorized subscriber of either topic. `ts` MUST be the authoritative `ts` the worker stamped on the event (the worker `TaskEvent` carries `ts`; the API's existing ingest decoder happens to drop it, so the gateway's decoder MUST include it); if a delivery lacks a parseable `ts`, the gateway MAY fall back to its receive time (clients order by `seq`, so `ts` is display-only). A delivery with an undecodable envelope MUST be dropped (counted) and MUST NOT drop any connection.

A connection subscribed to BOTH the task and version topic of the same event MUST receive TWO frames (one per topic) — this is intended; the client dedups per topic, so the two frames (different `topic`) both surface.

#### Scenario: One event fans out to both task and version subscribers
- **GIVEN** connection A subscribed to `task:T1` and connection B subscribed to `version:V1`, where the next event has `task_id=T1` and `version_id=V1`
- **WHEN** that event is consumed from the fan-out queue
- **THEN** A MUST receive a `task:T1` frame and B MUST receive a `version:V1` frame, each carrying the same `seq`, `kind`, `payload`, and the worker-stamped `ts`

#### Scenario: Fan-out queue is independent of DB ingest
- **WHEN** a gateway instance starts and binds its fan-out queue
- **THEN** the shared `q.task.events` ingest consumer MUST continue to receive and persist every event (the fan-out queue does not steal deliveries), and the gateway MUST NOT write to `task_events` or any other table

#### Scenario: Fan-out queue is re-declared after a connection drop
- **GIVEN** a running gateway whose AMQP connection drops and reconnects
- **THEN** the fan-out consumer MUST re-declare its exclusive queue and re-bind `event.#` on the new connection, and event delivery MUST resume without manual intervention

#### Scenario: Undecodable delivery does not break the connection
- **WHEN** the fan-out consumer receives a message whose body is not a valid event envelope
- **THEN** the gateway MUST drop the message (incrementing a drop counter) and MUST keep all connections open

### Requirement: Resource Limits

The gateway SHALL bound per-connection resource use to prevent abuse: an explicit inbound read limit on each socket, and a cap on the number of topics a single connection may subscribe to. Because each subscribe triggers one ownership probe (a DB call) per topic, a `subscribe` frame's `topics` array MUST also be bounded so a single frame cannot fan out into an unbounded number of DB probes. Exceeding a limit MUST elicit an `error`-kind frame (and, for the read limit, MAY close the connection); it MUST NOT panic or exhaust memory.

#### Scenario: Oversized subscribe is rejected
- **WHEN** a connection sends a `subscribe` frame whose `topics` array exceeds the configured cap (or would push the connection past its topic limit)
- **THEN** the server MUST reject it with an `error`-kind frame and MUST NOT run an unbounded number of ownership probes

### Requirement: Backpressure and Slow-Client Eviction

Each connection MUST have a bounded outbound buffer. When a connection cannot keep up (buffer full), the gateway MUST evict it — close the socket (server-initiated) rather than block the fan-out path or buffer without bound. The web client recovers by reconnecting and gap-filling missing `seq` via `GET /versions/{id}/events?after_id=`, so eviction is safe and MUST NOT affect other connections or the fan-out consumer.

#### Scenario: A stalled client is evicted, others keep flowing
- **GIVEN** a slow connection whose outbound buffer is full and a healthy connection subscribed to the same topic
- **WHEN** new events arrive faster than the slow connection drains
- **THEN** the gateway MUST close the slow connection and MUST continue delivering events to the healthy connection without stalling

### Requirement: Server-Side Read Deadline

The gateway SHALL apply a read deadline to each connection longer than the client's ping interval (the client pings every ~25s). A connection with no inbound frame within the deadline MUST be closed and purged from the connection registry and topic index, so a half-open connection (vanished client, no TCP FIN) cannot leak a goroutine or a stale subscription that inflates the active-connections gauge.

#### Scenario: Idle half-open connection is reaped
- **GIVEN** a connection that stops sending any frame (including pings)
- **WHEN** the read deadline elapses
- **THEN** the gateway MUST close the connection and remove it from the registry and every topic subscription set

### Requirement: Graceful Shutdown

On API shutdown the gateway SHALL stop its fan-out consumer and close all live WebSocket connections with close code `1001` (going away) within the configured drain window, so clients reconnect against the next available instance.

#### Scenario: Shutdown closes connections with 1001
- **WHEN** the API begins graceful shutdown with live WebSocket connections
- **THEN** each connection MUST be closed with code `1001`, the fan-out consumer MUST stop, and shutdown MUST proceed within the drain window

### Requirement: Realtime Observability

The gateway SHALL expose Prometheus metrics for connection and fan-out health: a gauge of active connections, a gauge of active subscriptions, a counter of events fanned out (labelled by outcome), a counter of dropped clients (labelled by reason), and a gauge for the fan-out consumer's connection state (mirroring the existing `EventConsumerConnected` so an exclusive-queue drop is observable). All collectors MUST be registered in the existing `observability.Metrics` registry — the gateway MUST NOT create its own registry. Connection open/close and subscribe/deny MUST be logged with `trace_id` and the relevant `task_id`/`version_id`; the auth token MUST NOT appear in any log field (the gateway MUST NOT log `r.URL.RawQuery` / `r.URL.String()` — the standard middleware already logs only the path).

#### Scenario: Metrics reflect connection and fan-out activity
- **WHEN** connections open, subscribe, receive events, and a slow client is evicted
- **THEN** the active-connections and active-subscriptions gauges MUST track the live counts, the fanned-out counter MUST increment per delivered frame, and the dropped-clients counter MUST increment on eviction

#### Scenario: Token is never logged
- **WHEN** a connection opens (with `?token=...`) or is rejected with 4001
- **THEN** no log field MUST contain the token value
