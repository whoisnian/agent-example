## 1. Dependency + fan-out consumer

- [ ] 1.1 Add `github.com/coder/websocket` to `api/go.mod`; `go mod tidy`
- [ ] 1.2 Add a per-instance fan-out consumer in `infrastructure/messaging/` (e.g. `event_fanout.go`): declare a server-named **exclusive, auto-delete, non-durable** queue, bind it to `ExchangeEvents` (`task.events`) with key `event.#` (NOT `event.*` — the worker key is the 3-segment `event.<task_type>.<kind>`), consume, and invoke a callback with the decoded envelope. MUST NOT touch `q.task.events` or the DB (D1). Use a decode struct that INCLUDES `ts` (`{task_id, version_id, run_id, seq, kind, ts, payload}`) — the worker stamps `ts`; the existing `taskEventEnvelope` just drops it (D6)
- [ ] 1.3 On channel/connection loss, **re-declare + re-bind** the exclusive queue (not merely re-`Consume`) before resuming — the queue is connection-scoped and vanishes on connection drop (D1 risk; deliberate divergence from the durable-queue `Consumer`). Expose `Stop()` for ordered shutdown and a `connected` gauge

## 2. Application ownership port + domain seam

- [ ] 2.1 Add a package-level `ownedTask(ctx, q sqlc.Querier, owner, taskID) (sqlc.Task, error)` in `domain/task` mirroring the existing package-level `ownedVersion`; have the `*ReadService` method delegate to it (no behavior change)
- [ ] 2.2 Add an application-layer ownership port `apptask.OwnershipChecker` with `OwnsTask(ctx, tenantID, userID, id) error` / `OwnsVersion(...)` returning the existing `ErrTaskNotFound`/`ErrVersionNotFound` sentinels (not-found and not-owned indistinguishable). The gateway depends ONLY on this port — it never imports `domain/task` (D5, §4.1 layering)

## 3. Connection hub + conn

- [ ] 3.1 Add the gateway package (`interfaces/ws/`): `Hub` holding the connection registry and a `topic → set<*conn>` index under a mutex (D3); methods `register`, `unregister`, `subscribe(conn, topics)`, `unsubscribe(conn, topics)`, `fanout(event)`. Subscription set is per-conn keyed by topic (idempotent re-subscribe — no double-count)
- [ ] 3.2 `*conn`: a bounded `send chan frame` (depth configurable, default ~128), one writer goroutine draining to the socket, one reader goroutine; `serverFrame{Topic, Kind string; Seq int64; Ts string; Payload json.RawMessage}`, `controlFrame{Op string}` for pong, and `clientFrame{Op string; Topics []string}`
- [ ] 3.3 `fanout`: derive topics `task:<task_id>` + `version:<version_id>`, forward the worker `ts` (fallback `now().UTC()` if absent), and do a **non-blocking** send to each subscribed conn; on full buffer, evict that conn (close + `WSClientDroppedTotal{reason="slow"}`) without blocking others (D4). A conn subscribed to both the task and version topic of one event gets two frames (intended, D-spec)

## 4. WS handler: handshake, protocol, ownership, limits

- [ ] 4.1 `GET /api/v1/ws` handler: validate `Origin` against a configurable allowlist at upgrade (coder `OriginPatterns`, D9) → reject cross-origin; then reject empty `?token` with close `4001` before registering (never log the token / `RawQuery`); else resolve identity via `DevTenantID`/`DevUserID` and register. Set an explicit socket read limit
- [ ] 4.2 Reader loop: handle `{op:"subscribe"|"unsubscribe"|"ping"}`; `ping` → send an **application-level `{op:"pong"}` text frame** (D8 — protocol pong is invisible to the browser, so idle conns would reconnect every 60s); unknown op / malformed topic → `error`-kind frame (diagnostic — the client ignores non-event frames, so also log/count it), connection stays open
- [ ] 4.3 Subscribe authorization via the `apptask.OwnershipChecker` port (task 2.2): each topic probed once; unauthorized/unknown/malformed → `error` frame, not added, not-found≡not-owned (D5). Cap the per-`subscribe` `topics` array length AND topics-per-connection so one frame can't trigger unbounded ownership probes (spec "Resource Limits")
- [ ] 4.4 Server read deadline (> client 25s ping); a connection idle past the deadline is closed and purged from the hub + topic index (no goroutine/subscription leak). Closed/evicted conns are always removed from the index

