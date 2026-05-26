# Review: add-event-ingest-status-sync

Critical reviewer findings, verified against the actual codebase (api/, worker/). Each item is actionable; severity in brackets.

---

## S1 — Confirm and document that worker events are NOT wrapped in the API `Envelope` [Important]

**Files:** design.md (Context bullet "Wire format", D1/D7), specs §"Consume the task events stream", tasks.md 4.1.

The API's own `messaging.Publisher` wraps every published payload in `Envelope{msg_id, idempotency_key, payload, occurred_at}` (publisher.go L20-25, outbox_relayer.go L132-137). The worker, however, publishes the **bare** `TaskEvent` model via `model_dump_json()` (worker/core/publisher.py L81-105) — no `Envelope`, no `payload` wrapper, no `occurred_at`. The proposal's decode struct (`{task_id, version_id, run_id, seq, kind, payload, ts}`) is therefore **correct**, but the codebase contains a same-named-but-different `Envelope` shape that an implementer will very plausibly reach for by analogy with the Relayer.

**Recommendation:** Add an explicit one-line warning in design.md D7 / tasks.md 4.1: "The inbound `taskEventEnvelope` is the worker's bare `TaskEvent` JSON — do NOT reuse `messaging.Envelope` (that wrapper is API→worker only)." This prevents a wrong-by-default implementation.

---

## S2 — Idempotency-key field name and the `idempotency_key` header are NOT in the body [Minor]

**Files:** design.md Context "Wire format".

design.md says the worker emits "headers `idempotency_key=<run_id>:<seq>`, `message_id=<run_id>:<seq>`". Verified correct (publisher.py L91, L104). But note the dedupe key the consumer uses is the **body's** `(run_id, seq)`, not the header. The header is informational only. Fine as written, but tasks 4.1 should state the decode reads `run_id`/`seq` from the **body** (the unique index `task_events_run_seq_key` is on body-derived columns), so nobody wires dedupe off the AMQP header.

---

## S3 — `seq` and UUID JSON types: decode must parse strings, then convert to pgtype [Important]

**Files:** tasks.md 2.2, 2.3, 4.1.

`InsertTaskEventParams` expects `TaskID/VersionID/RunID pgtype.UUID`, `Seq int64`, `Payload []byte` (task_events.sql.go L23-30). The worker JSON serialises UUIDs as **strings** and `payload` as a **JSON object**, `seq` as a number. So the handler/service must:
- `uuid.Parse` each id string → `toPgUUID(...)` (the existing helper in service.go L451 is unexported; either export it, duplicate it, or do the conversion in the domain method where it already lives).
- Re-marshal `payload` (a `map[string]any` or `json.RawMessage`) to `[]byte` for the `Payload` column. Capturing `payload` as `json.RawMessage` during decode avoids a re-marshal round-trip and preserves the worker's exact bytes.

**Recommendation:** tasks.md 2.2/4.1 should specify `payload json.RawMessage` in the decode struct and that ids are parsed via `uuid.Parse` then `toPgUUID`. Note `toPgUUID`/`fromPgUUID` are currently **unexported** in package `task` (service.go L450-462) — since `IngestEvent` lives in the same package (`event_sync.go`), it can use them directly; the messaging handler cannot. Keep the conversion inside the domain method.

---

## S4 — No transient-vs-permanent DB error classifier exists; "transient error" is undefined [Critical]

**Files:** design.md D8, specs §"Transient processing failure is requeued", tasks.md 4.3.

The plan says "on transient error `delivery.Nack(false, true)` (requeue)" and "transient DB error → requeue". **There is no existing helper in the codebase to classify a pgx error as transient.** A naive "any non-nil error from `IngestEvent` ⇒ requeue" is dangerous: a **permanent** error (e.g. a CHECK-constraint violation because the worker sent a status outside the allowed set, or a malformed-but-decodable payload that fails JSONB cast) would requeue forever and wedge the queue — exactly what D8 claims to prevent.

