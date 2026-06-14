# Review: `add-task-deletion` — Improvement Suggestions

> Consolidated review against the existing backend/frontend code and AGENTS.md. **This file does not modify the proposal.** Findings are grounded in `file:line` evidence. **[BLOCKING]** = wrong / contradictory / will-break / violates a red-line; **[IMPROVEMENT]** = clarity / nice-to-have.

## Summary

| Area | #Blocking | #Improvements | Health |
|------|-----------|---------------|--------|
| proposal.md | 1 | 1 | Needs fix (error-code fact) |
| design.md | 1 | 2 | Mostly sound |
| spec: task-delete-api | 1 | 1 | Code-string fix |
| spec: task-data-model | 1 | 1 | Index req conflicts with archived spec |
| spec: task-read-api | 1 | 1 | ADDED should be MODIFIED |
| spec: web-tasks-pages | 0 | 2 | Sound |
| cross-cutting | 1 | 2 | PR-size / completeness |
| **Total** | **6** | **10** | |

The single most important issue is factual: the proposal asserts the not-found envelope code is `"not_found"`, but the entire task domain returns **`task_not_found`** (HTTP layer `errors.go:58`). This wrong string is repeated in five places and would produce specs/tests that contradict the real contract. Everything else is sound-to-good; the design's core decisions (soft-delete via `deleted_at`, reuse `active_version_exists`, no Outbox) hold up against the code.

---

## proposal.md

### P1 · [BLOCKING] Not-found code is `task_not_found`, not `not_found`
- **Evidence:** Proposal line 9 ("返回 `404 not_found`"), line 11, line 19. Actual code: `api/internal/interfaces/http/errors.go:57-58` maps `ErrTaskNotFound` → `http.StatusNotFound, "task_not_found", "task not found"`. The control handler hard-codes the same: `task_control.go:111` `Error(c, http.StatusNotFound, "task_not_found", ...)`. The generic `not_found` string only exists for `KindNotFound` DomainError (`errors.go:112`), which the task aggregate never uses.
- **Why it matters:** Specs and tests written to `code == "not_found"` will not match the real envelope; the frontend 404 handling keys on `err.status === 404` (queries.ts:49) so it survives, but contract tests asserting the `code` will fail and the spec misdescribes the contract.
- **Fix:** Replace every `not_found` with `task_not_found` in the proposal (lines 9, 11, 19), design D3, the task-delete-api spec, the task-read-api delta, and `tasks.md` 3.1. Keep HTTP 404.

### P2 · [IMPROVEMENT] "复用 active-version 检查" understates the actual mechanism
- **Evidence:** Proposal line 29 / design D2 say "复用现有 active-version 检查". The real pattern (rollback_service.go:98-107, service.go:250-257) is: `IsActive(locked.Status)` gate → `GetActiveVersionByTask(ctx, taskID)` to fetch `{id,status}` → construct `&ErrActiveVersionExists{...}`. It is reached after an owner-scoped `LockTaskForControl` (FOR UPDATE) load, not a standalone helper.
- **Why it matters:** An implementer reading "reuse the check" may look for a single shared function; there isn't one — it's an inline 3-step idiom. Naming the exact query (`GetActiveVersionByTask`, present in `querier.go:46`) and the lock query prevents a wrong abstraction.
- **Fix:** In Impact/tasks, reference `GetActiveVersionByTask` + the `IsActive(status)` gate explicitly as the reuse target.

---

## design.md

### D-1 · [BLOCKING] D3/D4 carry the same wrong `not_found` code
- **Evidence:** D3 line 32-33 ("一律 `404 not_found`"). Same root cause as P1.
- **Fix:** `task_not_found`.

