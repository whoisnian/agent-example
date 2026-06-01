# Review: add-realtime-gateway — Improvement Suggestions

Grounded against actual code (file/line cited per finding). Numbered, severity-tagged.

---

1. **[blocker]** — *The "worker envelope carries no timestamp" premise is factually wrong.*
   - **Where**: proposal.md L11 ("the worker event envelope carries no timestamp"); design.md D6 L38–39 ("The worker envelope has no timestamp"); spec.md "Per-Instance Event Fan-Out" L58 ("the envelope carries no timestamp"); tasks.md 2.3 / 1.2.
   - **Issue**: The worker DOES emit `ts`. Verified: `worker/worker/core/messages.py` L53 `ts: datetime` on `TaskEvent` (model_config `extra="forbid"`), stamped in `worker/worker/core/publisher.py` L88 `ts=datetime.now(UTC)`; and `docs/ARCHITECTURE.md` §5.3 L656–662 shows the event envelope with `"ts": "..."`. What actually has no `ts` is the API-side decoder struct `taskEventEnvelope` in `api/internal/infrastructure/messaging/event_ingest.go` L28–35 (six fields, no `Ts`). So the design reasons from the Go struct, not the wire. The conclusion (stamp at fan-out, order by seq) may still be acceptable, but the *rationale* is false and will mislead the implementer into thinking the worker ts does not exist.
   - **Suggested change**: Rewrite D6 to: "the worker envelope DOES carry an authoritative `ts` (ARCHITECTURE §5.3, worker `TaskEvent.ts`), but the API's existing `taskEventEnvelope` decoder drops it. The gateway will [forward the worker `ts` by adding a `Ts` field to its own decode struct] OR [deliberately re-stamp at fan-out as 'time forwarded by this gateway', because the client orders by seq and ts is display-only]." Pick one and state the trade-off honestly. If re-stamping, note that two gateways will show slightly different ts for the same seq — acceptable since ts is advisory. Update proposal.md L11 and spec.md L58 to match.
   - **Confidence**: high — verified in worker code + ARCHITECTURE.