The realistic failure modes for `IngestEvent`:
- **Transient** (requeue): `pgconn` connection errors, `context.DeadlineExceeded`, pool acquisition failures, serialization failures (SQLSTATE 40001 / 40P01).
- **Permanent** (DLQ): SQLSTATE 23xxx constraint violations (e.g. 23514 check_violation from a bad status string sneaking past the Go mapping, 23502 not-null). These must NOT requeue.

**Recommendation:** Add a task to write a small `isRetryable(err) bool` classifier (e.g. in the messaging handler or domain): treat `pgconn.PgError` class `08*`/`53*`/`40001`/`40P01` and `context.DeadlineExceeded` as retryable; everything else (including 23xxx) as permanent → DLQ. The current D8 binary "decode-fail = DLQ, else requeue" is too coarse and will loop poison messages that decode fine but can't ever commit. This is the single biggest implementation-correctness gap.

---

## S5 — `:execrows` return value and Querier wiring is correct, but confirm the invocation pattern [Minor]

**Files:** design.md D6, tasks.md 1.1-1.3.

Verified: `Queries` has `WithTx(tx) *Queries` (db.go L28) and `:execrows` queries return `(int64, error)` in this repo's sqlc output (e.g. `CountTasks` returns `(int64, error)`). The domain method must call `s.Queries.WithTx(tx)` exactly as `CreateTask`/`IterateTask` do (service.go L148, L218), open the tx via `s.Pool.BeginTx(ctx, pgx.TxOptions{})`, `defer tx.Rollback`, commit at end. tasks.md 2.3 says this. Good. Just make explicit that `UpdateVersionStatus`/`UpdateTaskStatus` are invoked on the **tx-bound** `q`, not `s.Queries` directly, or the insert+update won't share a transaction.

---

## S6 — `UpdateTaskStatus` 3-arg signature vs sqlc param naming [Minor]

**Files:** design.md D6 (SQL block), tasks.md 1.2, 2.3.

The proposed `UpdateTaskStatus` uses positional `$1=id, $2=status, $3=current_version`. sqlc will generate `UpdateTaskStatusParams{ID, Status, CurrentVersion}` (field order by `$n`). That's fine, but the design's prose "`UpdateTaskStatus(taskID, mappedTaskStatus, versionID)`" (D6, tasks 2.3) implies a positional Go call — sqlc generates a **params struct**, not positional args. Reword tasks 2.3 to "`UpdateTaskStatus(ctx, sqlc.UpdateTaskStatusParams{ID:…, Status:…, CurrentVersion:…})`" to match the generated API and avoid confusion. `current_version` is `pgtype.UUID` in the model, so pass `toPgUUID(versionID)`.

---

## S7 — Status string values must be the raw DB strings, and version `pending` is reachable [Important]

**Files:** design.md D6 mapping, specs §"Status events drive…", tasks.md 2.1.

