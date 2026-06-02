## Context

`task-cost-api` is archived and green; its DTOs and edge cases are fully specified (decimal-string `amount_usd` at scale 8, discriminated `/me/cost` response, zero-fill never-404, owner-scoped 404, day/task_type/model grouping with a 366-day cap and a 30-day default window). The web client mirrors the read/write/control DTOs in `features/tasks/types.ts` and accesses them through the thin-`apiFetch` + React Query pattern (`features/tasks/{api,queries}.ts`). State layering is fixed: server state in React Query, local UI state in Zustand (AGENTS.md §4.3). `CostBadge` already encodes the "decimal string, truncate-don't-round, full value in `title`" display rule. No charting library is installed and the MVP does not want one (ARCHITECTURE §A.3).

## Goals / Non-Goals

**Goals:**
- A working `/cost` CostDashboard backed by `GET /me/cost`, with a grouping selector and a window preset, faithfully discriminating the `{total}` vs `{group_by, items}` response shapes.
- A `features/costs/` slice that is the single typed entry point for cost reads, following the existing `features/tasks` shape exactly (types → api → queries).
- A reusable `TokenBar` that renders the full `CostSummary` (amount + token/tool/wall breakdown) and is mounted on both the dashboard and `TaskDetail`.
- Zero new runtime dependencies; decimal-string discipline preserved end-to-end.