2. **[important]** — *Token-in-access-log risk is overstated; the real exposure is narrower.*
   - **Where**: design.md "Risks" L49 ("Token in query string ends up in access logs"); spec.md "Token is never logged" scenario L98–100; tasks.md 3.1 / 4.3.
   - **Issue**: The existing `accessLogMiddleware` logs `c.Request.URL.Path` only (`api/internal/interfaces/http/middleware.go` L98), NOT `RawQuery`; `metricsMiddleware` uses `c.FullPath()` route template (L80–83); `tracingMiddleware` uses route/path (L47–51), not the raw query. So the standard chain does **not** leak `?token=`. The token only leaks if the gateway itself logs `r.URL.String()`/`RawQuery`, or if the upstream proxy access log captures full URLs. The spec scenario is good to keep, but the design risk should be re-scoped so the implementer guards the *right* thing (the gateway's own log lines + the LB note) rather than "fixing" middleware that is already safe.
   - **Suggested change**: Reword design risk to: "The standard middleware logs only path/route (verified, no leak). The gateway MUST NOT log `r.URL.RawQuery`/`r.URL.String()` in its own open/close/deny lines; log `task_id`/`version_id`/`trace_id` only. Also add the LB note (don't log full request URLs for `/ws`)." Keep the "token never logged" scenario.
   - **Confidence**: high — verified middleware.go.

3. **[blocker]** — *`ownedTask` cannot be reused as claimed without a new exported seam; `ownedVersion` already has one — the proposal conflates them.*
   - **Where**: proposal.md L10/L33; design.md D5 L36; spec.md "Owner-Scoped Subscriptions" L43; tasks.md 3.3 ("validate via the read-side `ownedTask`/`ownedVersion` probes ... Wire a small read-only port").
   - **Issue**: Verified in `api/internal/domain/task/read_service.go`: `ownedTask` is an **unexported method** on `*ReadService` (L278) — not callable from `interfaces/ws`. `ownedVersion` exists BOTH as an unexported method (L296) AND as a **package-level function** `ownedVersion(ctx, q sqlc.Querier, owner, versionID)` (L305) explicitly built "so services that aren't the task ReadService can reuse the exact same guard." So version-ownership has a reusable seam; **task-ownership does not**. Also, `interfaces/ws` calling `domain/task` directly is the wrong layering per AGENTS.md §4.1 (interfaces ↔ application ↔ domain) — every other handler goes through an `application/task` service (see `main.go` L248 `apptask.NewReadService(taskdomain.NewReadService(...))`). The gateway should get an **application-layer** ownership port, not reach into domain.
   - **Suggested change**: Add an explicit task (or expand 3.3) to: (a) add an exported task-ownership probe — either a package-level `OwnsTask(ctx, q, owner, taskID) error` in domain mirroring the existing package-level `ownedVersion`, or a method on `ReadService`; (b) expose both task+version ownership through a thin **application** port (e.g. `apptask.OwnershipChecker` with `OwnsTask`/`OwnsVersion`) that the gateway depends on, returning the existing `ErrTaskNotFound`/`ErrVersionNotFound` sentinels so not-found and not-owned stay indistinguishable (D5). Note the layering explicitly so the gateway never imports `domain/task` directly.
   - **Confidence**: high — verified read_service.go + main.go wiring.

4. **[important]** — *No RabbitMQ test fixture exists; task 5.5 assumes one that isn't there (repeat of the MinIO-fixture gap).*
   - **Where**: tasks.md 5.5 ("testcontainers PG + RabbitMQ ... Reuse the existing PG suite + a RabbitMQ container (mirror messaging integration tests)"); 6.2.
   - **Issue**: Verified there is **no** RabbitMQ testcontainer anywhere. `api/go.mod` L16–18 has only `testcontainers-go`, `.../modules/minio`, `.../modules/postgres` — no `modules/rabbitmq`. The messaging unit tests use **fakes only** (`api/internal/infrastructure/messaging/testhelpers_test.go` is just a counter reader; `event_ingest_test.go`/`cost_ingest_test.go` drive handlers with fake ingesters — no broker). HTTP integration tests spin only a Postgres container (`tasks_integration_test.go` header + `tcpostgres`). So "mirror messaging integration tests" is misleading — there are none against a real broker. This is the same class of omission flagged previously for the MinIO fixture.
   - **Suggested change**: Split 5.5 into an explicit fixture task: "Add `testcontainers-go/modules/rabbitmq` to go.mod and a reusable RabbitMQ container fixture (mirroring the PG fixture in `*_integration_test.go`), declare the topology against it, then run the WS end-to-end test." Call out that this is net-new test infra, not a reuse, so the budget reflects it.
   - **Confidence**: high — verified go.mod + test files.

5. **[important]** — *Missing requirement: duplicate / overlapping subscribe coalescing on the server, and re-subscribe-on-reconnect behavior.*
   - **Where**: spec.md "Subscription Protocol"; design.md D3.
   - **Issue**: The client (`web/src/services/ws.ts`) re-sends ALL active topics in a single `{op:"subscribe", topics:[...]}` frame on every reconnect (L240–241 `onOpen`) and coalesces multiple handlers into one subscribe (L122–138). The archived spec `web-realtime-client` makes both behaviors normative ("re-send all currently-subscribed topics in a single subscribe frame after each reconnect"; "coalesce overlapping subscriptions"). The server spec says nothing about what happens when a connection subscribes to a topic it is **already** subscribed to (idempotent add? duplicate in the set? double-count of `ws_subscriptions_active`?). Since the client legitimately re-sends the full set after reconnect on the SAME conn only after a new socket, the risk is mainly the gauge double-counting and set semantics.
   - **Suggested change**: Add a scenario: "Subscribing to an already-subscribed topic is idempotent — the subscription set and `ws_subscriptions_active` MUST NOT double-count, and delivery is unaffected." State the subscription set is a set keyed by topic per connection.
   - **Confidence**: high — verified ws.ts behavior + archived spec.

6. **[important]** — *Missing requirement: connection limits, max topics per connection, and inbound message-size limits.*
   - **Where**: spec.md (absent); design.md D4 only bounds the *outbound* buffer.
   - **Issue**: Backpressure (D4) bounds outbound memory, but there is no bound on: (a) number of topics a single connection may subscribe to (a malicious/buggy client could send `{op:"subscribe", topics:[10000 topics]}`, each triggering an ownership DB probe — a DB-amplification DoS, since D5 does one probe per topic at subscribe time); (b) max concurrent connections per instance; (c) inbound frame size (`coder/websocket` defaults to a 32KiB read limit, but it should be set explicitly). None are in the spec.
   - **Suggested change**: Add a requirement "Resource limits": cap topics-per-connection (e.g. reject/error beyond N), set an explicit `SetReadLimit` on the socket, and optionally cap the per-subscribe `topics` array length to bound the ownership-probe fan-out. Add a task under §3.
   - **Confidence**: high — D5 does per-topic DB probes (read_service.go probes hit the DB); coder/websocket read-limit default is a known value.

7. **[important]** — *Missing requirement: Origin / CSWSH protection given `?token=` auth.*
   - **Where**: spec.md (absent); design.md Risks (absent).
   - **Issue**: `coder/websocket`'s `Accept` enforces same-origin by **default** (rejects cross-origin handshakes unless `InsecureSkipVerify`/`OriginPatterns` is set). The token rides in the query string, not a custom header, so the browser will send it on a cross-site WS open if origin checking is disabled — classic CSWSH. The spec/design never states whether origin checking is on, nor what origins the SPA serves from (`VITE_WS_URL` default `ws://localhost:8080/api/v1/ws`). If left default-deny, the dev SPA on a different port may itself be blocked; if disabled, CSWSH is open.
   - **Suggested change**: Add a requirement/decision: "The handshake MUST validate Origin against an allowlist (configurable; matches the SPA origin). Document the dev/prod origins and the env that configures them." Add a task under §3 and a scenario "cross-origin handshake without an allowed Origin is rejected."
   - **Confidence**: high (CSWSH is a real concern with query-param token auth); medium on coder default specifics — verify `Accept` options during apply.

8. **[important]** — *Stub-auth 4001 semantics: any non-empty token passes — flag as a known, scoped security gap.*
   - **Where**: design.md Risks L51; spec.md "WebSocket Endpoint" L5 ("any non-empty `token`"); proposal.md L7.
   - **Issue**: Consistent with REST handlers today (identity is `DevTenantID`/`DevUserID`, see `main.go` L186–188), so this is intentional. But combined with #7 (no origin check) it means a cross-site page could open an authenticated WS with a junk token and stream another tenant's events IF that page can also pass ownership — it can't, because ownership is checked against the Dev identity, so cross-tenant leakage is currently impossible. Worth stating that the only thing 4001 gates is *presence* of a token, and real isolation waits on `add-api-auth`.
   - **Suggested change**: Keep the behavior; add one sentence to the design risk making explicit that "subscribe-time ownership against the resolved (stub) identity is the actual access-control boundary, not the 4001 check" so a future reviewer doesn't mistake 4001 for authorization.
   - **Confidence**: high — verified stub identity wiring.

9. **[important]** — *`ping` response: resolve the open question before/at apply against ws.ts, and pin it in the spec.*
   - **Where**: design.md Open Questions L61; spec.md "Ping does not change subscriptions" L33–35 (vague "liveness reply"); tasks.md 3.2.
   - **Issue**: Verified `ws.ts` `onMessage` (L246–261): non-string frames are ignored; any JSON frame failing `isRealtimeEvent` (requires `topic`+`kind`+`seq`+`ts`) is **silently ignored** ("Server may push non-event frames (e.g. pong). Ignore."). So an app-level `{op:"pong"}` is **harmless but unnecessary** — the client's liveness is driven purely by `lastInboundAt` being refreshed on ANY inbound frame (L247). Critically: the client's 60s stale-timeout (L343–352) only resets on inbound traffic, so the gateway MUST send *something* periodically on otherwise-idle owned topics, OR respond to the client's 25s `{op:"ping"}` with any frame, OR rely on coder's protocol-level pong (which `coder/websocket` answers automatically but does NOT deliver a `message` event to the JS `WebSocket` — protocol pongs are invisible to `onmessage`, so they will NOT refresh `lastInboundAt`).
   - **Issue (consequence)**: If the gateway only answers with a protocol-level pong and the task is idle, the browser's `onmessage` never fires, `lastInboundAt` goes stale after 60s, and the client needlessly reconnects every 60s. So an **app-level `{op:"pong"}` text frame (or any text frame) is effectively required** to keep idle connections alive.
   - **Suggested change**: Resolve the open question NOW in favor of "respond to `{op:"ping"}` with an app-level `{op:"pong"}` text frame," and make the spec scenario concrete: "the server MUST send a text frame in response to `{op:"ping"}` (so the client's inbound-liveness timer resets); a protocol-level pong alone is insufficient because the browser does not surface it to `onmessage`." Reference ws.ts L343–352.
   - **Confidence**: high — verified ws.ts heartbeat + onMessage; the protocol-pong-invisible-to-JS behavior is standard browser WebSocket semantics.

10. **[important]** — *"No Modified Capabilities" is incorrect — api-bootstrap (shutdown order + route + ServerDeps + new dependency) is materially changed.*
    - **Where**: proposal.md "Modified Capabilities (none)" L23–25; tasks.md 4.1/4.2.
    - **Issue**: The change adds a new consumer goroutine + new shutdown step in `cmd/api/main.go` (the ordered shutdown at L328–362 is spec-bound — comment "per spec Task 5.3"), adds a field to `ServerDeps` (`server.go` L18–27), registers a new route, and adds a dependency. The messaging-topology claim ("api-messaging unchanged") is correct (the ephemeral queue isn't in `DeclareTopology`), but the bootstrap/shutdown contract is owned by the api-bootstrap capability and the ordered-shutdown sequence is part of it. Declaring zero modified capabilities understates the blast radius and skips the spec delta that should record "gateway stops after HTTP drain, before MQ close, closing conns 1001."
    - **Suggested change**: Either add a `## MODIFIED Requirements` delta to api-bootstrap capturing the new shutdown step + route, or explicitly justify in the proposal why the bootstrap spec needs no delta (with the same rigor the topology claim got). Don't leave it as a bare "(none)".
    - **Confidence**: high — verified main.go shutdown block + server.go.

11. **[important]** — *Channel/connection sharing for the fan-out consumer vs the existing publisher/consumers is unspecified — exclusive-queue semantics interact with the shared `*Connection`.*
    - **Where**: design.md D1 / Risks L46; tasks.md 1.2/1.3.
    - **Issue**: `Connection.Channel()` (`connection.go` L121–129) hands out fresh channels off ONE shared amqp connection that auto-reconnects (watchLoop L58). An **exclusive, auto-delete** queue is bound to the *connection* (RabbitMQ deletes it when the declaring connection drops, not the channel). Since the gateway shares the process-wide `*Connection` with the publisher + ingest consumers, the exclusive queue survives a *channel* reconnect but is destroyed on a *connection* reconnect — at which point the existing `Consumer.subscribeAndServe` (`consumer.go` L113) merely re-`Consume`s an existing durable queue, whereas the fan-out consumer must **re-declare + re-bind** its exclusive queue on every reconnect. The proposal says "mirror the existing Consumer resilience" (tasks 1.3) but the existing Consumer does NOT redeclare its queue — so a naive mirror loses the fan-out queue after a connection blip and silently stops delivering events with no error.
    - **Suggested change**: Make tasks 1.2/1.3 explicit: the fan-out consumer MUST re-declare the exclusive/auto-delete queue AND re-bind `event.#` on every (re)subscribe (not just re-Consume), because the queue is connection-scoped and vanishes on connection loss. Add a test/scenario for "after a connection drop, the fan-out queue is re-declared and delivery resumes." Note this is a deliberate divergence from the existing `Consumer`.
    - **Confidence**: high — verified connection.go + consumer.go; exclusive/auto-delete queue lifetime is standard AMQP semantics.

12. **[important]** — *Routing-key reality: confirm `event.#` captures the worker's 3-segment key, and document the real key.*
    - **Where**: proposal.md L9; design.md D1; spec.md "Per-Instance Event Fan-Out" L56 (binds `event.#`); tasks.md 1.2.
    - **Issue**: Verified the worker publishes with routing key `event.<task_type>.<kind>` (`worker/worker/core/publisher.py` L108 `routing_key=f"event.{task_type}.{kind}"`; ARCHITECTURE §5.3 L655). The topic binding `event.#` matches "event." + zero-or-more words, so it **does** capture `event.codegen.status` (and the existing `q.task.events` uses the same `event.#`, `topology.go` L66 — so this is proven in production). The claim is correct, but the proposal never states the actual key shape, so an implementer can't sanity-check the binding. (For the record, `event.*` would NOT match the 3-segment key — only `#` works.)
    - **Suggested change**: Add a one-line note in D1/spec: "Worker keys are `event.<task_type>.<kind>` (ARCHITECTURE §5.3); the binding MUST be `event.#` — `event.*` would not match the 3-segment key." Prevents a future 'optimization' to `event.*` from silently breaking fan-out.
    - **Confidence**: high — verified publisher.py + topology.go.

13. **[nice-to-have]** — *Cost increments won't reach the WS in this change — the spec implies "live cost" but events come only from `task.events`.*
    - **Where**: proposal.md L34 ("Unblocks the live view ... status + cost deltas stream"); design.md Non-Goals L19.
    - **Issue**: ARCHITECTURE §5.3 L679 says the Cost Service forwards a slim cost increment back onto `task.events` (step ⑤). But the gateway binds `event.#` on `task.events`, and the Cost Service forward is described in `add-cost-service`/ARCHITECTURE but may not yet emit onto `task.events` with an `event.*` key (the cost path publishes to `cost.exchange` with `cost.<kind>`, `publisher.py` L231). So "cost deltas stream" (proposal L34) is aspirational unless something republishes cost onto `task.events`. Design.md L19 correctly scopes cost OUT ("a later change can add a cost topic") — but proposal L34 contradicts it.
    - **Suggested change**: Align proposal L34 with design Non-Goal L19: say it unblocks **status** live-view; cost stays REST-polled this round.
    - **Confidence**: medium — verified cost publishes to cost.exchange, not task.events; the §5.3-⑤ forward is documented but I did not find it implemented.

14. **[nice-to-have]** — *Metric naming should match the prom-style of existing collectors (suffix `_total`, `_seconds`, consistent help text).*
    - **Where**: proposal.md L15; spec.md "Realtime Observability"; tasks.md 4.3.
    - **Issue**: Existing collectors in `api/internal/infrastructure/observability/metrics.go` follow `*_total` for counters with `WithLabelValues` (e.g. `EventsIngestedTotal`, `CostEventsConsumedTotal`) and bare gauges (`EventConsumerConnected`). The proposed names `ws_connections_active`/`ws_subscriptions_active` (gauges) and `ws_events_fanned_total{outcome}`/`ws_client_dropped_total{reason}` (counters) are consistent — good. But ensure they're registered in the single `reg.MustRegister(...)` block (metrics.go ~L194) and the gateway never registers its own registry. Also consider a `connected` gauge for the fan-out consumer mirroring `EventConsumerConnected` (per-queue connection-state signal) — the existing `Consumer` pattern provides one and tasks.md omits it.
    - **Suggested change**: Add to 4.3: register all WS metrics in the existing `Metrics` struct + `MustRegister` block; add a `WSFanoutConsumerConnected` gauge mirroring `EventConsumerConnected` so an exclusive-queue drop is observable.
    - **Confidence**: high — verified metrics.go patterns.

15. **[nice-to-have]** — *Behavior when an event matches both task and version subscriptions on the SAME connection is unspecified (double-delivery).*
    - **Where**: spec.md "One event fans out to both task and version subscribers" L60–63 (covers two *different* conns).
    - **Issue**: A single connection could subscribe to both `task:T1` and `version:V1` where the event has `task_id=T1, version_id=V1`. Naive fan-out (iterate the two topic sets, non-blocking send each) delivers the event **twice** to that one connection. The client dedups by `(topic, seq)` per `TopicState.lastDeliveredSeq` (ws.ts L262–278) — but the two deliveries carry **different topics** (`task:T1` vs `version:V1`), so both pass dedup and both fire handlers. That's arguably correct (different topics, different subscribers), but the spec should say so explicitly so the implementer doesn't "fix" it.
    - **Suggested change**: Add a sentence: "A connection subscribed to BOTH the task and version topic of the same event receives TWO frames (one per topic); this is intended — the client keys dedup per topic." Or, if undesired, specify de-dup per connection per seq.
    - **Confidence**: high — verified ws.ts per-topic dedup.

16. **[nice-to-have]** — *Read deadline / idle timeout on the server side is unspecified; relying solely on client ping is fragile.*
    - **Where**: spec.md (absent); design.md (only client-side ping mentioned).
    - **Issue**: The client pings every 25s and self-closes after 60s of silence (ws.ts L338–352). But the server has no stated read deadline — a half-open connection (client vanished, no FIN) leaks a goroutine + a subscription-set entry until TCP keepalive (hours). The fan-out gauge would over-report.
    - **Suggested change**: Add a requirement: "The server MUST apply a read deadline (e.g. > client ping interval, ~60–90s); a connection with no inbound frame within the deadline is closed and purged from the hub + topic index." Add a task and a goroutine-leak test.
    - **Confidence**: high — verified client ping cadence; server-side deadline absent from all four artifacts.

17. **[nice-to-have]** — *~500-line budget is unrealistic for the full scope; plan the split explicitly.*
    - **Where**: AGENTS.md §7 (PR ≤500 lines excl. generated/tests); tasks.md whole.
    - **Issue**: Scope = fan-out consumer (+ re-declare logic, #11) + hub + conn (reader/writer goroutines) + handler + application ownership seam (#3) + 4 metrics + origin check (#7) + limits (#6) + main.go/server.go wiring + ARCHITECTURE/README docs. Non-test production code alone plausibly exceeds 500 lines. Tests are excluded from the budget but the new RabbitMQ fixture (#4) is substantial.
    - **Suggested change**: Either (a) split into two changes — "add-realtime-fanout-consumer + ownership seam" then "add-ws-gateway-handler + hub" — or (b) add a note in tasks.md acknowledging the production code may run 400–550 lines and pre-agree the split point (consumer vs hub) if it overruns.
    - **Confidence**: medium — estimate, not a measured diff.

18. **[nice-to-have]** — *Error frame shape is underspecified vs the client's `isRealtimeEvent` guard.*
    - **Where**: spec.md "Subscription Protocol" L19/L21 (error is `kind:"error"`); tasks.md 3.2/3.3.
    - **Issue**: The spec says malformed/unauthorized → an "`error`-kind frame," and `kind ∈ {status,log,step,artifact,error}`. But the client's `isRealtimeEvent` (ws.ts L412–421) requires `topic`(string) + `kind`(string) + `seq`(number) + `ts`(string) or it **silently drops** the frame (onMessage L258–260). So an error frame for a *malformed topic* (where there's no valid topic/seq) cannot satisfy `isRealtimeEvent` and will be **silently ignored by the client** — the user gets no feedback. The "soft error" scenario (spec L37–39) is therefore unobservable client-side with the current client.
    - **Suggested change**: Either (a) accept that error frames are server-log-only for MVP (state it: "error frames are diagnostic; the current client ignores non-event frames, so errors surface in server logs/metrics, not the UI"), or (b) define the error frame to still carry a `topic`/`seq`/`ts` so the client *could* route it later. Pick one and document; otherwise the error-frame requirement implies a UX that doesn't exist.
    - **Confidence**: high — verified isRealtimeEvent + onMessage drop path.

---

## Overall assessment

The proposal is well-structured, correctly chooses the per-instance exclusive-queue fan-out (matching ARCHITECTURE §6.2's "no cross-instance forwarding"), and the wire contract (ops, topics, close codes 4001/1001, `{topic,kind,seq,ts,payload}`) genuinely matches `ws.ts` and §5.2. The design discipline (backpressure-by-eviction, ownership-at-subscribe, observability) is sound. But it ships on at least three load-bearing factual errors that will mislead implementation, and several real gaps:

**Top 3 fixes (do these first):**
1. **#1 — Correct the false "worker has no ts" premise.** The worker *does* emit `ts` (verified in worker code + ARCHITECTURE §5.3); only the API decoder drops it. Decide forward-vs-restamp on honest grounds.
2. **#3 — Add the exported/application ownership seam.** `ownedTask` is an unexported domain method and can't be reused as written; only `ownedVersion` has a package-level seam. Specify an application-layer `OwnsTask`/`OwnsVersion` port so the gateway respects §4.1 layering.
3. **#9 + #16 — Pin the ping/pong + server read-deadline contract.** A protocol-level pong is invisible to the browser's `onmessage`, so an idle connection will reconnect every 60s unless the server sends an app-level text frame; and without a server read deadline, dead connections leak. Resolve the open question now and add the deadline requirement.

Also strongly recommended before apply: the RabbitMQ test fixture is net-new (#4), the fan-out consumer must re-declare its queue on reconnect unlike the existing Consumer (#11), Origin/CSWSH needs a decision (#7), and "no modified capabilities" understates the api-bootstrap shutdown/route delta (#10).

---

## Resolution (applied 2026-06-02)

All 18 findings verified against code and **all accepted**. Key changes:

- **#1 (blocker)** — Corrected the false "worker has no ts" premise. Verified `worker/core/messages.py:53` + `publisher.py:88` stamp `ts`. D6 rewritten: gateway **forwards** the worker `ts` (decode struct includes `Ts`), falls back to receive-time only if absent. proposal/spec/tasks updated.
- **#3 (blocker)** — `ownedTask` is an unexported method (only `ownedVersion` has a package-level seam). Added task group §2: package-level `ownedTask` seam + an **application-layer `apptask.OwnershipChecker` port** the gateway depends on (never imports `domain/task`, per §4.1). D5 + spec + tasks updated.
- **#9 + #16** — Pinned **app-level `{op:"pong"}`** (protocol pong is invisible to the browser → 60s reconnect storms) and a **server read deadline** to reap half-open conns. New spec requirement + D8 + tasks 4.2/4.4/6.4.
- **#7** — Added **Origin allowlist (CSWSH)** requirement + D9 + handshake task 4.1.
- **#6** — Added **Resource Limits** requirement (topics-per-conn cap, read limit, subscribe-array bound to cap ownership-probe fan-out).
- **#11** — Fan-out consumer **re-declares + re-binds** its exclusive queue on reconnect (diverges from the durable-queue `Consumer`); spec scenario + tasks 1.2/1.3 + test 6.6.
- **#4** — RabbitMQ test fixture flagged as **net-new** (own task 6.5; go.mod has only postgres+minio).
- **#12** — Documented the real worker key `event.<task_type>.<kind>` → binding MUST be `event.#` (D1 + spec + tasks).
- **#2** — Re-scoped the token-log risk: middleware logs only `URL.Path` (safe); guard is the gateway's own log lines + LB.
- **#5/#15/#18** — idempotent-subscribe, intended double-delivery (task+version on one conn), and error-frames-are-diagnostic all spec'd.
- **#8** — Stated the `4001` check gates token *presence* only; subscribe-time ownership is the real boundary.
- **#10** — Strengthened the "no modified capabilities" justification (additive route+consumer+shutdown, consistent with `add-cost-service`/`add-task-control-api` precedent).
- **#13** — proposal aligned with design: status streams live; cost stays REST this round.
- **#14** — metrics registered in the existing `Metrics` struct + a `WSFanoutConsumerConnected` gauge.
- **#17** — PR-size split point pre-agreed (consumer+port, then hub+handler); noted in design + tasks.

Re-validated: `openspec validate add-realtime-gateway --strict` → valid.
