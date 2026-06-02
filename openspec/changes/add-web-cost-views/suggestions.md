# Review: `add-web-cost-views`

**Summary.** This is a well-scoped, backend-faithful web slice. The discriminated-union modeling (D2), decimal-string discipline (D5), `group_by=none` omission (D4), and never-404 empty-state contract (D6) all match the archived `task-cost-api` spec. The proposal is largely correct. The findings below are mostly precision gaps rather than factual errors: a handful of under-specified scenarios (window-cap, `model`/"other" bucket, right-exclusive `to`, ungrouped open-window default), one genuine mismatch about how the dashboard's 5xx/404 toasts actually behave in this codebase (transport `toastOnError` vs React Query `meta.silent`), an ordering caveat for `group_by=day`, and a couple of task-list / consistency gaps. Nothing here is a hard blocker to the design; two items are "should-fix before apply" because they will otherwise produce divergent behavior or an untestable requirement.

**Severity counts:** blocker 0 · should-fix 6 · nice-to-have 7

---

## Findings

### 1. [should-fix] Dashboard error/empty toast behavior misdescribes this codebase's two-layer toast model
**File/section:** `design.md` §D6; `specs/web-cost-views/spec.md` "Cost Dashboard Page" → *Server/transport error shows generic retry*; `tasks.md` 1.3, 3.3.

D6 says "The cost queries opt into the global toast suppression … `/me/cost` 400s … fall through to the generic error path" and frames suppression as a single lever (`meta.silent`). But this codebase has **two** independent toast paths:
- Transport (`services/http.ts`): `emitErrorToast` fires on every non-zero envelope / 5xx / network error **unless the `apiFetch` caller passes `toastOnError:false`** (see `http.ts:160,170,205,217`). `features/tasks/api.ts` only sets `toastOnError:false` on the **mutations** (`createTask`/`iterateTask`/`controlTask`); the read `getTask` does **not**.
- React Query cache (`services/query-client.ts:5-10,41`): the global `onError` is suppressed by `meta:{silent:true}`.

Consequence: if `getMyCost`/`getTaskCost` are written like `getTask` (no `toastOnError:false`), a dashboard 5xx will fire a **transport toast** in addition to the in-page "generic retry" message the spec mandates — i.e. a double-surface. And `meta:{silent:true}` alone will **not** suppress it. The design needs to state explicitly which 5xx/4xx surface the user sees and which lever is used.

**Fix:** In D6 and tasks 1.2/1.3, decide and record: dashboard `/me/cost` reads should pass `toastOnError:false` (in-page error owns the UX, like the existing mutations) AND set `meta:{silent:true}` (cache layer); the task-cost read should mirror `useTaskQuery` (which currently still transport-toasts its 404 — see finding 2). State the chosen levers per query so the implementer doesn't accidentally produce a double toast.

### 2. [should-fix] "404 surfaces as a render state … mirroring `useTaskQuery`" overstates what `useTaskQuery` actually suppresses
**File/section:** `design.md` §D7; `specs/web-cost-views/spec.md` "Caller Cost Rollup Data Access" (the `/tasks/{id}/cost` paragraph); `specs/web-tasks-pages/spec.md` *Task Detail Cost Panel* requirement; `tasks.md` 1.3, 4.2.

The spec says a `/tasks/{id}/cost` 404 "surfaces as a not-found render state, never a thrown unhandled error" and tasks 1.3 says it must "treat 404 as a render state (no retry/toast), mirroring `useTaskQuery`". But `useTaskQuery` only (a) skips retry on 404 and (b) sets `meta:{silent:true}` (suppresses the **cache** toast). It does **not** pass `toastOnError:false`, so `getTask`'s 404 envelope still fires a **transport** toast today. "No toast" is therefore not actually true of the mirror target. More importantly, the cost-panel 404 should essentially never happen for an owned task that already loaded (`useTaskQuery` already resolved it); if it does 404 it means a race/ownership change. The requirement is fine, but "no toast … mirroring useTaskQuery" is imprecise.

