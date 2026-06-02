## Why

The cost backend (`task-cost-api`) is complete — `GET /me/cost`, `GET /tasks/{id}/cost`, `GET /versions/{id}/cost`, `GET /pricing` all ship — but the web client uses **none** of it. The only cost surface today is the inline `CostBadge` (a single `amount_usd` pulled from the read DTOs), the `/cost` route is still `CostDashboardPlaceholder`, and `TaskDetail` shows no token breakdown. The MVP requires the user-facing cost story (ARCHITECTURE §5.1, §A.1): a cost dashboard and a per-task cost panel. This is the next web slice after `add-web-control-bar`, with its whole backend already green.

## What Changes

- **New `features/costs/` data slice** — typed access (types + `apiFetch` wrappers + React Query hooks) for the caller-scoped rollup (`GET /me/cost`) and the per-task breakdown (`GET /tasks/{id}/cost`). The discriminated rollup response (`{total}` vs `{group_by, items}`) is modeled faithfully; `amount_usd` stays a **decimal string**, never parsed to `number`.
- **Real `CostDashboard` page** — replaces `CostDashboardPlaceholder` on the `/cost` route. A `group_by` selector (Total / day / task_type / model). **Total is the lifetime cumulative spend** (sends no `from`/`to`, matching the backend's open-window ungrouped default and ARCHITECTURE §3.1 "累计成本"); the time-window preset (7d / 30d / 90d, default 30d) appears and applies **only** for the grouped views, where the backend caps the window at 366d. A totals header plus a lightweight CSS bar list over `items` (server order preserved; the `model` grouping's synthetic `other` bucket renders as a normal row). Owner-scoped via `/me/cost`. Loading / empty (zero-fill, never 404) / error states.
- **`TaskDetail` cost panel** — a reusable `TokenBar` showing the task's `input / output / cached` token breakdown + tool calls + wall time alongside the existing amount, sourced from `GET /tasks/{id}/cost` (`total`). The inline `CostBadge` stays. The per-version breakdown already renders via `VersionTree`'s per-node badges, so it is not duplicated.
- **No new runtime dependency** — bars are CSS/flex, no charting library (charts are Post-MVP per ARCHITECTURE §A.3).
- **Web-only** — no API / worker / schema / MQ change.

## Capabilities

### New Capabilities
- `web-cost-views`: The `/cost` CostDashboard page and the `features/costs/` data-access slice — caller-scoped rollup with grouping + window, decimal-string-safe rendering, owner-scoped reads.

### Modified Capabilities
- `web-tasks-pages`: adds a Task Detail cost-panel requirement (token breakdown via `/tasks/{id}/cost`) — a new surface on an existing page, not a change to existing TaskDetail requirements.

## Impact

- **New files**: `web/src/features/costs/{types,api,queries}.ts`, `web/src/components/costs/TokenBar.tsx`, `web/src/routes/CostDashboard.tsx`, plus co-located tests and an MSW handler for `/me/cost` + `/tasks/{id}/cost`.
- **Modified**: `web/src/router.tsx` (swap placeholder → real page), `web/src/routes/TaskDetail.tsx` (mount the cost panel). `routes/placeholders/CostDashboardPlaceholder.tsx` is removed.
- **Consumes** (no change): `task-cost-api` endpoints `/me/cost`, `/tasks/{id}/cost`.
- **Already ships, unchanged**: TaskList's cumulative-cost column (`CostBadge` per row, `TaskList.tsx`) and VersionTree's per-version cost badge — neither is touched by this slice.
- **Out of scope (deferred)**: `GET /pricing` reference view + TaskCreate cost estimate (a later `add-web-cost-estimate`); a live per-token meter for the running version. The latter is a **deliberate, temporary divergence from ARCHITECTURE §3.1** ("当前 running 版本的实时累计"): the realtime gateway streams *status* only this round, so the cost panel reflects updates on the existing poll/refetch cadence; the divergence is recorded here per AGENTS.md §1. Real charting (SVG/canvas/lib) is also out of scope (Post-MVP, ARCHITECTURE §A.3).
