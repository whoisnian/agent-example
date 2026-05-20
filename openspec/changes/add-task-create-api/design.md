# Design — add-task-create-api

## Context

The `task-data-model` migration set has shipped: `tasks`, `task_versions` (with the generated `is_active` column and the `one_active_version_per_task` partial unique index), `task_runs`, plus all read paths under `api/internal/infrastructure/persistence/sqlc/`. The `api-messaging` capability already provides a Publisher with confirms, an Outbox Relayer with advisory-lock leadership, and a topology that declares `task.exchange` (topic, durable). `api-bootstrap` already defines the unified `{code, message, data, trace_id}` envelope and the panic / health / metrics scaffolding.

What is missing is the first set of write endpoints that produce active versions. Until those exist, nothing inserts into `outbox`, nothing is published to `task.exchange`, the worker dispatcher has nothing to consume, and the web client has no `POST /tasks` to call. This change fills exactly that gap with the two endpoints that share a single transactional flow: `POST /api/v1/tasks` and `POST /api/v1/tasks/{task_id}/iterate`.

The architecture document (`docs/ARCHITECTURE.md §5.1`, `§5.3`, `§6.1`, `§6.4`) prescribes the request shapes, the MQ payload, and the per-step transactional sequence. This design adopts those prescriptions verbatim and focuses decisions on places the architecture leaves implicit.

## Goals / Non-Goals

**Goals**
- Ship `POST /api/v1/tasks` and `POST /api/v1/tasks/{task_id}/iterate` end to end, including request validation, transactional persistence, outbox emission, and the unified error envelope.
- Make the DB-enforced task-level mutex (`one_active_version_per_task`) the sole correctness boundary; treat any application-level pre-check as a friendliness optimization, never as a substitute.
- Establish the Domain Service + Application Service split for the task aggregate (`internal/domain/task/`, `internal/application/task/`) so subsequent write endpoints (rollback, control) extend the same pattern.
- Emit a canonical execute payload that the worker will consume unchanged in `add-worker-code-agent`.

**Non-Goals**
- Read endpoints (`GET /tasks`, `GET /tasks/{id}`, `GET /tasks/{id}/versions`, etc.) — deferred to a follow-up `task-read-api` change.
- Rollback (`POST /tasks/{id}/rollback`, both `switch` and `branch` modes) — deferred. Branch rollback shares the iterate transaction shape but with target-version resolution semantics that warrant their own design.
- Control endpoints (`POST /tasks/{id}/control` pause / resume / cancel) — deferred to `task-control-api`.
- Cost queries — deferred to `add-task-cost-api`.
- Worker / web side implementations — separate changes.
- Idempotency-Key request header for client retries — recorded as Open Question; MVP relies on UUIDv7 server-side IDs only.
- Quotas, per-tenant concurrency limits, and `pending_throttled` (architecture §7.5) — Post-MVP.

## Decisions

### D1. Two endpoints share one Domain Service

`internal/domain/task/service.go` exposes two methods that internally call a single private `createActiveVersion(tx, params)` helper. The helper performs the version-row insert, the run-row insert, the outbox row insert, and the `tasks.current_version` update. The `CreateTask` method additionally inserts the `tasks` row before calling `createActiveVersion`; `IterateTask` resolves the base version and updates `tasks.status` to `pending` instead.

**Rationale.** The two endpoints differ only in (a) whether the parent `task_versions` row needs to be created and (b) how the parent / base is resolved. Sharing one helper keeps the SQLSTATE-to-409 translation, the outbox payload assembly, and the idempotency-key derivation in exactly one place.

**Alternative considered.** Two completely independent flows. Rejected because the conflict-translation logic and outbox payload are non-trivial and the duplication invites drift.

### D2. DB unique index is mutex truth; app-level FOR UPDATE is fast-path only

The control flow inside `IterateTask` (active-status set defined once in `status.go`, see Task 2.2):

