# Review Suggestions — add-task-read-api

Reviewer notes on `proposal.md`, `design.md`, `specs/task-read-api/spec.md`, `tasks.md`.
Each item is independent; severities: [blocking] must resolve before apply, [should] resolve or
consciously waive, [nice-to-have] optional polish.

---

## S1 — [blocking] `cost.amount_usd` wire representation is unspecified; `pgtype.Numeric` does not serialize to a plain JSON number

**Location:** design.md D4/D5; spec.md "Embedded Cost Summary Is Best-Effort" (scenario "Cost summary reflects populated rows", asserting `cost.amount_usd MUST equal 0.62`); tasks.md 2.2.

**Problem:** `task_costs.amount_usd` is `NUMERIC(18,8)`, generated as `pgtype.Numeric` (see `sqlc/models.go:94`, `sqlc/task_costs.sql.go:36`, and `GetTaskCost`'s `::numeric` cast also lands as `pgtype.Numeric`). `pgtype.Numeric`'s `MarshalJSON` does **not** emit a bare JSON number like `0.62`; if the field is embedded directly in the response DTO the client gets a numeric *string* at best, or an unexpected shape, and the spec scenario (`cost.amount_usd MUST equal 0.62`) is not satisfiable as written. The design never states the JSON type of `amount_usd`, where the `pgtype.Numeric → JSON` conversion happens, or how precision is preserved. This is the single biggest correctness gap.

**Fix:** Decide and pin the wire contract in D4 and the spec. Recommended: represent `amount_usd` as a **decimal string** (e.g. `"0.62000000"` or trimmed `"0.62"`) to avoid float64 rounding of an 8-dp money value, and have the `CostSummary` DTO assembly (task 2.2) explicitly convert `pgtype.Numeric` via a helper (e.g. `num.Value()` / `Float64Value()` / format the `big.Int`+exponent) rather than embedding the pgtype. If a JSON number is preferred instead, state that and accept float64 precision limits explicitly. Update the spec scenario to assert the chosen representation (`"0.62"` vs `0.62`). Add a unit test in task 6.1 for the numeric→DTO conversion (zero, fractional, large).

---

## S2 — [blocking] Version-tree cost lookup is N+1, contradicting the "no N+1" design claim

**Location:** design.md D4 ("Cost summary embedded via a batched query (no N+1)") and D6; tasks.md 2.5.

**Problem:** D4 only batches the **task list** via `ListTaskCostsByTasks`. For the version tree (`GET /tasks/{id}/versions`), D6 and task 2.5 attach cost "via `GetVersionCost`" — i.e. one query per version in a loop. A task with N versions issues N+1 queries, which is exactly the pattern D4 claims to avoid, and the version tree is the most version-dense endpoint. The Impact/Risks sections advertise no N+1 but the tree path reintroduces it.

**Fix:** Add a batched `ListVersionCostsByVersions` (`WHERE version_id = ANY($1::uuid[]) ... ` or `WHERE task_id = $1`) and use it for the tree, mapping zero-fill in Go just like `ListTaskCostsByTasks`. Since `task_costs` has `version_id` PRIMARY KEY and a `task_costs (task_id)` index, a single `WHERE task_id = $1` fetch covering the whole tree is cleanest and needs no array param. Add it to tasks.md §1 and update D4/D6 to describe the batched version-cost path.

---

## S3 — [should] `next_after_id` cursor is keyed on `id`, but the architecture's WS reconnection contract tracks `seq`

**Location:** design.md D7; spec.md "Version Event Backfill Endpoint"; docs/ARCHITECTURE.md §5.2 ("客户端记录每个 topic 已收到的最大 `seq`，重连后通过 REST `/events?after_id=` 补齐缺口").

**Problem:** The WS push frame (§5.2) carries `seq` (per-run monotonic), and §5.2 says the client records the max `seq` it has seen, then backfills via `/events?after_id=`. This endpoint's `after_id`/`next_after_id` are the `task_events.id` BIGSERIAL PK, **not** `seq`. `seq` is only unique per `run_id` (`UNIQUE (run_id, seq)`), so a client holding a max-`seq` cannot pass it as `after_id` and get correct results — the two are different number spaces. The architecture's stated reconnection flow does not line up with the cursor this endpoint exposes. The design rides the existing `ListEventsAfter`/`(task_id, id)` index convention (which is internally consistent), but never reconciles that `id`-cursor with §5.2's `seq`-cursor.

**Fix:** Add a short note in D7 (and ideally the spec's endpoint description) clarifying that the backfill cursor is the global `task_events.id`, not the per-frame `seq`, and that the WS client must therefore track the `id` (or the gateway must surface `id` alongside `seq` in the WS frame). If the intended client contract is genuinely `seq`-based, that is a deeper mismatch that needs an explicit decision here rather than silent divergence. Either way, the `EventItem` DTO should expose both `id` and `seq` so the client can reconcile. Confirm `EventItem` includes `id`, `seq`, `kind`, `payload`, `created_at`, `run_id` (and note `run_id` may be `null`).

---

## S4 — [should] `EventItem` shape and nullable `run_id` are unspecified

**Location:** spec.md "Version Event Backfill Endpoint"; tasks.md 2.2.

**Problem:** The spec never states the per-event item shape, only "the version's `task_events`". `task_events.run_id` is **nullable** (`migrations/0002 ... run_id UUID` with no NOT NULL; `sqlc.TaskEvent.RunID` is `pgtype.UUID`). A consumer needs to know whether `run_id` can be `null` and what fields appear. `payload` is JSONB (`[]byte` in Go) and must be passed through as raw JSON, not a base64 string.

**Fix:** Define the `EventItem` DTO in the spec/D7: `{id, version_id, run_id (nullable), seq, kind, payload, created_at}`, with `payload` rendered as raw JSON (`json.RawMessage`) and `run_id` as JSON `null` when absent. Add a scenario or note covering an event with `run_id = NULL`.

---

## S5 — [should] `params` / `error` JSONB pass-through not specified for version detail and runs

**Location:** spec.md "Version Detail Endpoint" and "Version List (Tree) Endpoint"; design.md D8.

**Problem:** D8 says the run `error` JSONB is "passed through as raw JSON" — good — but the spec text for the run item only lists `{id, attempt_no, status, started_at, ended_at, last_heartbeat, error}` without stating `error` is raw JSON (and nullable: `task_runs.error` is nullable JSONB → `[]byte`). Separately, the version node (`VersionNode`) and version detail (`version`) omit any mention of `params` (JSONB on `task_versions`) and `prompt`: D6's node list is `{id, parent_id, version_no, status, is_active, artifact_root, created_at, cost}` (no `prompt`/`params`), while D8's "full version row" would include `prompt`/`params`. Whether the tree node deliberately drops `prompt`/`params` (likely yes, for payload size) vs. the detail including them is not made explicit, so an implementer may guess wrong.

**Fix:** In the spec, state that the tree node intentionally omits `prompt`/`params` (lightweight node) while version detail's `version` includes them; render `params` and run `error` as raw JSON, with `error` as JSON `null` when the column is NULL. Note nullable raw-JSON fields explicitly so the DTO uses `json.RawMessage` / `*json.RawMessage`.

---

## S6 — [should] `page < 1` / `page = 0` / negative and non-numeric pagination inputs have no defined behavior

**Location:** design.md D3; spec.md "List Tasks Endpoint" (only `page_size` clamping has a scenario).

**Problem:** D3 says `page` defaults to 1 and `page_size` is clamped to `[1,100]`, but never says what happens for `page=0`, `page=-3`, or a non-integer `page`/`page_size` (e.g. `page=abc`). The spec has a `page_size=9999` clamp scenario but no `page` lower-bound or parse-failure scenario. Offset is computed as `(page-1)*page_size`; `page=0` would yield a negative offset and a SQL error, and `page=abc` needs a defined outcome (400 vs. default).

**Fix:** Specify in D3: `page` clamped to a minimum of 1 (values `< 1` treated as 1); non-integer `page`/`page_size`/`after_id`/`limit` either rejected with `400 invalid_input` (naming the field) or coerced to default — pick one and state it. Add spec scenarios: "page below 1 is clamped to 1" and "non-numeric page_size" (matching whatever rule you choose). Make task 4.2 / 6.1 cover these explicitly.

---

## S7 — [should] `status` filter accepts arbitrary values with no validation; behavior on invalid status undefined

**Location:** spec.md "List Tasks Endpoint" (status filter); design.md D3.

**Problem:** `ListTasks` passes `status` straight into the SQL predicate. The `tasks_status_check` constraint only permits `pending/running/paused/cancelled/succeeded/failed` (note: NOT `queued`/`cancelling`, which are version-only statuses). A caller passing `status=queued` or `status=bogus` gets an empty list with `total=0` and HTTP 200 — silently, not a 400. The spec does not define whether an unknown/invalid `status` is a 400 or an empty 200.

**Fix:** Decide and state: either validate `status` against the six allowed task statuses (`task.Status` set, minus the version-only ones) and return `400 invalid_input` for anything else, or explicitly document "unknown status yields an empty result set, HTTP 200". Add a scenario. Reusing the existing `task.Status` constants keeps this DRY, but note `IsActive`/`activeStatuses` is the wrong set here (those are version-active states); a separate task-status validity set is needed.

---

## S8 — [should] Application-layer `Owner` identity type proliferation; reuse the existing command identity convention

**Location:** tasks.md 3.1 ("an `Owner{TenantID, UserID}` (or reuse existing identity type)"); design.md D1.

**Problem:** The write path threads identity as bare `TenantID`/`UserID` fields on each command (`CreateTaskCommand`, `IterateTaskCommand` in `application/task/commands.go`) and on the handler struct (`TaskHandlers.DevTenantID/DevUserID`). Introducing a new `Owner` struct only in the read path creates two conventions for the same concept and risks an ownership check that compares the wrong field. The task leaves this as an open choice, which invites inconsistency.

**Fix:** Pick one and commit to it in D1. Recommended: define a single `Owner{TenantID, UserID uuid.UUID}` value type in `domain/task` and pass it to every `ReadService` method; do not also add loose `tenant_id`/`user_id` params. Keep the handler reading `DevTenantID`/`DevUserID` (S-consistent with `TaskHandlers`) and constructing the `Owner` at the interface boundary. State that the ownership comparison is `row.TenantID == owner.TenantID && row.UserID == owner.UserID` (both must match), so a same-tenant/different-user row is still 404.

---

## S9 — [should] Ownership comparison must handle `pgtype.UUID` vs `uuid.UUID` and the `Valid` flag explicitly

**Location:** design.md D2; tasks.md 2.4/2.6.

**Problem:** `GetTaskByID` returns `Task` with `TenantID`/`UserID` as `pgtype.UUID` (`sqlc/models.go:65-66`), while the caller identity is `uuid.UUID`. The ownership check must convert and also confirm `pgtype.UUID.Valid` is true. `tenant_id`/`user_id` are NOT NULL so `Valid` should always hold, but a silent zero-UUID comparison (e.g. an all-zero dev identity matching an all-zero column) is a real foot-gun. D2 describes the comparison conceptually but not the type bridge.

**Fix:** In D2 (or task 2.4), state the comparison converts `pgtype.UUID` → `uuid.UUID` (via `.Bytes`/`uuid.UUID(row.TenantID.Bytes)`), requires `.Valid`, and treats any mismatch as the not-found error. Add an integration assertion that a same-tenant **different-user** task returns 404 (the "Other owners' tasks are invisible" scenario currently only covers another *tenant*; cover the user dimension too).

---

## S10 — [nice-to-have] `version_id` path that resolves to a task the caller owns but whose `version.task_id` mismatch is not covered

**Location:** spec.md "Owner-Scoped Reads Hide Unowned Resources"; design.md D2.

**Problem:** D2 resolves `version → task_id → GetTaskByID → ownership`. The spec covers (a) version doesn't exist and (b) version's owning task belongs to another user. It does not explicitly cover the case where `GetTaskVersionByID` returns a row but `GetTaskByID(version.TaskID)` returns no row (orphaned/dangling version — shouldn't happen given the FK, but the read path should fail closed to `version_not_found`, not 500).

**Fix:** Add a one-line note in D2 that a version whose owning task cannot be loaded (any reason) maps to `version_not_found`, never 500 or 403. Optionally a defensive scenario. Low priority because the `task_versions.task_id → tasks.id` FK makes orphans impossible under normal operation.

---

## S11 — [nice-to-have] Offset/total race on the list is unaddressed

**Location:** design.md D3 / Risks.

**Problem:** `ListTasks` (LIMIT/OFFSET) and `CountTasks` run as two separate statements (no snapshot/transaction stated). Under concurrent inserts, `total` and `items` can be momentarily inconsistent (e.g. `total=21` but page 1 of 20 returns a row that also appears as the count shifts). At MVP scale this is benign, but it is undocumented.

**Fix:** Add a Risks/Trade-offs bullet acknowledging the list is not a consistent snapshot (count and page may race) and that it is acceptable for MVP; optionally note that wrapping both queries in a single read-only transaction (repeatable read) would close it if needed later.

---

## S12 — [nice-to-have] `is_active` source field nullability for version nodes

**Location:** spec.md "Version List (Tree)" and "Version Detail"; design.md D6/D8.

**Problem:** `task_versions.is_active` is a generated column typed `*bool` in Go (`sqlc/models.go:130`, nullable pointer because sqlc cannot prove a GENERATED column is NOT NULL). The DTOs expose `is_active` as a plain bool; the mapping must deref the pointer (treat `nil` as `false`) to avoid a nil-panic or a JSON `null` leaking into a field the spec implies is always boolean.

**Fix:** Note in D6/D8 (or task 2.2) that `is_active` is dereferenced from `*bool` with `nil → false`, so the DTO field is always a concrete boolean.

---

## S13 — [nice-to-have] tasks.md 7.1 edits `api/README.md` — confirm it exists / is the right surface

**Location:** tasks.md 7.1.

**Problem:** Task 7.1 updates `api/README.md` with the five endpoints. Worth confirming that file exists and that endpoint docs belong there rather than in an OpenAPI/`docs/` surface; otherwise this task may create a stray doc. Not a correctness issue.

**Fix:** Verify `api/README.md` is the established place for endpoint docs (the write endpoints' docs location), or point the task at wherever the write endpoints were documented, to keep one source of truth.

---

## Items checked and found correct (no change needed)

- 404-not-403 rule (D2) is consistent with `MapError` mapping `ErrTaskNotFound`/`ErrVersionNotFound` to 404 and reusing the existing domain errors — no new error codes invented. Good.
- Reusing the unified `Envelope` + `OK` helper and `code:0` success convention matches `envelope.go` and the write handlers.
- No DB migration / no new index: D7's reliance on the existing `task_events (task_id, id)` index with `version_id` as a residual filter is sound for the `task_id = $1 AND version_id = $2 AND id > $3 ORDER BY id` predicate.
- Separate `ReadService` (D1) and separate `TaskReadHandlers` file (D9) respect DDD layering and the AGENTS.md sqlc-only / no-handler-SQL rule.
- D10 (no new Prometheus counters for reads) correctly applies the AGENTS.md observability rule (state transitions / external calls), and still requires `trace_id`/`task_id`/`version_id` on slog lines.
- Endpoint paths and the list "含累计成本" / detail "含...成本摘要" expectations match ARCHITECTURE.md §5.1.
- No worker writes to main tables, no Outbox in the read path — red lines respected.