The mapping table says version status maps 1:1 for `running/succeeded/failed/queued`. But:
- The worker only ever emits `payload.status ∈ {running, succeeded}` for `kind=status` (consumer.py L219, L277; the `succeeded` path is even marked "unreachable in scaffold"). It never emits `queued`, `pending`, `paused`, `cancelling`, `cancelled`. So `queued→pending` task mapping is **dead code for MVP** — acceptable as defensive, but the spec's claim that it's exercised should be softened (it isn't, today).
- The version CHECK constraint (migration 0002 L54-59) permits `pending,queued,running,paused,cancelling,cancelled,succeeded,failed`. The `versionStatusToTask` mapping in tasks 2.1 omits `pending` and `queued`→? It lists `queued→pending` and "running/paused/cancelled/succeeded/failed→1:1" but **not** `pending` itself and **not** `cancelling`. A `kind=status` with `payload.status="cancelling"` would be a recognised version status (and would pass the version CHECK) but has **no task mapping** (task CHECK forbids `cancelling`, L30). The mapping function must return `(_, false)` for `cancelling`/`queued`→task so `UpdateTaskStatus` is skipped, while `UpdateVersionStatus` still runs. tasks 2.1 lists `queued→pending` for the **task** side but the version side stays `queued`; make the two sides explicitly separate (version mapping is identity over the 8 statuses; task mapping is the partial function). State this clearly to avoid an implementer pushing `cancelling` into `tasks.status` and tripping 23514.

**Recommendation:** Split the mapping into two functions/tables: `versionTargetStatus(kind, payloadStatus) (Status, bool)` (identity for known version statuses; `error`→`failed`) and `taskStatusFromVersion(Status) (Status, bool)` (`queued`→`pending`, `cancelling`→none, `pending/running/paused/cancelled/succeeded/failed`→1:1). Reuse the existing `taskStatuses`/`activeStatuses` maps in status.go as the validity gate so an unknown string never reaches the DB.

---

## S8 — Out-of-order: a stale `running` after the version is `pending`→ premature transition? [Minor]

**Files:** design.md "Risks / Out-of-order", specs §"Terminal states".

The terminal guard only blocks regressions *out of* terminal states. It does **not** order `pending→running` vs a later `step`. That's fine for the states MVP emits (status:running is the first event). But note: the version is created as `pending` (service.go L389) and the run as `queued`. The very first event is `status:running`. There is no event that sets the version to `queued` (the worker never emits it), so `task_versions.status` jumps `pending → running` directly, and `tasks.status` `pending → running`. Confirm this is intended (it matches "task.status is derived state of current active version" and there's no observable `queued` phase on the version). No code change; just confirm the spec's Scenario "Running event marks version and task running" starts from `pending`, not `queued`, as the realistic precondition. Worth a one-line note so the integration test (7.1) seeds the version as `pending`.

---

## S9 — Metrics registration: add to BOTH the struct fields AND `reg.MustRegister(...)` [Minor]

**Files:** tasks.md 5.1, metrics.go.

`NewMetrics` registers every collector explicitly in the `reg.MustRegister(...)` block (metrics.go L106-120). tasks 5.1 says "registered in the existing private registry" — make explicit that each new collector must be (a) a struct field on `Metrics`, (b) constructed in the `&Metrics{...}` literal, and (c) listed in `reg.MustRegister(...)`. Missing (c) panics at startup only if the collector is used; missing it silently for a gauge means it never appears in `/metrics`. Name the metrics with `_total` suffix for counters per the existing convention (`http_requests_total`, `outbox_failed_total`). Proposed `EventsIngestedTotal` etc. already follow this.

---

## S10 — Consumer lifecycle / reconnect: `Connection.Channel()` returns a fresh channel; re-subscribe loop must re-open on close [Important]

**Files:** design.md D1, tasks.md 3.1-3.2.

Verified `Connection.Channel()` (connection.go L121-129) returns a **new** channel each call and errors if the conn is closed/closing; the `Connection` reconnects underneath via `watchLoop`. The publisher rebuilds its channel lazily on failure (publisher.go L62-76). The consumer must do the same: on `channel.NotifyClose` firing, loop and call `Connection.Channel()` again with backoff (the conn may not be back yet → `Channel()` returns "connection not established"; retry). tasks 3.2 says this. One concrete gap: the Relayer's `Run/Stop` shape uses an `atomic.Bool stopped` + ctx (outbox_relayer.go L70-90); mirror that exactly, and ensure `Stop()` is called **before** `mqConn.Close()` in main.go shutdown (S11) so the in-flight delivery isn't nacked into a closing channel.

---

## S11 — main.go wiring: consumer must start after topology declare and stop before MQ close [Important]

**Files:** tasks.md 6.1, 6.3, main.go.

Current shutdown order (main.go L224-248): HTTP → Relayer (`Stop()`, cancel, wait done) → publisher.Close → mqConn.Close → pool.Close. The consumer must be inserted: started after `DeclareTopology` succeeds (L141) and after `domainSvc` is constructed (L173) — note the domain service is currently built **after** the relayer goroutine starts (L172), so the consumer construction has to move below L181 or the relayer block has to move down. Stop the consumer in the same place the relayer is stopped (before `publisher.Close()`/`mqConn.Close()`), with its own `ctx`/`done` channel. tasks 6.3 covers the ordering intent but 6.1 should call out the **construction-order dependency** on `domainSvc` (currently created at L172-181, after the relayer is already running).