1. `BEGIN`
2. `SELECT id, status, current_version FROM tasks WHERE id = $1 FOR UPDATE` — fails with `task_not_found` if no row.
3. If `tasks.status` is in the active set, short-circuit: read the active version row (one exists, by the partial-unique invariant) and return `409 active_version_exists` with `active_version_id` + `active_version_status`. `ROLLBACK`.
4. Resolve base version (request `base_version_id` if present, else `current_version`); 404 `version_not_found` if missing.
5. `SAVEPOINT sp_insert_version` then `INSERT INTO task_versions … RETURNING *`.
   - On SQLSTATE `23505` with constraint name `one_active_version_per_task`: `ROLLBACK TO SAVEPOINT sp_insert_version` (which un-aborts the parent tx, so subsequent queries are legal), read the active version row via `GetActiveVersionByTask`, then `ROLLBACK` the outer tx and return `409 active_version_exists`.
   - On any other SQLSTATE: roll back the outer tx and return `internal_error`.
6. `INSERT INTO task_runs …`, `INSERT INTO outbox …`, `UPDATE tasks SET status='pending', current_version=$new`.
7. `COMMIT`.

**Rationale.** Step 3 covers ~100% of real-world conflicts and lets us return rich `data` without depending on Postgres error string parsing. Step 5 is the DB-side safety net that catches the narrow window where two requests both saw `tasks.status` non-active and tried to insert simultaneously; the error-translation path needs to exist anyway because external writers (CLI scripts, future services) might bypass step 3 logic. AGENTS.md §6 explicitly forbids bypassing the unique index.

**Why a savepoint, not "re-read in the same tx" nor "open a new tx".** After a constraint violation in a plain transaction, PostgreSQL marks it aborted (`25P02 in_failed_sql_transaction`) and ignores all further commands until end-of-block, so we cannot run `GetActiveVersionByTask` on the same tx without first releasing the failed statement. A savepoint scopes the abort to the INSERT only; `ROLLBACK TO SAVEPOINT` resurrects the outer tx and the `FOR UPDATE` row lock from step 2, guaranteeing the active version we then read is the same one the unique index detected. Opening a second read-only tx would lose that lock and read a snapshot taken after the conflicting committer, which is still correct but pays an extra round-trip for no extra safety.

**Alternative considered.** Skip step 3 entirely and rely only on step 5's SQLSTATE detection. Rejected because the friendly error path needs the active version's id+status, which is awkward to fetch after a rolled-back transaction.

### D3. Idempotency key for the first run = `run_id` UUID

Architecture §5.3 says `execute.<task_type>.<lane>` carries `idempotency_key = <run_id>`. We mirror that: `task_runs.idempotency_key` is set to the textual UUID of `task_runs.id` itself. This satisfies the existing schema's `UNIQUE (idempotency_key)` constraint without introducing a parallel ID.

**Rationale.** Architecture treats `run_id` as the natural idempotency boundary. The worker side (`worker-messaging` claim flow) and the existing `GetRunByIdempotencyKey` sqlc query already assume this shape.

### D4. ID generation: UUIDv7 server-side

All ids minted by this service (`tasks.id`, `task_versions.id`, `task_runs.id`, outbox `aggregate_id`) use UUIDv7 generated by `github.com/google/uuid.NewV7()`. Created entities are therefore time-orderable, which matters for `ORDER BY id` queries on `task_events` / `cost_events` later.

**Alternative considered.** UUIDv4. Rejected — random ordering wastes B-tree fill and is harder to debug.

### D5. Single transaction; outbox is async

All four inserts plus the `tasks.current_version` update happen in one transaction. The HTTP handler returns `201` once the transaction commits — it does NOT wait for MQ publish. The Outbox Relayer (already running per `api-messaging`) picks up the row on its next scan tick (default 1s) and publishes via the confirms-enabled Publisher.

**Rationale.** Architecture §6.1 invariant 1: "任务创建成功 ≡ MQ 必然能收到该任务". The Outbox pattern ensures durability; synchronous publish in the request path would couple HTTP latency to broker latency and risk partial state on broker hiccup.

### D6. Lane resolution