**Fix:** Either (a) correct the claim to "skips retry on 404 and suppresses the cache toast (`meta.silent`), as `useTaskQuery` does; the panel renders nothing/zero rather than its own not-found screen," or (b) if a truly silent 404 is wanted, additionally pass `toastOnError:false` on `getTaskCost` and say so. Also clarify in §4.2 that because `useTaskQuery` gates the page, the panel's own 404 is a defensive no-op, not a second not-found screen.

### 3. [should-fix] `group_by=day` ascending order is by ISO `key` string, but task type/model ordering and tie semantics are unstated
**File/section:** `specs/web-cost-views/spec.md` "Caller Cost Rollup Data Access" → *Grouped rollup parses to the items branch* and "Cost Dashboard Page" → *Switching grouping re-queries*.

The backend spec (`task-cost-api` §"Caller-Scoped Cost Rollup Endpoint", line 74) guarantees `items` are **already ordered by `key` ascending** server-side for every grouping, not just `day`. The web scenarios say "ordered by `key` ascending" only for the `day` case and "in ascending order" for the switch scenario. For `task_type`/`model` the proposal never states whether the UI preserves server order or re-sorts (e.g. by amount). Re-sorting by amount on the client would silently break the documented "sum of items == total" reconciliation expectation and confuse `day` charts.