---

## S12 — Prefetch config: follow the existing env-tag convention, not a bare constant [Minor]

**Files:** tasks.md 6.2.

config.go uses struct tags `env:"NAME" envDefault:"X" yaml:"name"` (config.go L27-62). Add `EventConsumerPrefetch int \`env:"EVENT_CONSUMER_PREFETCH" envDefault:"16" yaml:"event_consumer_prefetch"\``. tasks 6.2 says "or reuse an existing knob" — there is **no** existing prefetch knob, so add a new one; update `config_test.go` (defaults table test exists for the others). Don't hardcode 16 in main.go.

---

## S13 — Ack-after-commit ordering and the duplicate-insert ambiguity [Important]

**Files:** design.md D3/D4, specs §"Redelivered event is a no-op".

`InsertTaskEvent` is `:exec` (returns only `error`), and `ON CONFLICT … DO NOTHING` means a duplicate insert returns **nil error, zero rows** — the consumer **cannot distinguish** "first insert" from "duplicate" from the insert alone. D4 relies on this being a structural no-op, which is correct *for persistence*. But two consequences must be spelled out:
1. On a duplicate redelivery, the CAS status updates still run (and correctly no-op under the terminal guard or because the value is unchanged). That's safe but means `EventStatusTransitionsTotal` could **double-count** a transition if the same `status:running` is delivered twice before the version reaches terminal (first delivery: pending→running, rows=1, +1 metric; redelivery: running→running, `status NOT IN terminal` still matches running, rows=1 again → another +1). The `WHERE status NOT IN (terminal)` does **not** prevent running→running re-application. Either add `AND status <> $2` to the CAS (so a no-op same-state update returns 0 rows and the metric is accurate), or accept metric over-count and document it. Recommend `AND status IS DISTINCT FROM $2` in both `UpdateVersionStatus` and `UpdateTaskStatus`.
2. Ack must follow `tx.Commit` success (D3) — verified the design states this; ensure the handler does NOT ack inside a deferred rollback path.

**Recommendation:** Add `AND status IS DISTINCT FROM excluded-status` to the two CAS queries (tasks 1.1/1.2) so `:execrows` truly reflects "a real transition happened", making `EventStatusTransitionsTotal` correct under redelivery.

---

## S14 — Test placement: integration tests live under `interfaces/http` / `persistence`, not `domain/task` [Minor]

**Files:** tasks.md 7.1-7.8.

Existing `//go:build integration` tests are in `internal/interfaces/http/*_integration_test.go` and `internal/infrastructure/persistence/*_integration_test.go`, each spinning a Postgres container and reusing a shared suite/helpers in the same package (task_reads_integration_test.go header). The proposed ingest integration tests exercise the **domain service** (`IngestEvent`) directly against a DB, so they'd be a **new** integration-test home (e.g. `internal/domain/task/event_sync_integration_test.go`) needing its own container bootstrap, or they should live where the existing container harness already is. tasks.md 7.x doesn't say which package/harness. **Recommendation:** specify the file location and whether it reuses the existing container suite or stands up its own; otherwise the build-tag + DB-fixture wiring is undefined. Also: there is currently no AMQP-level integration harness, so 7.7 (`TestMalformedToDLQ`) should test the **handler's nack decision** via a fake `amqp.Delivery`/handler return contract rather than asserting real DLQ routing (the design's "assert via metric scrape or handler return contract" hedge is right — pick the handler-return-contract approach and make the handler unit-testable without a broker).

---

## S15 — `error` event payload has no `status`; ensure decode doesn't require `payload.status` [Minor]

**Files:** tasks.md 2.4, 4.1, specs §"Error events".

`kind=error` payload is `{code, message}` (consumer.py L293, L315) — no `status` field. The decode/branch logic must branch on `kind` **first**: `kind=error` → target `failed` (ignore payload.status); `kind=status` → read `payload.status`; else persist-only. tasks 2.4 has this right; just ensure the envelope decode treats `payload` as opaque `json.RawMessage`/`map[string]any` and only inspects `payload.status` on the `status` branch, so an `error` event (no `status` key) doesn't get mis-routed to "unknown status persist-only".

