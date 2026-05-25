## Context

`add-task-create-api` shipped the write side (`POST /tasks`, `POST /tasks/{id}/iterate`) with a `Service` that owns transactions, the Outbox, and the task-level mutex. The schema (`add-task-domain-schema`) already defines `tasks`, `task_versions`, `task_runs`, `task_events`, and `task_costs`, and several read queries already exist in `api/queries/` (`GetTaskByID`, `ListTasks`, `ListVersionsByTask`, `GetTaskVersionByID`, `GetTaskCost`, `GetVersionCost`, `ListEventsAfter`).

What's missing is the HTTP read surface. The frontend `TaskList` / `TaskDetail` / `VersionTree` pages (architecture §3, §5.1) need: a paginated task list with a cumulative cost column, a task detail with the current-version summary, the version tree, a version detail with its runs, and an event-stream backfill for WS reconnection.

Constraints carried from the codebase:
- Unified envelope `{code, message, data, trace_id}`; success `code` is the number `0` via `OK`/`JSON` helpers; errors via `Error`.
- DDD layering interfaces ↔ application ↔ domain ↔ infrastructure; SQL only through sqlc.
- Auth is not built yet; the write endpoints scope to `DevTenantID`/`DevUserID` injected on the handler struct. Reads adopt the same identity.
- `slog` lines must carry `trace_id` / `task_id`.

## Goals / Non-Goals

**Goals:**
- Five owner-scoped read endpoints returning the unified envelope: list tasks, get task, list versions, get version, list version events.
- A single, reusable `CostSummary` DTO embedded in list rows, task detail, and version nodes, sourced from `task_costs`, that degrades gracefully to all-zero when the Cost Service hasn't populated anything.
- Strict tenant/user ownership on every read: rows the caller does not own return `404` (never `403`), so existence is not leaked.
- No DB schema change: reuse existing indexes; add only sqlc queries.

**Non-Goals:**
- Cost *breakdown* endpoints (`/tasks/{id}/cost`, `/versions/{id}/cost`, `/me/cost`) and `/pricing` — owned by `add-task-cost-api`.
- Artifacts (`/versions/{id}/artifacts`, `/artifacts/{id}/presign`) and uploads — separate proposal.
- WebSocket realtime delivery — this change provides only the HTTP *backfill* the WS client uses to close gaps.
- Auth / JWT — still the dev identity; `403` and real tenancy arrive with the auth proposal.
- Server-side tree nesting, full-text search, arbitrary sort/filter beyond the existing `status` filter.

## Decisions

### D1 — A queries-only `ReadService` in `domain/task`, separate from the write `Service`

Reads need no `pgxpool.Pool`, no transactions, no `Clock`/`IDGen`. Folding them into the write `Service` would bloat its dependencies and blur the "this struct owns the mutex/Outbox" story. Introduce `ReadService{ Queries *sqlc.Queries }` in the same package, owning DTO assembly and ownership checks. The application layer (`apptask`) gets thin read methods that forward to it, mirroring the existing command structure.

**Identity type (S8):** define a single value type `Owner{ TenantID, UserID uuid.UUID }` in `domain/task` and pass it to every `ReadService` method — do **not** also accept loose `tenant_id`/`user_id` params, and do not invent a second identity convention. The HTTP layer keeps reading `DevTenantID`/`DevUserID` (consistent with the existing `TaskHandlers`) and constructs the `Owner` at the interface boundary. Ownership holds iff `row.TenantID == owner.TenantID && row.UserID == owner.UserID` (see D2).

*Alternative considered:* call sqlc directly from the HTTP handler. Rejected — it leaks persistence types into `interfaces/` and duplicates ownership logic across five handlers.

### D2 — Ownership enforced in the read path; not-owned ⇒ 404, never 403

`GetTaskByID` / `GetTaskVersionByID` are not tenant-scoped at the SQL level (the schema has no tenants table yet). The `ReadService` therefore compares `row.TenantID`/`row.UserID` against the caller identity and returns the existing `ErrTaskNotFound` / `ErrVersionNotFound` when they differ — identical to the "no such row" case. This is deliberate: a `403` would confirm the resource exists. For versions, resolve `version → task_id → GetTaskByID → ownership check` so a version is reachable only through a task the caller owns.