### D-2 · [IMPROVEMENT] D4's soft-delete UPDATE diverges from the established owner-scoped lock idiom
- **Evidence:** D4 (line 36) proposes `UPDATE tasks SET deleted_at=now() ... WHERE id=$1 AND deleted_at IS NULL` "带 owner 约束", deriving not-found from `RowsAffected==0`. But the active-version check requires the task's `status`/active version *first*, and the codebase already has the exact primitive: `LockTaskForControl` (tasks.sql.go:163, FOR UPDATE + inline owner predicate, returns `status`+`current_version`) used by both control and rollback. The natural implementation is: `LockTaskForControl` (→ `ErrTaskNotFound` on no rows) → `IsActive(status)` check (→ `ErrActiveVersionExists`) → a new `SoftDeleteTask` exec, all in one tx.
- **Why it matters:** If `SoftDeleteTask` derives not-found purely from `RowsAffected==0`, it cannot distinguish "already-deleted" from "unowned/missing" cleanly *and* still run the active check (an already-deleted row would skip the active check but also needs the 404). Reusing `LockTaskForControl` gives a single owner-scoped load that serves both the 404 and the active check, matching the precedent and avoiding a second owner predicate.
- **Fix:** State that the domain method reuses `LockTaskForControl` for the owner-scoped + locked load, then guards active, then issues the soft-delete exec — rather than a bespoke owner-predicated UPDATE. (Note: `LockTaskForControl` does not yet filter `deleted_at IS NULL`; an already-deleted row would still lock and then the soft-delete exec must be a guarded `WHERE deleted_at IS NULL` to stay idempotent — make that explicit.)

### D-3 · [IMPROVEMENT] "前端已有 isConflictData" is true but it is not shared
- **Evidence:** D5 line 43 / D2 line 28 imply `isConflictData` is reusable. It exists only as a **local** helper inside `web/src/routes/TaskDetail.tsx:29` (not exported, not in `features/tasks/`). TaskList has no equivalent.
- **Why it matters:** The TaskList row delete-button path (D5) needs the same 409 narrowing but cannot import a route-local function; it would be duplicated or should be lifted to `features/tasks/types.ts` next to `ActiveVersionConflict` (types.ts:212).
- **Fix:** Add a task to promote `isConflictData` into `features/tasks/types.ts` so both TaskList and TaskDetail reuse it.

---

## spec: task-delete-api/spec.md

### S1 · [BLOCKING] Scenario 3 asserts `code = "not_found"`
- **Evidence:** spec line 20: `code = "not_found"`. Wrong per P1 — should be `task_not_found`.
- **Fix:** `code = "task_not_found"`.

### S2 · [IMPROVEMENT] Idempotency scenario's "no second `deleted_at` write" is untestable as worded
- **Evidence:** spec line 20 "no second `deleted_at` write MUST occur". The endpoint returns 404 for an already-deleted task because the row is invisible to the owner-scoped load (or guarded by `WHERE deleted_at IS NULL`); there is no observable "second write" signal in the response.
- **Why it matters:** The scenario reads like an assertion a test can make, but it can only be verified by inspecting that `deleted_at` is unchanged (timestamp preserved), not via the API.
- **Fix:** Reword to "the original `deleted_at` timestamp MUST be unchanged (the guarded update affects 0 rows)".

---

## spec: task-data-model/spec.md

