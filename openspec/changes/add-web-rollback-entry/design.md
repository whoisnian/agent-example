## Context

The rollback backend (`task-rollback-api`) is live but has no web client. The web tasks slice already has a settled pattern for write actions — iterate and control both use a `toastOnError:false` api wrapper + a `meta:{silent:true}` mutation, with the page owning all 409 / invalid_input UX inline and never optimistically mutating `task.status` (status flows back through the live/polling pipeline). This change adds rollback as a third such action.

Two backend facts shape the UI and were verified against source, not docs:

1. **Both modes require a non-active task.** `domain/task/rollback_service.go:93` returns `409 active_version_exists` for branch *and* switch when `IsActive(task.status)`. ARCHITECTURE §6.5 documents this correctly, but the API table (line 522, "switch 仅切指针不受约束") and the §6.4 UI note (line 788, "禁用迭代/回滚-branch 按钮") are stale.
2. **Switch additionally requires a terminal target** (`409 invalid_state`), asserted via the target version row's generated `is_active` column (`rollback_service.go:136`).

Response shapes differ by mode: branch → `201 {version_id, version_no, status}`; switch → `200 {current_version_id, version_no, status}`.

## Goals / Non-Goals

**Goals:**
- A reachable, per-version rollback entry on the Task Detail page for both modes.
- UI mutex + advisory guards that mirror the backend preconditions, with the backend staying authoritative on races.
- Reuse the existing iterate/control client + mutation conventions verbatim (silent mutation, invalidate on settle, inline error handling).
- Reconcile the two stale ARCHITECTURE lines.

**Non-Goals:**
- No backend / API / DB / spec change to `task-rollback-api` itself.
- No new global (Zustand) state — picker state is local, like the iterate prompt box.
- No version-tree visual redesign (DAG/branch graphics) — rows stay as today, gaining an action.
- `params` / `lane` rollback fields are not collected by the UI this round (the wrapper accepts them for parity, mirroring how iterate omits them).

## Decisions

### D1 — Entry lives on the VersionTree row, not the existing ControlBar
Rollback targets a *specific historical version*, so the action belongs on each non-current version row (you roll back *to* that row), unlike pause/resume/cancel which act on the task as a whole. The ControlBar stays untouched. **Alternative considered:** a single "Rollback…" button + a version dropdown — rejected as redundant with the tree already on screen and worse for discoverability.

To keep `VersionTree` from growing stateful and hard to test, the per-row picker is a small dedicated presentational component (`RollbackControl`) receiving `disabled` flags + reasons + an `onRollback(mode, prompt?)` callback (the leaf control has no notion of the version id); TaskDetail owns the mutation and toasts (same split as ControlBar ↔ TaskDetail). The version id is supplied by `VersionTree`, which closes over `node.id` and exposes a `onRollback(versionId, mode, prompt?)` prop upward to TaskDetail.

### D2 — Disable both modes while the task is active
Driven by `isActiveStatus(task.status)` — the same predicate that gates Iterate. A disabled action carries a `title` reason ("Task is busy — wait for the active version to finish"). This is advisory; a racing `409 active_version_exists` is still handled inline (toast naming `data.active_version_id`/`active_version_status`, then refetch task+versions, no retry). **Alternative:** gate only branch (per the stale line 788) — rejected, the backend 409s switch too, so an enabled switch button would just produce confusing failures.

### D3 — Advisory-disable switch on non-terminal target rows
A version row is shown with `is_active` from the read DTO. When `version.is_active` is true, the row's **switch** option is disabled with a reason ("Can only switch to a finished version"); **branch** is governed only by the task-level mutex (D2). If a switch races and the target turns out non-terminal, the resulting `409 invalid_state` surfaces as a warning toast (reusing the control bar's invalid_state handling shape). The current version itself is never offered a rollback action (no-op).

### D4 — Mode picker UX: inline disclosure, branch gets an optional prompt
Clicking "Rollback" on a row reveals two choices: **Switch** (immediate, confirmable) and **Branch** (reveals an optional prompt textarea; empty prompt is valid — the backend auto-fills "rollback to version N"). This mirrors the iterate disclosure already on the page. Submitting issues one rollback request with the chosen `mode`.

### D5 — Success handling per mode
On settle (success or 409 race) invalidate `taskKeys.detail(id)` + `taskKeys.versions(id)` — identical to iterate/control. Branch seeds a new pending version (the tree + status update via the live pipeline); switch repoints `current_version` (the "current" marker moves on refetch). Success toast names the mode and resulting `version_no`. **The mode is read from the mutation variables, not the response** — the two response bodies (`RollbackBranchResponse` / `RollbackSwitchResponse`) carry no shared discriminator and use different field names (`version_id` vs `current_version_id`), so the mutation is keyed on `RollbackVars { taskId, body }` (like `IterateVars`/`ControlVars`) and `onSuccess` reads `body.mode`; only `version_no`, common to both bodies, is read off the response. No optimistic `task.status` write.

## Risks / Trade-offs

- **[tasks.status vs current_version divergence after switch]** → Already documented behavior (ARCHITECTURE §6.5): the list badge reflects last-execution outcome, not the working base. The UI does not try to hide this; the version tree's "current" marker is the source of truth for the working base. No new mitigation needed.
- **[stale `is_active` in a cached versions query lets a doomed switch through]** → Acceptable: the backend 409_invalid_state is the real guard; the UI guard is advisory and the on-settle refetch corrects the row.
- **[ARCHITECTURE line edits touch a doc owned tightly by §6.5]** → Limited to rewording two lines to match the already-correct §6.5; no semantic change. Per AGENTS §1 the deviation is declared here and the doc is updated in the same change.

## Open Questions

- None blocking. Whether to later collect `lane`/`params` on rollback-branch is deferred (Post-MVP, consistent with iterate).