---

## S16 — Spec scenario "releasing the one_active_version_per_task index slot" is an emergent DB effect, not consumer behaviour [Minor]

**Files:** specs §"Succeeded event marks version and task succeeded" (3rd bullet).

The `is_active` column is `GENERATED ALWAYS AS (status IN (pending,queued,running,paused,cancelling)) STORED` (migration 0002 L49-51). When the consumer sets `status='succeeded'`, `is_active` flips to false automatically and the partial unique index slot frees. The scenario asserting "the version leaves the active set, releasing the index slot" is correct but is a **side effect of the generated column**, not something the consumer does. Fine to keep as an observable assertion (the 7.2 test `GetActiveVersionByTask` returning no row validates it), just don't let an implementer think they must write `is_active`.

---

## Summary of severities

- **Critical:** S4 (no transient/permanent error classifier — risk of poison-message requeue loop).
- **Important:** S1 (Envelope confusion), S3 (UUID/payload decode + unexported helpers), S7 (split version vs task status mapping; `cancelling` would trip task CHECK), S10 (consumer reconnect), S11 (main.go construction/stop ordering), S13 (CAS metric double-count + `IS DISTINCT FROM`).
- **Minor:** S2, S5, S6, S8, S9, S12, S14, S15, S16.

No reject-worthy issues: the architecture (idempotent consumer over existing quorum queue, CAS terminal guard in SQL, domain-service transition, task_runs left worker-owned) is sound and matches the codebase. The gaps are in error-classification precision (S4), the status-mapping edge cases (S7), and a handful of "match the existing pattern exactly" details.

---

## Evaluation & disposition (proposal author)

All 16 verified against code (`toPgUUID` unexported at `service.go:451`, `WithTx` at `db.go:28`, `InsertTaskEvent` takes a params struct, separate `tasks_status_check` vs `task_versions_status_check`). **All accepted; none reject-worthy.**

| # | Severity | Disposition |
|---|----------|-------------|
| S4 | Critical | ✅ design D8 rewritten into malformed / permanent(23xxx→DLQ) / transient(08*/53*/40001/40P01/deadline→requeue) + default-DLQ; new task 4.2 `isRetryable`; spec gains "Permanent processing failure is dead-lettered" scenario; test 7.9. |
| S7 | Important | ✅ D6 split into `versionTargetStatus` + `taskStatusFromVersion`; `cancelling`/`queued`→task skipped; tasks 2.1 + test 2.5/7.x. |
| S13 | Important | ✅ `AND status IS DISTINCT FROM $2` added to both CAS queries (1.1/1.2); transition metric now accurate; test 7.5/7.7. |
| S1 | Important | ✅ D7 "do NOT reuse `messaging.Envelope`" note; tasks 4.1. |
| S3 | Important | ✅ D7 decode-types note (`json.RawMessage`, `uuid.Parse`→`toPgUUID` inside domain method); tasks 2.2/2.3. |
| S10 | Important | ✅ D1 reconnect/Run-Stop note; tasks 3.1/3.2. |
| S11 | Important | ✅ tasks 6.1/6.3 construction-order + stop-before-close. |
| S2 | Minor | ✅ D7 dedupe-off-body note; tasks 4.1. |
| S5 | Minor | ✅ D7 tx-bound `WithTx(tx)` note; tasks 2.3. |
| S6 | Minor | ✅ D7/tasks 2.3 params-struct call form. |
| S8 | Minor | ✅ design risk "No observable queued phase"; test 7.2 seeds `pending`. |
| S9 | Minor | ✅ tasks 5.1 three-step registration. |
| S12 | Minor | ✅ tasks 6.2 env-tag `EVENT_CONSUMER_PREFETCH`. |
| S14 | Minor | ✅ tasks 7.1 names test home; 4.1/7.9 handler-return-contract (no broker). |
| S15 | Minor | ✅ D7 branch-on-kind-first; tasks 2.4. |
| S16 | Minor | ✅ spec scenario reworded (generated `is_active` side effect); D6 note. |