### S3 · [BLOCKING] Partial-index requirement conflicts with the archived "Tasks Table" requirement (should be MODIFIED, not ADDED)
- **Evidence:** The archived `task-data-model` "Tasks Table" requirement (`openspec/specs/task-data-model/spec.md:8`) mandates: *"A composite index `(tenant_id, user_id, status)` MUST exist to support listing."* The delta ADDS a new requirement (delta line 5) mandating a **partial** index `(tenant_id, user_id, status) WHERE deleted_at IS NULL` and says the listing path "MUST remain index-backed". The real migration created the **non-partial** index (`migrations/0002_init_task_domain.up.sql:33` `tasks_tenant_user_status_idx`).
- **Why it matters:** After archiving, the spec would assert *two* indexes on the same `(tenant_id, user_id, status)` columns — one full (existing requirement, still MUST exist) and one partial (new requirement). That is contradictory/redundant: either the full index is replaced by the partial one (then the existing requirement's scenario/text is now false → MODIFIED) or both exist (wasteful, and the listing query would still be index-backed by the original full index, undermining the stated rationale). Also note the existing "Tasks Table" requirement enumerates the exact column list; adding `deleted_at` as a *separate* ADDED requirement leaves the column enumeration in the old requirement stale (it no longer lists every column).
- **Fix:** Convert to `## MODIFIED Requirements` for "Tasks Table": (a) add `deleted_at TIMESTAMPTZ` to the enumerated column list, and (b) decide the index story explicitly — either replace the composite index with the partial one (drop+create in the migration, update the requirement text) or keep the full index and drop the "MUST be served by the partial index" claim. Don't leave both as independent MUSTs.

### S4 · [IMPROVEMENT] "Soft delete sets the timestamp without touching status" enum list is correct — but double-check ordering
- **Evidence:** delta line 15 lists `{pending, running, paused, cancelled, succeeded, failed}`. Verified against `status.go:40-47` (taskStatuses) and `migrations/0002:30` — matches exactly. Good.
- **Why it matters:** No action needed; flagged only to confirm the enum claim is accurate (it is), unlike the not-found code claim.
- **Fix:** None.

---

## spec: task-read-api/spec.md

### S5 · [BLOCKING] "Reads Exclude Soft-Deleted Tasks" changes existing List/Detail behavior → MODIFIED, not ADDED
- **Evidence:** The delta is `## ADDED Requirements` (delta line 1). But the existing "List Tasks Endpoint" requirement (`openspec/specs/task-read-api/spec.md:8`) defines `total` as "the total count of matching tasks for the caller (ignoring pagination)" and its scenario "Status filter narrows results" (line 21-22) asserts `data.total` MUST equal the count of the caller's `succeeded` tasks. Once soft-deleted tasks are excluded, that scenario's truth condition changes (a soft-deleted `succeeded` task must now be excluded from `total`) — i.e. the existing requirement's behavior is altered. Likewise "Task Detail Endpoint" (line 49) returns 200 for "one task the caller owns"; a soft-deleted owned task must now 404, contradicting the unqualified existing text.
- **Why it matters:** OpenSpec ADDED vs MODIFIED matters for archive correctness: an ADDED requirement leaves the old requirements' text/scenarios stale and now-false. The precedent `add-artifact-deletion` correctly used `## MODIFIED Requirements` when it changed existing rendering behavior (`archive/2026-06-14-add-artifact-deletion/specs/web-tasks-pages/spec.md:1`).
- **Fix:** Restructure the read-api delta as `## MODIFIED Requirements` that re-state "List Tasks Endpoint" (total/items now exclude `deleted_at IS NOT NULL`) and "Task Detail Endpoint" / "Version List Endpoint" (soft-deleted → 404). Keep the new scenarios but anchor them under the modified requirements so the archived spec stays internally consistent.

### S6 · [IMPROVEMENT] Owner-scoped 404 today is enforced in Go, not SQL — name where the filter goes
- **Evidence:** `GetTaskByID` is **unscoped** SQL (`tasks.sql.go:84-88`, `WHERE id = $1` only); ownership is enforced in the read service in Go (`read_service.go:140-142`, `owns()` check → `ErrTaskNotFound`). `ListTasks`/`CountTasks` filter owner *in SQL* (`tasks.sql.go:110-112`, `:17-19`). So the `deleted_at IS NULL` filter for list/count goes in those two SQL queries, but for `GetTaskByID`-backed reads (detail, versions via `ownedTask`, version-by-id via `ownedVersion` → `GetTaskByID` at read_service.go:342) it must be added either to the SQL or as a Go guard after the owner check.
- **Why it matters:** The delta says "all owner-scoped reads MUST filter `deleted_at IS NULL`" but the implementation surface is split (SQL for list, Go-or-SQL for detail). `tasks.md` 1.3 only mentions list/get-task/list-versions; it omits the **version-by-id** path (`GET /versions/{id}`), which is reachable through a soft-deleted task via `ownedVersion` → `GetTaskByID` (read_service.go:342). That path is in the delta's prose ("version reads reachable through a soft-deleted task") but missing from `tasks.md`.
- **Fix:** Add the `/versions/{id}` (GetVersion) path to `tasks.md` 1.3, and specify whether `deleted_at` filtering lives in `GetTaskByID` SQL (cleanest — covers detail, versions, version-by-id, and the realtime `OwnsTask`/`OwnsVersion` ports at read_service.go:308-318 in one shot) or in the Go guards.

---

## spec: web-tasks-pages/spec.md

### W1 · [IMPROVEMENT] AlertDialog must be vendored — `components/ui/` has no confirm primitive
- **Evidence:** `ls web/src/components/ui/` shows badge, button, card, dropdown-menu, input, label, scroll-area, select, separator, skeleton, tabs, tooltip — **no `alert-dialog`** and no confirm dialog. The design (D5, Risks line 50) and `tasks.md` 6.1 correctly flag this as conditional ("若无则 vendoring"); the condition is in fact true.
- **Why it matters:** The "MUST require an explicit confirmation step" requirement (spec line 5) cannot be met with an existing primitive; vendoring `alert-dialog` (Radix + Tailwind-4 form) is mandatory, not optional. This adds non-trivial LOC and a new Radix dependency.
- **Fix:** Make the AlertDialog vendoring an unconditional task in `tasks.md`/Impact, and record the new `@radix-ui/react-alert-dialog` dependency.

### W2 · [IMPROVEMENT] 404-as-no-op is safe; no refetch-loop risk — worth stating why
- **Evidence:** Deleting the open task then `navigate('/tasks')` unmounts TaskDetail, stopping `useTaskQuery`. Even without navigation, `liveRefetchInterval` polls only while `isActive && WS not open` (use-task-live.ts:75-77); a deletable task is non-active, so no poll. `useTaskQuery` already disables retry on 404 (queries.ts:49). So there is no 404 refetch loop.
- **Why it matters:** Confirms the frontend section is safe; no change needed beyond noting it so a reviewer doesn't re-litigate.
- **Fix:** None (optionally note the reasoning in design D5).

---

## Cross-cutting

### X1 · [BLOCKING] Cost aggregation still counts soft-deleted tasks — confirm this is acceptable, and verify the list-cost batch
- **Evidence:** Design Risks line 47 marks this **intentional** (audit/§6). The list path batches per-task cost via `ListTaskCostsByTasks` keyed by the *already-filtered* task rows (read_service.go:118), so the **list** won't show deleted-task costs once `ListTasks` filters them — good. But the **CostDashboard** and any task-cost aggregate query are unchanged, so org/tenant-level cost still includes deleted tasks.
- **Why it matters:** This is a defensible product call (settlement integrity, §6 red-line forbids dropping cost rows), but it means "deleted" tasks silently keep contributing to dashboard totals with no UI trace — a surprise for users. The design names it but doesn't list the affected read (`task-cost-api` / CostDashboard) as explicitly out-of-scope-by-decision.
- **Fix:** Add one line to Non-Goals stating CostDashboard/cost aggregation deliberately still counts soft-deleted tasks (and that excluding them, if ever wanted, is a separate change). Not a code change — a scope-clarity fix so it isn't later treated as a bug.

### X2 · [IMPROVEMENT] PR is realistically splittable backend/frontend (AGENTS.md §7 <500 LOC)
- **Evidence:** `tasks.md` spans migration + sqlc + domain + handler + 2 test suites + api/mutations + 2 routes + AlertDialog vendoring + MSW tests. AlertDialog vendoring alone is sizable; backend domain+handler+tests is its own coherent unit.
- **Why it matters:** AGENTS.md §7 caps PRs at ~500 LOC (gen/tests excluded). This is borderline; the AlertDialog vendor + frontend wiring is independently shippable behind the already-existing API contract.
- **Fix:** Consider splitting into `add-task-deletion-api` (backend + specs) and `add-task-deletion-web` (frontend), or at minimum sequence the commits so backend lands first. Not blocking, but note it in the proposal.

### X3 · [IMPROVEMENT] Missing: realtime / WS ownership ports and the `total` semantics for an existing-page-out-of-range case
- **Evidence:** `OwnsTask`/`OwnsVersion` (read_service.go:308-318) authorize WS subscriptions via `ownedTask`/`ownedVersion` → `GetTaskByID`. If `deleted_at` filtering is added to `GetTaskByID` SQL (per S6 fix), a soft-deleted task's WS subscription correctly stops authorizing — desirable. If filtering is added only in the read DTO methods and not in `ownedTask`, a client could still subscribe to a deleted task's topic.
- **Why it matters:** Completeness — the realtime path is a reachable read of task ownership not enumerated in `tasks.md`.
- **Fix:** Ensure the `deleted_at IS NULL` guard lives where `ownedTask`/`ownedVersion` resolve (i.e. `GetTaskByID`), and add a test that a soft-deleted task's `OwnsTask` returns `ErrTaskNotFound`.

---

## What checks out (no action)

- `active_version_exists` contract: **accurate** — HTTP 409, `code = "active_version_exists"`, `data.{active_version_id, active_version_status}` (errors.go:76-80, tasks.go:317-327, ErrActiveVersionExists at errors.go:50-58). Reuse claim is correct.
- `taskKeys.lists` prefix-invalidation: **real and idiomatic** (queries.ts:19, mutations.ts:53/80/107).
- `isActiveStatus`: **real** (types.ts:42), correctly mirrors domain `IsActive` (status.go:31).
- `task-not-found` render state for dead direct links: **real** (TaskDetail.tsx:149), supporting design's direct-link claim.
- No-Outbox / local-only delete: **correct** — soft delete doesn't cross a service boundary, doesn't touch `task_versions` (so the `one_active_version_per_task` mutex is untouched), and the worker never writes `tasks` (AGENTS.md §4.2). The race analysis (Risks line 49) is sound.
- `status` CHECK enum claim `{pending, running, paused, cancelled, succeeded, failed}`: **accurate** (status.go:40-47, migration 0002:30).

---

## Resolution log (2026-06-14)

All 6 blocking + 10 improvements vetted against live code and **applied**; change re-validates clean.

| ID | Sev | Disposition |
|----|-----|-------------|
| P1 / D-1 / S1 | BLOCKING | Applied — `not_found` → `task_not_found` everywhere (verified errors.go:58 / task_control.go:111) |
| S3 | BLOCKING | Applied — task-data-model delta → **MODIFIED** Tasks Table (adds `deleted_at`, replaces full index with partial `WHERE deleted_at IS NULL`) |
| S5 | BLOCKING | Applied — task-read-api delta → **MODIFIED** List/Detail/Version-List (items+total exclude soft-deleted; deleted → 404) |
| X1 | BLOCKING | Applied — CostDashboard-still-counts-deleted made an explicit Non-Goal (intentional, §6) |
| P2 / D-2 | IMPROVEMENT | Applied — design names the real idiom: `LockTaskForControl` → `IsActive` → `GetActiveVersionByTask`; soft-delete reuses the locked load |
| D-3 | IMPROVEMENT | Applied — task to promote `isConflictData` from TaskDetail-local into `features/tasks/types.ts` |
| S2 | IMPROVEMENT | Applied — idempotency scenario reworded to "original `deleted_at` unchanged / 0 rows" |
| S6 / X3 | IMPROVEMENT | Applied — put the `deleted_at IS NULL` guard in `GetTaskByID` SQL (covers detail/versions/version-by-id/OwnsTask/OwnsVersion); added `/versions/{id}` + realtime-auth tasks & tests |
| W1 | IMPROVEMENT | Applied — AlertDialog vendoring made unconditional + `@radix-ui/react-alert-dialog` dep recorded |
| W2 | IMPROVEMENT | Applied — design notes why there's no 404 refetch loop |
| X2 | IMPROVEMENT | Applied — proposal notes PR is splittable (backend-first; `…-api` / `…-web` if over §7 limit) |
| S4 | — | No action (enum claim confirmed accurate) |

**Accurate as-proposed (no change):** `active_version_exists` contract (409 + data shape), `taskKeys.lists` invalidation, `isActiveStatus`, `task-not-found` render state, no-Outbox/no-mutex-touch race analysis, `status` enum.
