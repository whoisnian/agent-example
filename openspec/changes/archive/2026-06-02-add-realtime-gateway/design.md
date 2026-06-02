## Context

`add-worker-control-handling` now emits `status:paused|running|cancelling` events and the worker emits `log`/`step`/`artifact` events onto the `task.events` topic exchange. The API already consumes that exchange via the shared durable work queue `q.task.events` (competing consumers, no leader) to drive DB status ingest. The web client (`web/src/services/ws.ts`, capability `web-realtime-client`) is built to a fixed wire contract (ARCHITECTURE §5.2): `?token=` auth, `{op:subscribe/unsubscribe/ping}` frames over `task:`/`version:` topics, `{topic, kind, seq, ts, payload}` server frames, seq-based dedup + gap-fill via REST. The server side does not exist. This change builds it.

The API is Gin + net/http, layered. No WebSocket dependency exists yet. Auth is still a stub (`DevTenantID`/`DevUserID`).

## Goals / Non-Goals

**Goals:**
- A `/ws` endpoint that the existing web client connects to with zero client changes.
- Live fan-out of worker events to subscribed, owner-authorized connections, scaled horizontally (every instance independent).
- Bounded memory under slow clients; clean shutdown.

**Non-Goals:**
- Cross-instance message routing / Redis pub-sub coordination (ARCHITECTURE §6.2 explicitly chooses "every gateway consumes the same exchange, filters locally" over forwarding).
- Real JWT auth (stays stubbed via Dev identity, like all REST handlers, until a dedicated auth change).
- The gateway writing to the DB — DB ingest stays the job of the existing `q.task.events` consumer. The gateway is read-only w.r.t. state.
- Server-side gap-fill / replay buffering — the client gap-fills via the existing REST `/events?after_id=` cursor.
- Cost-delta frames — events flow from `task.events` only; cost is surfaced via REST in this round (a later change can add a cost topic if needed).

## Decisions