The owning columns are `pgtype.UUID` in the generated models (`Task.TenantID`/`Task.UserID`), while the caller identity is `uuid.UUID`. The comparison MUST convert via `uuid.UUID(row.TenantID.Bytes)` and require `row.TenantID.Valid` (the columns are `NOT NULL`, so `Valid` always holds; the explicit check guards against an all-zero identity silently matching an all-zero column). Ownership holds only when **both** `tenant_id` *and* `user_id` match — so a same-tenant, different-user task is `404`, not visible. Fail-closed: if a version row loads but its owning task cannot be loaded for any reason (impossible under the `task_versions.task_id → tasks.id` FK, but defended anyway), map to `version_not_found`, never `500` or `403`.

*Alternative considered:* push `tenant_id`/`user_id` into the WHERE clause of every read query. Reasonable, but it forces every existing query (`GetTaskByID`, `GetTaskVersionByID`) to grow params and would still need the version→task hop. Keeping the check in the service is simpler for MVP and trivially swappable when real auth lands; the trade-off (one extra row read before rejecting) is negligible.

### D3 — Offset pagination with a `{items, page, page_size, total}` envelope

`GET /tasks` accepts `page` (1-based, default 1) and `page_size` (default 20, clamped to [1, 100]); the service translates to the existing `ListTasks` `LIMIT/OFFSET`. A new `CountTasks` query (same `tenant_id/user_id/status` predicate) supplies `total`. `data = {items: [...], page, page_size, total}`. The optional `status` filter reuses `ListTasks`'s existing `sqlc.narg('status')`.

**Input handling (closes the offset foot-guns):**
- Numeric params (`page`, `page_size`) that fail to parse as integers MUST be rejected with `400 invalid_input` naming the field — consistent with the malformed-UUID rule. Parse errors are hard errors.
- In-range coercion, not rejection, for out-of-range values: `page < 1` is clamped to `1` (so `OFFSET = (page-1)*page_size` is never negative); `page_size` is clamped to `[1, 100]`. The echoed `data.page`/`data.page_size` reflect the effective (clamped) values.
- **`status` validity (S7):** when present, `status` MUST be one of the six *task* statuses (`pending`, `running`, `paused`, `cancelled`, `succeeded`, `failed`) — note this is **not** `task.activeStatuses`, which is the version-active set and includes the version-only `queued`/`cancelling`. An unrecognised `status` (including the version-only ones) MUST return `400 invalid_input`, not a silent empty `200`. A dedicated task-status validity set is added next to `task.Status`.

*Alternative considered:* keyset/cursor pagination. Better at scale but the existing query is offset-based and MVP task counts are small; revisit under a perf proposal.

### D4 — Cost summary embedded via batched queries (no N+1), `amount_usd` as a decimal string

A shared `CostSummary { amount_usd, input_tokens, output_tokens, cached_tokens, tool_calls, wall_time_ms }` is embedded in list rows, task detail, version nodes, and version detail.

**`amount_usd` wire contract (S1):** `task_costs.amount_usd` is `NUMERIC(18,8)` and surfaces as `pgtype.Numeric` in the generated models — which does **not** marshal to a clean JSON number, and a `float64` would silently round an 8-dp money value. So `CostSummary.amount_usd` is a **decimal string preserving full 8-dp scale** (e.g. `"0.62000000"`, `"0.00000000"` when absent). The DTO never embeds `pgtype.Numeric`; a helper `numericToDecimalString(pgtype.Numeric) string` performs the conversion (invalid/NULL ⇒ `"0.00000000"`). The token/`tool_calls`/`wall_time_ms` fields are plain integers (already `int64`/`int32` in the models).

