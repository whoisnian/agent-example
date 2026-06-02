## 1. `features/costs/` data slice

- [ ] 1.1 Add cost types in `web/src/features/costs/types.ts`: re-use `CostSummary` from `features/tasks/types`; add `CostGroupBy = "day" | "task_type" | "model"`, `CostGroupItem = { key: string; totals: CostSummary }`, and the discriminated `CostRollup = { total: CostSummary } | { group_by: CostGroupBy; items: CostGroupItem[] }`; add `TaskCostBreakdown = { task_id: string; total: CostSummary; by_version: Array<{ version_id: string; version_no: number; created_at: string; cost: CostSummary }> }`. Comment that `by_version` is modeled for response fidelity but intentionally unconsumed this round (per-version cost renders in `VersionTree` from the read DTO).
- [ ] 1.2 Add `web/src/features/costs/api.ts`: `getMyCost({ groupBy?, from?, to? }, signal?)` building the query string — for **Total** (ungrouped) OMIT `group_by` AND `from`/`to` entirely (lifetime open window, D3/D4); for grouped send `group_by` plus `from`/`to` as RFC3339. Pass `toastOnError:false` so the dashboard owns its error UX in-page (D6). `getTaskCost(taskId, signal?)` → `/api/v1/tasks/{id}/cost`. Mirror the `features/tasks/api.ts` `apiFetch` style.
- [ ] 1.3 Add `web/src/features/costs/queries.ts`: `costKeys` (`rollup({ groupBy, from, to })`, `taskCost(taskId)`), `useMyCostQuery(params)` (set `meta:{silent:true}` so neither toast layer fires — transport already suppressed in 1.2 — and the page renders the in-page error), and `useTaskCostQuery(taskId)` (skip retry on 404 + `meta:{silent:true}`, exactly mirroring `useTaskQuery`; note this suppresses the *cache* toast, the panel renders zero/no-op on the near-impossible 404 — it does NOT add a not-found screen).
- [ ] 1.4 Add `features/costs/queries.test.ts` (or `api.test.ts`) asserting: Total request omits BOTH `group_by` and `from`/`to`; a grouped request includes `group_by` + RFC3339 `from`/`to`; each window preset (7/30/90d) satisfies `0 < to−from ≤ 366d` and `from < to`; the rollup parses to the correct discriminated branch.

## 2. Shared amount formatting + `TokenBar`

- [ ] 2.1 Lift the decimal-string display helper into `web/src/features/costs/format.ts` as `formatAmount(amount: string): string` (the existing `displayAmount` truncate-to-4-dp logic), and refactor `CostBadge.tsx` to import it from there — keeps the dependency one-way (`components → features`, no cycle) and is behavior-preserving (full value still in `title`; stays within AGENTS.md §6 "no unrelated reformatting").
- [ ] 2.2 Add `barFraction(amount: string, max: string): number` in `features/costs/format.ts` (single home alongside `formatAmount`): returns a clamped `[0,1]` fraction for bar geometry ONLY (the only place a string amount becomes a number). Unit-test it, including `max = "0"` → 0 (no `NaN`).
- [ ] 2.3 Add `web/src/components/costs/TokenBar.tsx`: presentational, takes a `CostSummary`, renders amount via `formatAmount` plus input/output/cached tokens, tool calls, wall time. Decimal-string-safe (never `parseFloat` for display).
- [ ] 2.4 Add `components/costs/TokenBar.test.tsx`: amount comes from the string (truncated, not rounded), all token/tool/wall fields render, zero-cost renders all zeros.

## 3. CostDashboard page

- [ ] 3.1 Add `web/src/routes/CostDashboard.tsx`: grouping selector (Total / By day / By task type / By model) and window preset (7d / 30d / 90d, default 30d) as local `useState`. For **Total**, pass no `groupBy`/`from`/`to` (lifetime) and hide/disable the window control; for a grouping, compute `from`/`to` from the preset. Drive `useMyCostQuery`.
- [ ] 3.2 Render the discriminated result with an **exhaustive** branch (TS `never` check on the `group_by` discriminant): ungrouped → `TokenBar` over `total`; grouped → window-total header + a per-`key` row list **in server order (no client re-sort)**, each row = key + amount (`formatAmount`) + a proportional bar width from `barFraction(item, maxItemAmount)`. The `model` grouping's `"other"` key renders as an ordinary labeled row. Bars are CSS/flex, no charting lib.
- [ ] 3.3 Render loading / empty / error states: empty = zero/empty message (never a not-found screen, since the API zero-fills); a grouped all-zero window MUST still render its rows with zero-width bars (`barFraction(_, "0") → 0`); error = single in-page generic retry message (no duplicate toast — transport+cache both suppressed per 1.2/1.3).
- [ ] 3.4 Wire routing: in `web/src/router.tsx` replace `CostDashboardPlaceholder` import + `/cost` element with `CostDashboard`; delete `web/src/routes/placeholders/CostDashboardPlaceholder.tsx`.
- [ ] 3.5 Add `routes/CostDashboard.test.tsx`: default load (Total — omits `group_by` AND `from`/`to`, window control hidden/inert); switching to By day reveals the window and re-queries with `group_by=day` + `from`/`to`, rendering one row per key in server order; By model renders the `"other"` row; empty result renders the zero state; 5xx renders a single in-page error with no extra toast.

## 4. TaskDetail cost panel

- [ ] 4.1 In `web/src/routes/TaskDetail.tsx` mount a cost panel: call `useTaskCostQuery(id)` and render `TokenBar` over `total` near the existing header. Keep the inline `CostBadge` (driven by `detail.cost`) unchanged.
- [ ] 4.2 Handle the panel's loading and zero states; a cost-endpoint 404 is a defensive no-op (the page is already gated by `useTaskQuery`) — render nothing/zero, MUST NOT add a second not-found screen over the page's existing task not-found handling.
- [ ] 4.3 Extend `routes/TaskDetail.test.tsx`: cost panel shows the token breakdown from `/tasks/{id}/cost`; zero-cost renders an all-zero panel (not an error); badge and panel coexist.

## 5. Mocks, gates, and docs

- [ ] 5.1 Add MSW handlers in `web/src/test/mocks/handlers.ts` for `GET /me/cost` (switch on the `group_by` param → return `{total}` when absent vs `{group_by, items}` when present; the `model` fixture MUST include an `"other"` item so the "other" row is exercised) and `GET /tasks/:id/cost` (`{task_id, total, by_version}`); reuse `zeroCost()`.
- [ ] 5.2 Run `npm run typecheck`, `npm run lint`, `npm run test` — all green; format ONLY the files this change touched (`npx prettier --write` on the new/edited files — do NOT bulk-reformat; `format:check` is RED repo-wide from pre-existing drift).
- [ ] 5.3 Verify `docs/ARCHITECTURE.md §5.1 / §A.1` already describe the CostDashboard + TaskDetail cost panel; update only if the implemented surface diverges from the text (no speculative doc edits).