### D1 — Per-instance exclusive fan-out queue, separate from the ingest work queue
Each gateway process declares its own exclusive, auto-delete, non-durable queue bound to `task.events`. The worker publishes with routing key `event.<task_type>.<kind>` (3 segments — verified `worker/core/publisher.py`; ARCHITECTURE §5.3), so the binding MUST be `event.#` (the `#` wildcard matches the 3-segment key; `event.*` would NOT — don't "optimize" it to `*`). **Why a separate queue:** the existing `q.task.events` is a *shared work queue* — RabbitMQ load-balances deliveries across consumers, so a given event reaches only one replica. For fan-out, *every* replica must see *every* event (it only knows about its own connections). An exclusive per-instance queue gives each gateway the full stream without disturbing ingest (the existing ingest already binds `event.#` on the shared queue, so the pattern is proven). **Alternative:** add a second binding on `q.task.events` — rejected, it would steal deliveries from ingest. **Alternative:** Redis pub-sub fan-out across instances — rejected per ARCHITECTURE §6.2 (the documented MVP simplification is local filtering, no cross-instance forwarding). See the reconnect risk below: the queue is connection-scoped and MUST be re-declared on reconnect.

### D2 — `github.com/coder/websocket` for the WS transport
Use `coder/websocket` (the maintained successor to `nhooyr.io/websocket`). **Why:** context-aware `Read`/`Write`, net/http-native `Accept(w, r)` that drops straight into a Gin handler via `c.Writer` / `c.Request`, and a simpler model than gorilla's manual read/write pumps. `wsjson.Read/Write` covers the small JSON frames. **Alternative:** `gorilla/websocket` — battle-tested but requires hand-rolled ping/pong + concurrency discipline; coder's context cancellation maps cleanly onto our shutdown path (D6). Either is acceptable; coder is the lighter fit.

### D3 — Hub + per-connection goroutine with a bounded send channel
A single `Hub` owns the connection registry and a `topic → set<*conn>` index, guarded by a mutex (or an actor goroutine). Each `*conn` has a buffered `send chan frame` and one writer goroutine draining it to the socket; one reader goroutine parses client frames. The fan-out consumer, for each event, looks up subscribers for the two derived topics and does a **non-blocking** send to each conn's channel. **Why:** the non-blocking send is the backpressure primitive (D4) — the fan-out path never blocks on a slow socket. Subscription mutations (subscribe/unsubscribe) and fan-out both touch the topic index, so it's mutex-guarded; the index maps topic→conns so fan-out is O(subscribers), not O(connections).

### D4 — Slow client = evict, don't buffer
Each conn's `send` channel is bounded (e.g. 64–256 frames). If a non-blocking send finds the buffer full, the hub closes that connection (server-initiated, a distinct close code) and increments `ws_client_dropped_total{reason="slow"}`. **Why:** unbounded buffering is a memory-exhaustion vector and head-of-line stall risk; the client is built to reconnect + gap-fill via REST `/events?after_id=`, so eviction is lossless from the user's perspective. Eviction of one conn never touches another.

### D5 — Ownership checked at subscribe time, through an application-layer port
A `subscribe` resolves each topic's owner once; only authorized topics enter the conn's subscription set. Fan-out then trusts the set — no per-event DB hit. **Why:** per-event ownership queries would put a DB round-trip on the hot fan-out path. Checking once at subscribe is correct because ownership of a task/version does not change (no transfer in MVP).

**Layering / seam (corrects the first draft):** the gateway lives in `interfaces/` and MUST NOT import `domain/task` directly (AGENTS.md §4.1 — interfaces ↔ application ↔ domain). And the reusable probes don't exist as the draft assumed: `ownedVersion` has a package-level function form (`read_service.go` — `ownedVersion(ctx, q sqlc.Querier, owner, versionID)`), but `ownedTask` is only an **unexported method** on `*ReadService`. So this change adds (a) a package-level `ownedTask`-equivalent in `domain/task` mirroring the existing package-level `ownedVersion`, and (b) a thin **application** port, e.g. `apptask.OwnershipChecker` with `OwnsTask(ctx, owner, id) error` / `OwnsVersion(...)`, returning the existing `ErrTaskNotFound`/`ErrVersionNotFound` sentinels so "not found" and "not owned" stay indistinguishable (no existence leak). The gateway depends only on that port. **Trade-off:** a task deleted after subscribe keeps a dead subscription — harmless (no events arrive), out of scope (MVP has no delete).

### D6 — Forward the worker's `ts`; fall back to receive-time
**Corrects the first draft's false premise.** The worker DOES stamp an authoritative `ts` on every event (`worker/core/messages.py` `TaskEvent.ts`, set to `datetime.now(UTC)` in `publisher.py`; ARCHITECTURE §5.3). What lacks `ts` is the API's *existing* ingest decoder struct `taskEventEnvelope`, which simply doesn't read the field. So the gateway's own decode struct MUST include `Ts` and forward the worker's value. Only if a delivery has no parseable `ts` does the gateway fall back to `time.Now().UTC()`. **Why forward rather than re-stamp:** the worker time is the true event time and is consistent across gateway instances (re-stamping would make two instances disagree on the same `seq`'s `ts`). The client still orders/dedups by `seq`, so `ts` remains display-only — but forwarding is the honest, instance-consistent choice.

### D8 — `{op:"ping"}` gets an app-level `{op:"pong"}` text frame (open question resolved)
The client (`ws.ts`) pings every 25s and self-closes after ~60s with no inbound traffic; its liveness timer resets only on `onmessage`. A protocol-level pong is **not** surfaced to the browser `WebSocket.onmessage`, so relying on coder/websocket's automatic protocol pong would let an idle-task connection go stale and reconnect every 60s. Therefore the gateway replies to `{op:"ping"}` with an application-level `{op:"pong"}` text frame. The client ignores non-event frames, so the pong is harmless there but resets its inbound-liveness timer. Also: the server applies its own read deadline (> ping interval) to reap half-open connections (spec "Server-Side Read Deadline").