**Fix:** Add one sentence to the requirement: "The page MUST render `items` in the server-provided order (`key` ascending for all groupings) and MUST NOT re-sort." Optionally add a scenario asserting `group_by=model` rows render in the order returned (including the `"other"` bucket's position).

### 4. [should-fix] The `model` grouping's `"other"` bucket is never specified for the UI
**File/section:** `specs/web-cost-views/spec.md` "Cost Dashboard Page"; `tasks.md` 3.2, 5.1.

The backend collapses all non-llm `cost_events` into a single `"other"` bucket (`task-cost-api` spec line 82, and its scenario lines 111-114). The web spec lists "By model" as a selector option but never mentions that `"other"` will appear as a `key`, nor how the row label should read. A reviewer/implementer could assume model keys are always real model names and mis-handle/hide `"other"`. The MSW handler task (5.1) also doesn't call out exercising the `"other"` key.

**Fix:** Add a sentence to the dashboard requirement: "For `group_by=model`, a synthetic `key = "other"` bucket (non-LLM cost) MUST render as a normal row labeled `other`." Add a scenario or extend the By-day/By-model MSW fixture (5.1) to include an `"other"` item, so the rendering is tested.

### 5. [should-fix] Ungrouped window: spec says the UI always sends `from`/`to`, but the backend's ungrouped default is an OPEN window — the UI silently narrows "Total" to 30d
**File/section:** `design.md` §D3; `specs/web-cost-views/spec.md` *Window is sent explicitly as RFC3339* + *Default load is ungrouped 30-day total*.

Backend contract (`task-cost-api` line 68): **"When `group_by` is absent the window is open by default (no implicit bounds), matching 'show me everything I ever spent' semantics."** The proposal's D3 + scenario have the UI always send `from = now-30d` / `to = now`, including for the **Total** (ungrouped) view. So the dashboard's default "Total" shows only the **last 30 days**, not lifetime — which contradicts both the backend's ungrouped semantics and ARCHITECTURE §3.1 line 131 ("用户视角**累计**成本"). This may be intentional (consistent UI window across groupings), but it's a semantic decision that's currently buried and arguably wrong for a "cumulative cost" dashboard.

**Fix:** Make this an explicit decision. Either (a) keep the window on Total but rename the control/empty copy so the user knows it's a windowed total (e.g. "Total (last 30d)"), and note the deliberate divergence from the backend's open-default in D3; or (b) for the **Total** selection, omit `from`/`to` as well so it returns the lifetime total (the backend allows an open ungrouped window), and only apply the window presets to the grouped views. Pick one and encode it in a scenario.

### 6. [should-fix] No requirement/scenario covers the window-cap (`366d`) or `from >= to` 400s; D6 dismisses them as "not user-reachable" but the presets aren't proven safe
**File/section:** `design.md` §D6; `specs/web-cost-views/spec.md` (no scenario); `tasks.md` 3.1.

D6 asserts "`/me/cost` 400s (bad window) are not user-reachable because the UI only emits valid presets." With presets capped at 90d and `to=now`, `to-from ≤ 90d < 366d` and `from < to`, so that's true **today** — but it's an invariant the spec relies on without testing it, and a future preset (e.g. "1y" = 365d, or "All") would silently approach/cross the cap. There is no scenario pinning "presets always satisfy `0 < to-from ≤ 366d`."

**Fix:** Add a short note to the dashboard requirement that window presets MUST keep `0 < to-from ≤ 366d` (so grouped requests never 400), and ideally a unit test on the preset→`{from,to}` computation asserting each preset stays within the cap and `from < to`. Cheap insurance against a later preset addition breaking the grouped endpoint.

### 7. [nice-to-have] Right-exclusive `to` is a backend guarantee the UI inherits but never documents
**File/section:** `design.md` §D3; `specs/web-cost-views/spec.md` *Window is sent explicitly as RFC3339*.

The backend `to` predicate is strict `< to` (`task-cost-api` §"Right-exclusive `to` predicate", lines 116-119). With `to = now()` this is invisible, but if a future preset snaps `to` to a day boundary (e.g. "this month"), the last instant would be excluded. Not actionable now, just worth a one-line note so a later contributor adding boundary-snapped presets doesn't reintroduce off-by-one day bugs.

**Fix:** Add a parenthetical in D3: "the server `to` is right-exclusive (`< to`); presets use `to = now()` so this is transparent, but any future day-boundary preset must account for it."

### 8. [nice-to-have] Discriminated-union exhaustiveness / impossible-state handling isn't asserted
**File/section:** `specs/web-cost-views/spec.md` "Caller Cost Rollup Data Access"; `tasks.md` 1.4, 3.2.

D2 and the requirement correctly model `CostRollup` as a union and say consumers "MUST branch on the presence of the `group_by` key." But there's no scenario for the defensive case (a malformed payload carrying neither/both keys) and no statement that the dashboard render switch is exhaustive (TS `never` check). For an MVP this is fine, but a one-line "the render path MUST be exhaustive over the two branches" makes the requirement testable and guards against a silent blank render if the server ever drifts.

**Fix:** Optional scenario: "GIVEN a rollup payload, the page MUST render exactly one of the total/grouped branches selected by the presence of `group_by`." Add a `never`-exhaustiveness assert in 3.2.

### 9. [nice-to-have] `TaskList` cumulative-cost column status should be stated as explicitly out-of-scope (it already ships)
**File/section:** `proposal.md` "Impact"/"Out of scope"; cross-ref ARCHITECTURE §3.1 line 127.

ARCHITECTURE §3.1 line 127 calls for a TaskList "累计成本列". That column **already exists** today (`TaskList.tsx:71` header + `:88-90` `<CostBadge cost={t.cost}/>`), so it is correctly **not** in this change. The proposal never mentions it, which is fine, but a skeptical reader cross-checking ARCHITECTURE might think it was dropped. One line removes the ambiguity.

**Fix:** Add to the proposal's Out-of-scope/Impact: "TaskList's cumulative-cost column already ships (`CostBadge` per row) and is unchanged by this slice."

### 10. [nice-to-have] The deferred "live per-token meter" contradicts ARCHITECTURE §3.1 line 153 — call out the deliberate divergence
**File/section:** `proposal.md` Out-of-scope; `design.md` Non-Goals; cross-ref ARCHITECTURE line 153.

ARCHITECTURE §3.1 line 153 says TaskDetail's top bar shows "当前 running 版本的**实时累计**". The proposal defers the live meter (cost arrives via poll/refetch), which is a reasonable MVP cut given the realtime gateway streams status-only this round — but it is a stated divergence from the architecture doc. Per AGENTS.md §1 ("任何与该文档冲突的实现必须先更新文档或在 OpenSpec 提案中显式声明偏离原因"), this should be an explicit deviation note, not just a Non-Goal bullet.

**Fix:** In Non-Goals/Out-of-scope, explicitly frame it as a temporary divergence from ARCHITECTURE §3.1 line 153 with the reason (gateway streams status only this round; cost reflects on poll/refetch), so the doc-vs-impl gap is on record.

### 11. [nice-to-have] `by_version` is fetched and typed but never consumed — confirm it's intentional dead surface
**File/section:** `design.md` §D7; `specs/web-cost-views/spec.md` rollup requirement (`by_version` in the type); `tasks.md` 1.1.

D7 says the panel uses `total` and that `by_version` is fetched "for potential reuse," while Non-Goal #4 says per-version cost already renders in `VersionTree` from the read DTO, so `/versions/{id}/cost` isn't consumed. That's consistent, but it means `TaskCostBreakdown.by_version` is typed and returned yet unused by any surface this change ships — an unused field the linter may flag (`noUnusedLocals` is common in strict setups). Confirm the type carries it for fidelity only.

**Fix:** Add a one-line comment in 1.1 / D7: "`by_version` is modeled for response fidelity but intentionally unconsumed this round (per-version cost renders in `VersionTree` from the read DTO)." Avoids a reviewer thinking a surface was forgotten.

### 12. [nice-to-have] `formatAmount` lift location is ambiguous ("export from CostBadge OR a util") — pick one to avoid a circular import
**File/section:** `design.md` §D5; `tasks.md` 2.1.

D5/2.1 leave the shared `formatAmount` home open: "exported from `CostBadge` or a small `features/costs` util." If `CostBadge` (a `components/tasks` file) exports it and `features/costs/format.ts` also lives there, you risk `components → features` and `features → components` cross-pulls. Cleaner: define `formatAmount` in `features/costs/format.ts` (alongside `barFraction` from 2.2) and have `CostBadge` import it. Decide now so the import direction is one-way (`components` → `features`, matching the existing `CostBadge` importing types from `features/tasks/types`).

**Fix:** In 2.1 pick `features/costs/format.ts` as the single home for `formatAmount` + `barFraction`; `CostBadge` consumes `formatAmount` from there. Note this is a behavior-preserving refactor of `displayAmount` (still truncate-to-4dp, full value in title), so it stays within the "no unrelated reformatting" red line (AGENTS.md §6).

### 13. [nice-to-have] Tasks list ordering: TokenBar (task 2.3) depends on `formatAmount` lift (2.1) — fine — but 3.2 also depends on 2.2 `barFraction`; make the cross-section dependency explicit
**File/section:** `tasks.md` §2 vs §3.

Section 3 (CostDashboard) task 3.2 consumes `barFraction` (defined in 2.2) and `formatAmount` (2.1) and `TokenBar` (2.3). The numbering implies §2 fully precedes §3, which is correct, but 3.2's reliance on 2.2's `max="0" → 0` edge case (empty/all-zero items) isn't restated where the dashboard's zero/empty state is built (3.3). A grouped-but-all-zero window would call `barFraction(item, "0")`.

**Fix:** Add to 3.3 (or 3.2): "when all item amounts are `"0.00000000"` (max is zero), every bar width MUST be 0 (via `barFraction(_, "0") → 0`), and the rows still render." Ensures the empty/zero grouped path is covered, not just the ungrouped zero state.
</content>
</invoke>