Request body MAY include `"lane": "<string>"`. When the field is absent (omitted from JSON or supplied as `null`), the service uses the `DEFAULT_LANE` environment variable (default literal `"default"`). Empty strings, whitespace-only values, and pattern-violating values are NOT silently fallback'd — they fail validation per D12 with `400 invalid_input`. The resolved value is written to `outbox.topic` (which holds the AMQP routing key) as `execute.<task_type>.<lane>` and is also embedded in the JSON payload's `lane` field for the worker's convenience.

**Rationale.** MVP has only the `default` lane; introducing a lanes registry table is premature. Centralising the fallback in one env var keeps the option open for future lane gating without touching the API contract.

### D7. 409 envelope

```json
{
  "code": "active_version_exists",
  "message": "task has an active version; wait for it to finish or cancel it first",
  "data": { "active_version_id": "<uuid>", "active_version_status": "running" },
  "trace_id": "<id>"
}
```

The HTTP status is `409 Conflict`. The `data.active_version_status` echoes whatever value the active version currently has (`pending|queued|running|paused|cancelling`). Architecture §5.1 example matches this exactly.

### D8. Outbox payload shape

The outbox row stores:
- `aggregate = "task_version"`
- `aggregate_id = <new version_id>`
- `topic = "execute.<task_type>.<lane>"` (used directly as AMQP routing key by the existing Publisher → Relayer)
- `payload` JSONB matching architecture §5.3 verbatim:
  ```json
  {
    "msg_id": "<new uuidv7>",
    "idempotency_key": "<run_id>",
    "task_id": "<task_id>",
    "version_id": "<version_id>",
    "run_id": "<run_id>",
    "attempt_no": 1,
    "task_type": "<task_type>",
    "prompt": "<prompt>",
    "params": { ... },
    "parent_version_id": "<base_version_id>|null",
    "parent_artifact_root": "<oss_uri>|null",
    "deadline_ts": <unix_seconds|null>
  }
  ```
- `status = 'pending'`, `attempts = 0`, `next_retry_at = NULL`.

**`parent_artifact_root` resolution.** The single source of truth is `task_versions.artifact_root` of the base version row read at iterate time. If that column is `NULL` (the common case: base version finished before producing any artifacts, or was just rolled-back-from), the payload field MUST be JSON `null` — the worker side treats `null` as "start from an empty workspace" and MUST NOT raise. The `artifacts` table is NOT consulted; populating `task_versions.artifact_root` is the responsibility of the future worker-event-loop change that ingests `artifact` events. On `POST /api/v1/tasks` (no base), the field is always `null`.

**`attempt_no`.** Always `1` for both endpoints — this proposal only emits first-attempt execute messages; retries are produced by the worker-side claim flow on different `run_id`s.

`deadline_ts` is computed as `now() + DEFAULT_TASK_DEADLINE` (env, default `60m`). Architecture §13 lists deadline tuning as Open; we use a fixed default for MVP.

### D9. `tasks.status` writes

- Create: insert `tasks.status = 'pending'`.
- Iterate: `UPDATE tasks SET status='pending', current_version=$new, updated_at=now()` in the same transaction. (The architecture treats `tasks.status` as derived; the API service is the writer.)

Terminal-state transitions are the responsibility of the worker → API event loop (separate future change). This proposal only writes `pending`.

### D10. Error catalog additions

| Code                    | HTTP | When                                                                 |
|-------------------------|------|----------------------------------------------------------------------|
| `invalid_input`         | 400  | Missing required field; field fails validation rule                   |
| `task_not_found`        | 404  | Iterate target task_id has no row                                     |
| `version_not_found`     | 404  | `base_version_id` supplied but unknown / belongs to a different task  |
| `active_version_exists` | 409  | Task already has an active version (app-level or DB-level detection)  |

These slot into `internal/interfaces/http/errors.go` alongside `internal_error` from `api-bootstrap`. No change to envelope shape.

### D11. Transaction isolation

`READ COMMITTED` (PG default) is sufficient. The unique partial index serialises the only correctness-relevant write. The `SELECT … FOR UPDATE` on the `tasks` row serialises concurrent iterate requests targeting the same task at the app layer.

**Alternative considered.** `SERIALIZABLE`. Rejected — overhead is unjustified given the unique index guarantee.

### D11.5. Active-status set