### D9 — Origin allowlist at handshake (CSWSH)
coder/websocket's `Accept` is same-origin by default and takes an `OriginPatterns` allowlist. Because the token is a query param (auto-attached cross-site by the browser), the handshake MUST validate `Origin` against a configured allowlist (the SPA origin) to close the cross-site-WebSocket-hijacking vector. The allowed origins are operator-configurable (env); the dev SPA origin is documented. This is checked at upgrade, before the token/4001 check.

### D7 — Shutdown: stop consumer, close conns 1001, within drain window
The gateway registers into the ordered shutdown (after HTTP drain, alongside the other consumers). On shutdown it cancels the fan-out consumer context and closes every live conn with `1001 (going away)`. **Why:** 1001 tells the client this is a normal server-side teardown → reconnect to the next instance (vs 1006 abnormal). The per-conn writer goroutines exit on context cancel.

## Risks / Trade-offs

- **[Exclusive queue accumulates if a gateway dies mid-flight]** → auto-delete + exclusive means RabbitMQ reclaims the queue when the connection drops; no manual cleanup. Server-named (empty name) queues avoid collisions across instances.
- **[Fan-out CPU on a hot task with many subscribers]** → O(subscribers per event); bounded by connection count. Acceptable at MVP scale; the topic→conns index keeps it from being O(all connections).
- **[Mutex contention between subscribe churn and fan-out]** → index ops are short (map lookups/inserts); if it ever shows up, the hub can move to a single-goroutine actor with command channels. Start with a mutex.
- **[Token in query string in logs]** → the standard middleware already logs only `URL.Path` (verified `middleware.go` — access log L98, metrics/tracing use the route template), so it does NOT leak `?token=`. The actual guard is narrower: the gateway's own open/close/deny log lines MUST NOT log `r.URL.RawQuery`/`r.URL.String()`, and the LB MUST be configured not to access-log full `/ws` URLs. Covered by the "token never logged" spec scenario + a test.
- **[coder/websocket is a new dependency]** → small, maintained, MIT; scoped to the gateway package.
- **[Stub auth means any non-empty token "works"]** → intentional and consistent with REST handlers today. The real access-control boundary is the subscribe-time ownership check against the resolved (stub) identity — NOT the 4001 presence check. Cross-tenant leakage is impossible today because every connection resolves to the same Dev identity and ownership is checked against it. Real per-user validation arrives with the auth change.
- **[Fan-out exclusive queue lost on connection (not channel) drop]** → exclusive/auto-delete queues are connection-scoped; the shared `*Connection` auto-reconnects, so the consumer MUST re-declare + re-bind on every (re)connect, unlike the existing durable-queue `Consumer` which only re-`Consume`s. A naive "mirror the existing Consumer" would silently stop delivering after a blip. Pinned by a spec scenario + test.
- **[Per-topic ownership probe = DB amplification on a huge subscribe]** → bound the `topics` array length and topics-per-connection (spec "Resource Limits") so one frame can't trigger thousands of DB probes; set an explicit socket read limit.

## Migration Plan

- Additive: new endpoint, new ephemeral queue, new dependency. No DB migration, no change to `DeclareTopology`'s durable topology, no worker change.
- Rollback: revert; the web client detects the WS is unavailable and stays on its REST polling fallback (already its default when `getConnectionState() !== "open"`).
- Deploy note: behind a load balancer, `/ws` needs WebSocket upgrade pass-through (Connection/Upgrade headers, longer idle timeout) — document in the API README.

## Open Questions

- Send-buffer depth (64 vs 256), topics-per-connection cap, read-deadline duration, and the slow-client close code — pick concrete values in apply; expose the tunable ones as config.

## Scope note (PR size)

Production surface (fan-out consumer with re-declare + ownership seam + hub + conn reader/writer + handler + origin/limits + 4 metrics + main.go/server.go wiring) plausibly runs 400–550 lines before tests, near the AGENTS.md §7 budget. Pre-agreed split point if it overruns: land **(1) the fan-out consumer + application ownership port** first, then **(2) the WS hub + handler**. Tests (incl. the net-new RabbitMQ fixture) are excluded from the budget but are real work.