## 5. Wiring, shutdown, metrics

- [ ] 5.1 `cmd/api/main.go`: construct the Hub + fan-out consumer (callback → `hub.fanout`) + the `OwnershipChecker`, register `/ws` on the v1 group, add both to the ordered shutdown — stop the consumer, close all conns with `1001`, within the drain window (D7)
- [ ] 5.2 `interfaces/http/server.go`: `ServerDeps` gains the gateway; register the `/ws` route (raw upgrade, not enveloped)
- [ ] 5.3 Add metrics to the existing `observability.Metrics` struct + the single `MustRegister` block (no separate registry): `WSConnectionsActive` (Gauge), `WSSubscriptionsActive` (Gauge), `WSEventsFannedTotal{outcome}` (CounterVec), `WSClientDroppedTotal{reason}` (CounterVec), `WSFanoutConsumerConnected` (Gauge, mirrors `EventConsumerConnected`). Log open/close/deny with `trace_id` + `task_id`/`version_id`, never the token

## 6. Tests

- [ ] 6.1 Hub unit tests (fake conns, no real sockets): subscribe→fanout delivers to matching topic only; unsubscribe stops delivery; one event → both `task:` and `version:` subscribers (same seq/kind/payload/ts); idempotent re-subscribe doesn't double-count or double-deliver; closing a conn purges it from the topic index
- [ ] 6.2 Backpressure unit test: a conn with a full `send` buffer is evicted while a healthy conn on the same topic keeps receiving; `WSClientDroppedTotal{slow}` increments (D4)
- [ ] 6.3 Ownership unit test (fake `OwnershipChecker`): unowned/unknown/malformed → `error` frame, not subscribed, not-found≡not-owned; events for unauthorized topics never delivered (D5). Oversized `topics` array rejected without unbounded probes
- [ ] 6.4 Protocol unit tests: empty-token connect → 4001 (token never in logs); cross-origin handshake rejected (D9); `ping` → `{op:"pong"}` text frame and no subscription change; unknown op → `error`, conn stays open; idle conn past read deadline is reaped (no leak)
- [ ] 6.5 Add `github.com/testcontainers/testcontainers-go/modules/rabbitmq` to go.mod and a **net-new** RabbitMQ container fixture (none exists today — go.mod has only postgres+minio; messaging tests are fakes). Declare topology against it
- [ ] 6.6 Integration test (PG + the new RabbitMQ fixture): a real WS client connects, subscribes to an owned `task:<id>`, a message published to `task.events` (`event.<task_type>.<kind>`) arrives as `{topic, kind, seq, ts, payload}` carrying the worker `ts`; an event for an unowned task is NOT delivered; after an AMQP connection drop the fan-out queue is re-declared and delivery resumes (1.3)
- [ ] 6.7 Shutdown test: live conns closed with `1001`, fan-out consumer stops within the drain window

## 7. Gates + docs

- [ ] 7.1 `go vet ./...`, `go test ./...`, `golangci-lint run`, `make sqlc` (no diff) all clean
- [ ] 7.2 `make test-integration` (PG + RabbitMQ) green
- [ ] 7.3 `openspec validate add-realtime-gateway --strict` valid
- [ ] 7.4 `api/README.md`: document the WS env (origin allowlist, buffer depth, read deadline) + the LB WebSocket pass-through note (upgrade headers, longer idle timeout, don't access-log full `/ws` URLs). Tighten `docs/ARCHITECTURE.md §6.2` if needed

> **PR-size note (design "Scope note"):** production code may run 400–550 lines (near the §7 budget). If it overruns, split at the pre-agreed seam: land §1–2 (fan-out consumer + ownership port) first, then §3–5 (hub + handler).