**Batching (no N+1, fixing the version-tree gap):**
- **Task detail**: reuse `GetTaskCost` (already COALESCEs to zero).
- **List**: a new `ListTaskCostsByTasks` (`SUM(...) ... WHERE task_id = ANY($1::uuid[]) GROUP BY task_id`) fetched once for the page's task ids, then mapped onto rows; tasks with no `task_costs` rows get an all-zero summary.
- **Version tree**: a new `ListVersionCostsByTask` (`SELECT ... FROM task_costs WHERE task_id = $1`) fetched **once** for the whole tree (covered by the existing `task_costs (task_id)` index; `version_id` is the PK), then mapped onto nodes by `version_id`. This replaces the earlier per-node `GetVersionCost` loop, which would have been N+1 on the most version-dense endpoint.
- **Version detail (single version)**: reuse `GetVersionCost` (one row, no loop).
- Zero-fill (missing `task_costs` row ⇒ all-zero summary) happens in Go for every batched path.

### D5 — Cost is best-effort; reads never fail because cost is absent

`task_costs` is written by the not-yet-built Cost Service, so today every summary is `0`. The contract states cost values are eventually-consistent and a missing row means "zero so far," not an error. This lets `add-task-read-api` ship before `add-task-cost-api` without a stub.

### D6 — Version tree returned as a flat, version_no-ordered array

`GET /tasks/{id}/versions` returns `data = {items: [...]}` ordered by `version_no ASC` (existing `ListVersionsByTask`), each node = `{id, parent_id, version_no, status, is_active, artifact_root, created_at, cost}`. The client (react-flow) builds edges from `parent_id`. Server-side nesting is avoided so the wire shape stays stable and layout stays a client concern.

The tree node is deliberately **lightweight**: it omits `prompt` and `params` (potentially large user input) — those appear only in version *detail* (D8). `is_active` is a generated column typed `*bool` in the model (sqlc cannot prove a `GENERATED` column `NOT NULL`); the mapping dereferences it as `nil → false` so the DTO field is always a concrete boolean. `parent_id` renders as JSON `null` for root versions. Per-node cost comes from the batched `ListVersionCostsByTask` (D4), not a per-node query.

### D7 — Event backfill rides the existing `(task_id, id)` index — no new migration

`GET /versions/{version_id}/events?after_id=&limit=`: `after_id` default `0`, `limit` default 200, clamped to [1, 1000]; non-integer `after_id`/`limit` are rejected with `400 invalid_input` (same parse-vs-clamp rule as D3). The handler first resolves the version (which also does the D2 ownership check, yielding `task_id`), then calls a new `ListVersionEventsAfter(task_id, version_id, after_id, limit)` whose predicate is `task_id = $1 AND version_id = $2 AND id > $3 ORDER BY id ASC LIMIT $4`. The `task_id` equality + `id` range is served by the existing `task_events (task_id, id)` index; `version_id` is a cheap residual filter. Response `data = {items: [...], next_after_id}` where `next_after_id` is the last returned `id` (or the input `after_id` when empty) for the next poll.

**Cursor semantics — `id`, not `seq` (S3):** the cursor is the global `task_events.id` (`BIGSERIAL` PK), which is monotonic across all runs/versions. It is **not** the per-frame `seq`, which is only `UNIQUE (run_id, seq)` and therefore not a usable cross-run cursor. ARCHITECTURE §5.2's reconnection sketch ("client records the max `seq`, backfills via `/events?after_id=`") is loosely worded: a `seq` cannot be passed as `after_id` correctly. To let the WS client reconcile, each `EventItem` exposes **both** `id` and `seq`; the realtime client must track the `id` it last saw (or the WS gateway must surface `id` alongside `seq` in its push frame). See Open Question #3 — fully resolving the realtime cursor belongs to the realtime-gateway proposal, not here.

**`EventItem` shape (S4):** `{id, version_id, run_id, seq, kind, payload, created_at}` where `run_id` is **nullable** (`task_events.run_id` has no `NOT NULL`) and renders as JSON `null` when absent, and `payload` (JSONB / `[]byte`) is passed through as **raw JSON** (`json.RawMessage`), never a base64 string.

