## Context

`TaskDetail` (`web/src/routes/TaskDetail.tsx`) already renders the task header (title / `StatusBadge` / `CostBadge`), the version tree with an `Iterate` button gated by `isActiveStatus`, and a live event log. Live updates flow through `useTaskLive` (WS subscribe + React Query invalidation, polling fallback) — and with `realtime-gateway` now shipped, `status:*` events stream in real time. The write path is the established mutation idiom: a thin `apiFetch` wrapper in `features/tasks/api.ts`, a `useMutation` in `mutations.ts` with `meta:{silent:true}` and cache invalidation on settle, errors surfaced inline via `useUiStore` toasts (see `useIterateTaskMutation` + the 409 handling in `TaskDetail`).

The `task-control-api` contract is fixed: `POST /api/v1/tasks/{id}/control` with `{action, reason?}` → `202 {accepted, action, task_id, effective}` (`effective ∈ {queued, best_effort}`); `409 invalid_state` whose `message` names the current status; `404 task_not_found`; `400 invalid_input`. The API state guards are advisory — the worker is the authority — and the API does NOT dedupe duplicate controls.

This change is web-only and additive: no API/worker/schema/MQ change.

## Goals / Non-Goals

**Goals:**
- A pause/resume/cancel control bar on `TaskDetail`, wired to the existing control endpoint, that a user can drive without leaving the page.
- Button enablement that matches the API preconditions so the common case never round-trips into a 409, while still handling a 409 gracefully when the client view is stale.
- Reflect the resulting status through the existing live pipeline, with no optimistic local lie about state the worker may reject.

**Non-Goals:**
- A `reason` input (the API accepts one; deferred — the bar sends `{action}` only).
- A confirm dialog / two-step guard for Cancel (Post-MVP; see D5).
- Rollback / branch controls (deferred per the rollback-postponed direction).
- Any change to the control endpoint, the worker, or the realtime contract.
- Surfacing the control bar anywhere other than `TaskDetail` (TaskList row actions are out of scope).

## Decisions