Defined exactly once in `internal/domain/task/status.go` (Task 2.2) as the literal set `{pending, queued, running, paused, cancelling}` ordered by state-machine progression. Every other reference in this design (D2, D9) MUST import that constant rather than restate the list.

### D12. Validation rules (request bodies)

Create:
- `title` — required, 1..200 chars, trimmed
- `task_type` — required, kebab-case ASCII, max 64 chars, matches `^[a-z][a-z0-9-]{0,63}$`
- `prompt` — required, 1..16384 chars
- `params` — optional, default `{}`, max serialized size 32 KiB
- `lane` — optional, ASCII slug `^[a-z0-9-]{1,32}$`, default from env

Iterate:
- `prompt` — required, 1..16384 chars
- `params` — optional, same as above
- `base_version_id` — optional UUID; when present, MUST belong to the path `task_id` and the service uses it; when absent, the service uses `tasks.current_version` (404 if NULL).

### D13. Observability

- Counter `tasks_created_total{task_type=...}` — incremented on commit of `CreateTask`.
- Counter `tasks_iterated_total{outcome="success"|"conflict"|"not_found"|"invalid"}` — every terminal outcome of `IterateTask` (whether or not the tx committed).
- Log fields on every request log: `task_id`, `version_id` (success), `base_version_id` (iterate), `outcome` for non-2xx.
- OpenTelemetry span per handler named `POST /api/v1/tasks` and `POST /api/v1/tasks/{task_id}/iterate`. Outbox publish happens later under the Relayer's span — out of scope here.

## Risks / Trade-offs

- **[Risk] App-level FOR UPDATE serialises iterate calls per task.** → Acceptable: same task cannot have parallel active versions anyway, so the lock contention is by design. Distinct tasks remain fully parallel.
- **[Risk] Outbox row written but transaction commits before Relayer picks it up.** → Acceptable and intentional: that is the durability contract. The metric `outbox_pending` (already specified by `api-messaging`) will catch a stuck Relayer.
- **[Risk] Two-stage conflict detection (app + DB) could disagree under TOCTOU.** → Mitigated by Step 5's SQLSTATE handler which produces the same 409 envelope.
- **[Risk] `tasks.status` being writer-managed diverges from "derived" per architecture §4.3.** → Acceptable for MVP; documented here so the future Domain Service that introduces state-machine UPDATE methods picks up the same writer responsibility.
- **[Trade-off] No `Idempotency-Key` header.** → A retried `POST /tasks` will create a duplicate task. Documented as Open Question.
- **[Trade-off] `deadline_ts` is a fixed default.** → Workers can self-terminate but cannot respect a user-supplied deadline yet.

## Migration Plan

This is an additive HTTP capability against an already-migrated database. No DDL changes. Rollout:
1. Land the sqlc query addition (`InsertOutbox`).
2. Land the domain / application packages + handlers.
3. Register the routes.
4. Deploy. The Outbox Relayer (already running) will immediately start publishing the first execute messages once any client calls the endpoint.

Rollback is "revert the commit"; the `tasks` / `task_versions` rows written in the interim become orphaned but harmless (the worker side is not yet consuming).

## Open Questions

1. **Idempotency-Key request header.** Should we accept one on `POST /tasks` and `POST /iterate` to make client retries safe? Likely yes, but probably a small follow-up (`add-task-write-idempotency`).
2. **Per-task deadline override.** Should request body carry `deadline_seconds`? Defer — the worker side does not enforce yet.
3. **`task_type` allow-list.** Should the API reject `task_type` values that no worker advertises? MVP says no (workers come and go); future change could surface a registry of advertised task types and reject unknowns at write time.
4. **`tasks.status` writer migration.** Architecture §4.3 names `tasks.status` as a derived view of the active version's run status; this change writes it imperatively from the API for MVP (D9). The proper owner is a future state-machine `Domain Service` introduced alongside the worker-emitted state transitions. Concrete handoff: `task-control-api` (pause / resume / cancel) or `add-worker-event-loop` — whichever lands first MUST take over `tasks.status` writes and downgrade this proposal's UPDATEs to no-ops. Tracked here so the debt is not lost.