*Alternative considered:* a dedicated `task_events (version_id, id)` index + a version-only query. Cleaner long-term, but adds a migration and touches the `task-data-model` capability for a path whose per-version event volume is bounded in MVP. Deferred; noted as a perf follow-up.

### D8 — Version detail includes its runs

`GET /versions/{version_id}` returns `data = {version, runs, cost}`. Unlike the lightweight tree node, the detail `version` is the **full** row — including `prompt` and `params` — plus the dereferenced `is_active` (`*bool` → bool, `nil → false`); `params` (JSONB) renders as raw JSON. A new `ListRunsByVersion` (`WHERE version_id = $1 ORDER BY attempt_no ASC`) supplies `runs`; each run exposes `{id, attempt_no, status, started_at, ended_at, last_heartbeat, error}`, where `error` (nullable JSONB / `[]byte`) is passed through as raw JSON and renders as JSON `null` when the column is `NULL`. `runs` is always an array (empty `[]`, never `null`). This gives `TaskDetail` retry history without a second request.

### D9 — A separate `TaskReadHandlers` struct, wired alongside the write handlers

Add `task_reads.go` with `TaskReadHandlers{ App, Logger, DevTenantID, DevUserID }` and a `Register(r *gin.RouterGroup)` that mounts the five GETs on the same `/api/v1` group created in `server.go`. Keeping reads in their own struct/file mirrors the read/write split and avoids growing the write handler. `server.go`'s `ServerDeps` gains an optional `*TaskReadHandlers`.

### D10 — No new Prometheus counters for reads

Per AGENTS.md, the observability rule targets state transitions and external calls; reads are neither. The existing HTTP middleware already records request metrics and the access log carries `trace_id`. Handlers add structured `slog` lines on not-found/error outcomes (with `trace_id`/`task_id`/`version_id`) but no bespoke counters, to avoid metric sprawl.

## Risks / Trade-offs

- **403-vs-404 information leak** → always return 404 for not-owned rows (D2); covered by a spec scenario.
- **Cost summary reads 0 and a client treats it as "free"** → the contract documents cost as eventually-consistent best-effort (D5); the frontend proposal must label it accordingly. Acceptable for MVP.
- **N+1 cost lookups on the list** → mitigated by the single batched `ListTaskCostsByTasks` per page (D4).
- **Event query lacks a `version_id` index** → mitigated by anchoring on the `(task_id, id)` index with `version_id` as a residual filter (D7); revisit if per-version event counts grow.
- **Offset pagination degrades on deep pages** → acceptable at MVP scale; keyset is a later perf change (D3).
- **List is not a consistent snapshot** → `ListTasks` and `CountTasks` are two separate statements (no wrapping transaction), so under concurrent inserts `total` and `items` can momentarily disagree. Benign at MVP scale; if it ever matters, wrap both in a single read-only `REPEATABLE READ` transaction. Documented, not fixed.
- **`amount_usd` as a string forces client-side parsing** → deliberate (D4): preserves 8-dp money precision that a JSON `float64` would lose. Clients parse the decimal string.

## Migration Plan

Purely additive: append four sqlc queries (`CountTasks`, `ListTaskCostsByTasks`, `ListRunsByVersion`, `ListVersionEventsAfter`), regenerate sqlc, add the read service/handlers, register routes. No DB migration. Rollback = revert the commits; nothing to undo in the database.

## Open Questions

1. Should `GET /tasks` gain `task_type` and date-range filters for MVP? **Tentative:** no — ship only the existing `status` filter; add when the frontend needs it.
2. Should the version-tree node embed the latest run status, or only `task_versions.status`? **Tentative:** only the version status; run-level detail lives in the version-detail endpoint (D8).
3. **Realtime cursor reconciliation (deferred to the realtime-gateway proposal):** §5.2 describes the WS client tracking max `seq` and backfilling via `/events?after_id=`, but this endpoint's cursor is the global `task_events.id` (D7). The realtime proposal must decide whether the WS push frame surfaces `id` alongside `seq` (recommended), or whether §5.2 is amended. This change exposes both `id` and `seq` on every `EventItem` so either resolution is supported; no further decision is needed here.