**Non-Goals:**
- No `GET /pricing` view and no TaskCreate cost estimate (deferred to `add-web-cost-estimate`).
- No live per-token meter for the running version. This is a **deliberate, temporary divergence from ARCHITECTURE §3.1** ("当前 running 版本的实时累计"): the realtime gateway streams *status* only this round, so the cost panel reflects updates on the existing poll/refetch cadence rather than a per-token live stream. Recorded as a deviation per AGENTS.md §1 (no doc rewrite needed — the architecture target stands, this round just doesn't reach it yet).
- No real charts (SVG/canvas/lib); a CSS flex bar list is sufficient for the MVP.
- No `GET /versions/{id}/cost` consumption — per-version cost already renders in `VersionTree` from the read DTO; the dedicated version endpoint is not needed by any new surface.

## Decisions

### D1. New capability `web-cost-views` for the dashboard; delta to `web-tasks-pages` for the panel
The CostDashboard is a genuinely new page/surface (the `/cost` route), so it earns its own capability rather than being bolted onto `web-tasks-pages`. The TaskDetail cost panel is an addition to an existing page, so it is an ADDED requirement on `web-tasks-pages` (mirroring how `add-web-control-bar` appended the control-bar requirement there). *Alternative considered:* fold everything into `web-tasks-pages` — rejected because the dashboard is not a task page and the spec would lose cohesion.

### D2. Model the `/me/cost` response as a discriminated union on the `group_by` key
Per the spec, the payload contains **only** `total` (ungrouped) **or only** `group_by`+`items` (grouped). The TS type is `type CostRollup = { total: CostSummary } | { group_by: CostGroupBy; items: CostGroupItem[] }`, and the hook/page branches on `"group_by" in data`. This matches the server contract exactly and avoids an "optional everything" type that would let an impossible state compile. *Alternative:* a flattened type with optional fields — rejected; it permits states the API can never emit.

### D3. Grouping + window are local UI state, encoded in the React Query key (not Zustand, not the URL)
The selector value (`none | day | task_type | model`) and the window preset (`7d | 30d | 90d`) are `useState` inside the page and folded into `costKeys.rollup({groupBy, from, to})`, so changing either is a normal cache-keyed refetch. This is server-state-derived UI, kept local to the page — consistent with how `TaskList` holds its `page`/`status` filter locally rather than in the global Zustand store. *Alternative:* persist in URL query params — nice-to-have, out of scope for the MVP slice and easily added later.

**Window semantics differ by grouping (deliberate, backend-faithful):**
- **Total (ungrouped)** sends **no** `from`/`to` — the lifetime cumulative spend. This matches the backend's ungrouped open-window default (`task-cost-api`: "When `group_by` is absent the window is open by default … 'show me everything I ever spent'") and ARCHITECTURE §3.1's "累计成本". The window preset control is therefore **hidden/inert** while Total is selected, and switching to Total drops any window. *Alternative considered (windowed total, relabeled "Total (last 30d)"):* simpler always-visible control, but narrows the headline number to 30 days and contradicts both the backend default and the "cumulative" framing — rejected.
- **Grouped (day / task_type / model)** computes the window client-side as `to = now()` / `from = to − Nd` (preset N ∈ {7,30,90}) and sends both as RFC3339, so the UI label and the data always agree (we send `from`/`to` explicitly rather than leaning on the server's grouped 30-day default). Every preset keeps `0 < to − from ≤ 366d`, so a grouped request can never hit the backend's 366-day cap (`400 invalid_input`). The server `to` is **right-exclusive** (`< to`); presets use `to = now()` so this is transparent, but any future day-boundary-snapped preset must account for it.

### D4. `group_by=none` omits the param entirely
The "Total" selection sends `GET /me/cost` with **no** `group_by` param (not `group_by=`) and, per D3, no `from`/`to` either. The empty-value case is a server tolerance, not the intended path; omitting the key is the clean ungrouped request and keeps the response on the `{total}` branch deterministically. The page render path MUST be **exhaustive** over the two `CostRollup` branches (a TS `never` check on the discriminant), so a server drift that emits neither/both keys fails loudly rather than rendering blank.

### D5. `TokenBar` is presentational and decimal-string-safe; it reuses the `CostBadge` amount rule
`TokenBar` takes a `CostSummary` and renders the amount via the same truncate-to-4-dp display logic plus the token/tool/wall fields as integers. To avoid duplicating the `displayAmount` helper, it is lifted into a shared `formatAmount`. **The single home is `web/src/features/costs/format.ts`** (alongside `barFraction` from D-below), and `CostBadge` (a `components/tasks` file) is refactored to import `formatAmount` from there — keeping the dependency one-way (`components → features`, matching how `CostBadge` already imports its types from `features/tasks/types`) and avoiding a `components ↔ features` cycle. This is a behavior-preserving refactor (still truncate-to-4-dp, full value in `title`), so it stays within the "no unrelated reformatting" red line (AGENTS.md §6). Bars (for the dashboard `items`) are width-`%` flex divs scaled by each item's amount **as a fraction of the max item amount** via `barFraction(amount, max)` — a string→bounded-`[0,1]` conversion used **only for the bar geometry**, never for display (and `barFraction(_, "0") → 0`, so an all-zero grouped window renders zero-width bars, not `NaN`). The displayed number is always the original string. *Alternative:* parse amounts to floats throughout — rejected; violates the decimal-string invariant for displayed values.

### D6. Empty and error states mirror the API's never-404 contract; the page owns its error UX via BOTH toast levers
`/me/cost` returns `data.total = zeroCost()` / `data.items = []` rather than 404, so the dashboard renders a "$0.00 — no spend in this window" empty state, never a not-found screen.

This codebase has **two independent toast levers** and the design must engage both deliberately (this was an earlier conflation): (1) the **transport** lever — `services/http.ts` `emitErrorToast` fires on every `ApiError` *unless* the `apiFetch` caller passes `toastOnError:false` (it does **not** exempt 4xx/404 — verified at `http.ts:105,121,160`); (2) the **React Query cache** lever — `services/query-client.ts` `onError` fires *unless* the query sets `meta:{silent:true}`. Suppressing one does not suppress the other.

Decision: the dashboard `/me/cost` read passes **both** `toastOnError:false` (transport) **and** `meta:{silent:true}` (cache), so the in-page "generic retry" message is the *sole* error surface — no double toast. `/me/cost` 400s (bad window) are not user-reachable because the UI only emits valid presets within the 366d cap (D3), so they too fall through to the in-page generic error path.

### D7. TaskDetail panel sources `total` from `/tasks/{id}/cost`, not the read DTO's `cost`
The read DTO already embeds a `cost` summary, but the dedicated endpoint is the spec'd source of truth for the cost panel. The panel uses `useTaskCostQuery(id)` (`/tasks/{id}/cost`) for the `total` it displays; the inline `CostBadge` continues to use the read DTO's `detail.cost` (cheap, already loaded). The two can momentarily differ during settle; that is acceptable and self-heals on refetch. *Alternative:* drive the panel from `detail.cost` and not call the new endpoint at all — viable and lighter, but then this change wouldn't actually exercise `task-cost-api`, leaving its web contract unverified; we prefer wiring the real endpoint.

The `/tasks/{id}/cost` response also carries `by_version`; we **model it in the type for response fidelity but intentionally do not consume it this round** — per-version cost already renders in `VersionTree` from the read DTO (Non-Goal #4). (`noUnusedLocals` is on, but it does not flag an unread field of a returned object, so the unused `by_version` is not a lint hazard; the type stays complete.)

On 404 handling for the panel: `useTaskCostQuery` mirrors `useTaskQuery` by **skipping retry on 404 and setting `meta:{silent:true}`** (suppressing the *cache* toast) — note this is *not* the same as fully silent: like `getTask`, a 404 envelope still hits the transport `emitErrorToast` unless `toastOnError:false` is also passed. Because the page is already gated by `useTaskQuery` (which resolved the task before the panel mounts), a panel-only 404 implies a mid-flight ownership/race change and is treated as a **defensive no-op** — the panel renders nothing/zero, it does NOT raise a second not-found screen (the task-level not-found handling stays authoritative). We do not bother suppressing the transport toast for this near-impossible path.

## Risks / Trade-offs

- **Bar geometry needs a number from a decimal string** → confine the parse to a single `barFraction(amount, max)` helper that returns a clamped `[0,1]` and is never used for any displayed value; unit-test that display always uses the raw string.
- **Two cost sources on TaskDetail (badge vs panel) could look inconsistent mid-settle** → both converge on refetch; acceptable for the MVP, documented in the spec scenario.
- **`format:check` is RED repo-wide from pre-existing drift** → only format the files this change touches; do not bulk-reformat (AGENTS.md §6).
- **No URL-persisted dashboard state** → reload resets to defaults (Total / 30d); acceptable, and a clean follow-up.
- **MSW fixtures must cover both rollup branches + the task-cost shape** → add a handler that switches on the presence of `group_by` so tests exercise both the `{total}` and `{group_by, items}` paths.

## Open Questions

- **Resolved:** Total = lifetime cumulative (no window); the window preset (default **30d**, matching the server's grouped default) appears and applies only for grouped views (see D3).
- Whether the dashboard should also expose a `task_type`/`model` legend beyond the `items` list — deferred; the keyed bar list is sufficient for the MVP.