### D1 — Extend `web-tasks-pages`, don't mint a new capability
The control bar is part of the Task Detail page surface, alongside the already-specced Iterate Action and Live Observation. So the delta is a single `## ADDED Requirements` entry against `web-tasks-pages` (no existing requirement's behavior changes). **Alternative:** a standalone `web-control-bar` capability — rejected: it would fragment the Task Detail page across two specs for one button group, and the data-layer mutation has no separate spec home (the iterate/create mutations live under web-tasks-pages too).

### D2 — Client-side enablement mirrors the API state machine; the API stays authoritative
A small pure helper maps `task.status → {canPause, canResume, canCancel}`. The API preconditions read `pause ∈ {pending, running}`; `resume = paused`; `cancel ∉ {cancelled, succeeded, failed}`. For the *reachable* `task.status` set this is encoded **positively** over the six `TASK_STATUSES`: `pause = {pending, running}`, `resume = {paused}`, `cancel = {pending, running, paused}`. Encoding `cancel` positively (rather than the literal `∉ terminal`) avoids offering Cancel for the version-only `cancelling` value — `types.ts` documents that `cancelling`/`queued` never appear on `task.status` (event-ingest collapses them), so they are deliberately excluded from the helper. The helper signature takes `status: string` (matching `TaskInfo.status`) and returns all-false for any unknown status, so a typo can't silently enable an action. Disabled buttons carry a `title` reason. **Why client-side gating at all:** it makes the common path a single accepted request and avoids inviting obvious-mistake 409s (the same rationale the API gives for its advisory guards). **Why still handle 409:** the client view can be stale (status flips between render and click), so a 409 is a real, expected outcome — surfaced, not swallowed (D4). The helper is unit-tested across all six `TASK_STATUSES` plus an unknown-status all-false case.

### D3 — Status reflection is asynchronous via the live pipeline, never optimistic
`202` means "durably queued to outbox", not "state changed" — the worker flips status later and emits `status:*` events. So the bar does NOT locally mutate `task.status`. It relies on `useTaskLive` (already mounted on the page) to invalidate `taskKeys.detail` / `taskKeys.versions` when the `status` frame arrives (live via the gateway; ≤3s polling fallback otherwise). The mutation's `onSettled` ALSO invalidates those keys so the bar re-reads promptly even if the live frame is delayed/dropped. On success the bar shows a transient toast (e.g. "Pause requested") so the user gets immediate feedback for the gap between request and observed status change. **Alternative:** optimistic `status` update — rejected: it would show a state the worker hasn't (and may never) reach, and contradicts "event-ingest is the sole status writer".

### D4 — Errors handled inline, mirroring the iterate idiom
`controlTask` sets `toastOnError:false` and the mutation is `meta:{silent:true}`; `TaskDetail` handles outcomes in the mutation's `onError`/`onSuccess`:
- `409 invalid_state` → `warning` toast using `err.message` verbatim (the API guarantees it names the current status, e.g. `cannot pause task in status "paused"`), then the cache invalidation re-syncs the now-known status.
- `404 task_not_found` → note the not-found render (`TaskDetail.tsx`) is driven by `taskQuery.error`, NOT by the control mutation. A 404 from the control POST lands in the mutation's `onError`; it is otherwise handled like a generic error (toast), and the page's not-found view appears only because the `onSettled` task refetch *also* 404s. We do not special-case the mutation's 404 beyond that.
- `best_effort` cancel (`202` with `effective:"best_effort"`) → `info` toast noting it may not take effect until the task is claimed (pre-claim, the broker may drop the control message — the API documents this).
- `400 invalid_input` is treated as a programming error (we only ever send a valid `action`); it falls through to a generic error toast.

### D5 — Double-submit guard, but no Cancel confirm dialog (MVP)
While any control request is in flight, all three buttons are disabled (`mutation.isPending`). This is pure UX hygiene — the API tolerates duplicates (two outbox rows, worker de-dupes), so correctness does not depend on it. The guard is **scoped to the control group**: an in-flight control does NOT disable the Iterate button, and an in-flight iterate does NOT disable the control bar (the two are complementary by status — Iterate is gated by `isActiveStatus`, the control bar is enabled in exactly those active states). A later change must not couple the two mutexes. A confirm dialog for Cancel is deliberately omitted for MVP: cancel only *requests* cancellation (the worker decides and can still finish/clean up), and the codebase has no modal pattern yet. Cancel uses the `Button` `danger` variant for visual weight. A confirm step is noted Post-MVP.

### D6 — A presentational `ControlBar` component; `TaskDetail` owns orchestration
`components/tasks/ControlBar.tsx` is presentational: it takes `status`, a `pending` flag, and `onAction(action)`, and renders the three buttons with enablement + tooltips. `TaskDetail` owns the mutation, the toasts, and the disabled-while-pending wiring — same split as the existing Iterate block. This keeps the component trivially unit-testable (enablement/labels) and the data flow in one place.

## Risks / Trade-offs

- **[Stale client view → user clicks a now-invalid action]** → the API returns `409 invalid_state` with the current status in the message; we toast it and the invalidation re-syncs. The window is small (live status keeps the bar fresh) and the outcome is correct, just a wasted round-trip.
- **[202 but status visibly unchanged for a beat]** → inherent to the async control model; mitigated by the immediate confirmation toast + `onSettled` invalidation, and by the now-live status stream. Without the realtime gateway this would lag up to one poll interval (~3s); with it, near-instant.
- **[`best_effort` cancel silently no-ops]** → the info toast tells the user it may not take effect pre-claim; once the task is claimed and runs, a later cancel takes effect. Acceptable for MVP.
- **[Duplicate controls from rapid clicks]** → disabled-while-pending guard on the client; the API+worker already tolerate duplicates as a backstop.

## Migration Plan

- Additive web change; no flag, no migration. Ship behind nothing — the bar simply appears on `TaskDetail`.
- Rollback: revert the change; the page returns to view-only (iterate still works). No persistent state involved.

## Open Questions

- Toast copy ("Pause requested" / best-effort note) — finalize wording in apply; trivial and non-contractual.
