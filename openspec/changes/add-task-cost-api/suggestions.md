# Independent Review: `add-task-cost-api`

Reviewer notes for the lead engineer. Not a request to change everything — pick what's load-bearing, ignore what isn't. Severities are honest.

## Lead's verdict (applied after review)

| # | Verdict | Notes |
|---|---|---|
| S1 | accepted (must-fix) | `GetTaskCostWithOwner` now drives from `tasks LEFT JOIN task_costs … GROUP BY t.id`; design D4 + spec semantics updated. |
| S2 | accepted (must-fix) | SQL pinned with `date_trunc('day', occurred_at, 'UTC')` (PG16+ three-arg form); spec scenario clarifies "deterministic regardless of session timezone". |
| S3 | accepted (must-fix) | Replaced every "404 not_found" with the concrete subcodes `task_not_found` / `version_not_found` per the existing `MapError` mapper. |
| S4 | accepted (must-fix) | Tasks + design now reference the existing `ErrTaskNotFound` / `ErrVersionNotFound` sentinels; no new error type. |
| S5 | accepted (nice-to-have) | Added a design note that the CASE-based `group_by` query MUST verify generated sqlc types at apply time; fallback to three split queries documented. |
| S6 | accepted (should-fix) | Design D4 spells out the per-column `/me/cost` SQL — `tool_calls`, `wall_time_ms`, token columns — mirroring the cost-ingest aggregate mapping so divergence from `task_costs` is explicit. |
| S7 | accepted (should-fix) | Added a soft cap: `to - from ≤ 366 days` for `group_by` queries; default `to = now()`, `from = to - 30d` when both absent. |
| S8 | accepted (should-fix) | `OwnerCostRollup` split into two distinct DTOs (`OwnerCostTotal` / `OwnerCostGrouped`); application returns whichever the request asked for; handler renders directly. |
| S9 | accepted (nice-to-have) | Former Open Questions promoted to Decisions D11 / D12 / D13; Open Questions section now empty. |
| S10 | accepted (nice-to-have) | Added explicit `0.015` / `0.0001` / negative cases to the numeric-rendering unit-test task. |
| S11 | accepted (nice-to-have) | Spec now binds `/pricing` as read-only + identical-for-all-callers; mutation explicitly out of scope. |
| S12 | accepted (nice-to-have) | Design D4 documents the orphan `cost_events.task_id` exclusion as intentional defense-in-depth. |
| S13 | accepted (nice-to-have) | Empty `?group_by=` now treated as absent (matches `task_reads.go` sibling convention); D5 wording corrected. |
| S14 | accepted (nice-to-have) | Spec adds a one-line note on negative `unit_price_usd` rendering (leading `-`, no clamp). |
| S15 | accepted (nice-to-have) | Tasks 5.1 + 5.2 explicitly require an owner-agnostic `/pricing` assertion. |

---

## S1. `GetTaskCostWithOwner`'s join shape will return `ErrNotFound` for an owned task with zero versions

**Issue.** Design D4 says the query "composes the existing `GetTaskCost` aggregate with an ownership probe (`JOIN tasks ON tasks.id = $task_id AND tasks.tenant_id = $tenant AND tasks.user_id = $user`). Returns no rows when the task is unknown OR unowned." But if the driving table is `task_costs` and `tasks` is joined with `JOIN` (not `LEFT JOIN`), then an **owned task with no versions / no settled events** also returns 0 rows — because there's no `task_costs` row to anchor the join. The service layer would then map to `ErrTaskNotFound`, contradicting the spec scenario "Owned task with no versions yet" → `200 + zeroCost()`.

**Evidence.**
- design.md lines 59 (`JOIN tasks ON tasks.id = $task_id ...`)
- spec.md lines 17–20 ("Owned task with no versions yet ... HTTP `200` with `data.total = zeroCost()`")
- For comparison, existing `api/queries/task_costs.sql` `GetTaskCost` (lines 8–22) has no GROUP BY and always returns 1 row with `COALESCE(SUM, 0)` regardless of whether the task exists — so it cannot do the ownership check by itself either.

**Suggested fix.** Make the query drive from `tasks`, LEFT JOIN to `task_costs`, and `GROUP BY t.id`:
```sql
SELECT t.id AS task_id,
       COALESCE(SUM(tc.input_tokens), 0)::bigint AS input_tokens, ...
       COALESCE(SUM(tc.amount_usd), 0)::numeric  AS amount_usd
FROM tasks t
LEFT JOIN task_costs tc ON tc.task_id = t.id
WHERE t.id = $1 AND t.tenant_id = $2 AND t.user_id = $3
GROUP BY t.id;
```
With `GROUP BY t.id` the result is 1 row when owned (with zero aggregates if no `task_costs`), 0 rows when unknown / unowned. Spelling this out in design D4 also clarifies the otherwise easy-to-get-wrong "no GROUP BY = always 1 row with zeros" pgSQL gotcha.

**Severity.** must-fix.

---

## S2. `date_trunc('day', occurred_at)` truncates in session timezone, not UTC

**Issue.** Design D5 SQL uses `to_char(date_trunc('day', ce.occurred_at), 'YYYY-MM-DD')` and the spec scenario says "(UTC)". But `occurred_at` is `timestamptz` and `date_trunc(text, timestamptz)` truncates *in the session's `TimeZone` setting*. If the API process happens to connect with a non-UTC `TimeZone` (the pgx pool inherits the server's `timezone` GUC, which on many distros defaults to `localtime`), the day boundary moves and the `key` for an event at `2026-05-30T23:30:00Z` could become `"2026-05-31"` (CST) or `"2026-05-30"` (UTC). The spec scenario `key="2026-05-30"` only holds under the unstated assumption that session TZ is UTC.

**Evidence.**
- design.md line 74 (`to_char(date_trunc('day', ce.occurred_at), 'YYYY-MM-DD')`)
- spec.md line 69 ("`day`: bucket by `date_trunc('day', occurred_at)` and emit the `key` as the ISO date string `YYYY-MM-DD` (UTC)")
- Postgres 18 supports the three-arg form `date_trunc('day', occurred_at, 'UTC')` which is exactly what's wanted.

**Suggested fix.** Pin UTC explicitly in the SQL — either
```sql
to_char(date_trunc('day', ce.occurred_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD')
```
or the PG16+ form
```sql
to_char(date_trunc('day', ce.occurred_at, 'UTC'), 'YYYY-MM-DD')
```
and tighten the spec wording from "(UTC)" to "`date_trunc('day', occurred_at AT TIME ZONE 'UTC')`, so the bucket is deterministic regardless of the connection's session timezone."

**Severity.** must-fix.

---

## S3. Spec says `404 not_found` but the codebase emits `task_not_found` / `version_not_found`

**Issue.** The spec uses the phrase "HTTP `404 not_found`" several times (e.g. "MUST return HTTP `404 not_found` with the unified envelope"), but the existing `MapError` (and the integration tests written for `task-read-api`) emit code `task_not_found` for tasks and `version_not_found` for versions. If the new spec literally binds the string `"not_found"` it conflicts with how the read API spec is bound and how downstream tests / web clients are written.

**Evidence.**
- spec.md lines 10, 30, 53, 142–143, 150, 155 (uses "404 not_found")
- `openspec/specs/task-read-api/spec.md` lines 119, 124, 128 (uses `code = "task_not_found"` / `"version_not_found"`)
- `api/internal/interfaces/http/errors.go` lines 57–60 (returns `"task_not_found"` / `"version_not_found"`)
- `api/internal/interfaces/http/task_reads_integration_test.go` lines 230–231, 237 (asserts `"task_not_found"`)

**Suggested fix.** Replace each "404 not_found" with the concrete subcode the existing mapper emits: `task_not_found` for `/tasks/{id}/cost`, `version_not_found` for `/versions/{id}/cost`. The two non-id-bearing endpoints (`/me/cost`, `/pricing`) don't 404 at all, so the catch-all language doesn't need to apply there.

**Severity.** must-fix.

---

## S4. Tasks reference an `ErrNotFound` that doesn't exist in the codebase

**Issue.** Tasks 2.2 says "maps `pgx.ErrNoRows` / unowned rows to `ErrNotFound`". The existing domain package only exposes `ErrTaskNotFound` and `ErrVersionNotFound`; there is no generic `ErrNotFound`. Design D3 also mentions a single "`ErrNotFound`" mapping. If the implementation actually introduces a new generic error, the HTTP `MapError` will need to grow a new case and the response code will need a new subcode — neither is binding-compatible with S3.

**Evidence.**
- tasks.md line 14 ("maps `pgx.ErrNoRows` / unowned rows to `ErrNotFound`")
- design.md line 53 ("a mismatch returns `ErrNotFound`, which the HTTP layer maps to **404 not_found**")
- `api/internal/domain/task/errors.go` lines 20, 26 (only `ErrTaskNotFound` / `ErrVersionNotFound` exist)
- `rg ErrNotFound api/` returns no matches.

**Suggested fix.** Reuse the existing sentinels per endpoint: `GetTaskCost` → `ErrTaskNotFound`, `GetVersionCost` → `ErrVersionNotFound`. Rewrite the tasks.md / design.md "maps to `ErrNotFound`" lines accordingly. No new error type required.

**Severity.** must-fix.

---

## S5. The CASE-based `group_by` query is one new sqlc pattern; the design should ship a sample of the generated Go signature

**Issue.** Design D5 SQL uses `CASE sqlc.arg('group_by')::text WHEN 'day' THEN ... END AS key`. No existing query in this codebase uses `sqlc.arg` inside a CASE expression — the closest is `narg('status')` in `tasks.sql` as a NULL-or-eq filter. sqlc *should* handle a `sqlc.arg('group_by')::text` like any other text param, but binding a text arg into a `CASE` discriminator that yields multiple typed expressions can occasionally produce a Go signature where the result columns are inferred as `interface{}` (because Postgres reports the column as `text` from CASE — usually fine, but worth verifying once before committing the generated code).

**Evidence.**
- design.md lines 71–88
- `grep CASE api/queries/` finds no existing usage.
- `api/queries/tasks.sql` lines 23, 54 show the only existing `narg(...)::text` idiom.

**Suggested fix.** Either (a) run `make sqlc` against a minimal scratch version of the query before locking in design/spec and paste the resulting `GroupOwnerCostsRow` shape into the design as a sanity check, or (b) split into three queries (`GroupOwnerCostsByDay`, `...ByTaskType`, `...ByModel`) — design called this out as "Alternative considered" and rejected for code-bloat reasons, which I agree with *if* (a) confirms sqlc generates clean types. nice-to-have, but cheap insurance.

**Severity.** nice-to-have.

---

## S6. `/me/cost` totals don't share a denominator with `task_costs` for `tool_calls` / `wall_time_ms`

**Issue.** Design D4 commits to "always sum `cost_events` for `/me/cost`", which is the right call for `amount_usd`. But the `CostSummary` shape also carries `tool_calls` and `wall_time_ms`. `cost_events` has columns `calls` (int4, nullable) and `duration_ms` (int8, nullable) — not `tool_calls` / `wall_time_ms`. The cost-ingest spec defines a per-kind mapping that, for example, defaults `calls ?? 1` for tool events when computing `task_costs.tool_calls`. A naive `SUM(cost_events.calls)` won't match `SUM(task_costs.tool_calls)` whenever a worker emits a tool event with `calls = NULL`. Similarly `SUM(cost_events.duration_ms)` over all kinds includes compute events, whose `duration_ms` is *not* folded into `task_costs.wall_time_ms` (compute_seconds is the only compute aggregate).

The spec only binds reconcilability on `amount_usd` (line 95 scenario), so this is not a contract bug — but it *will* surprise the first developer who tries to compare `/me/cost` totals with the sum of `/tasks/{id}/cost` totals in a UI dashboard.

**Evidence.**
- design.md line 65 ("the token / call / duration columns also live on `cost_events` directly, so the totals shape stays identical to `CostSummary`")
- `openspec/specs/task-cost-ingest/spec.md` lines 81–90 (per-kind mapping, `calls ?? 1`, compute_seconds = floor(duration_ms/1000), wall_time_ms unchanged for compute)
- `api/internal/infrastructure/persistence/sqlc/models.go` lines 22–39 (`cost_events` columns: `Calls *int32`, `DurationMs *int64`)

**Suggested fix.** Either (a) explicitly document the divergence in design D4: "`/me/cost` rolls up directly from `cost_events`, so `tool_calls = SUM(COALESCE(calls, 1)) FILTER(WHERE kind='tool')` and `wall_time_ms = SUM(COALESCE(duration_ms,0)) FILTER(WHERE kind IN ('llm','tool'))` — these are kind-gated mirrors of the task_costs mapping; only `amount_usd` is guaranteed to reconcile bit-for-bit with `task_costs`". Or (b) take the simpler shortcut: build the no-group_by `/me/cost` from `task_costs` joined to `tasks`, and the grouped version from `cost_events` joined to `tasks`. The two will diverge in subtle ways under filtering, but at least the unfiltered shape is consistent with the embedded `cost` blocks the UI already renders.

I'd lean (a) — keep one source, write down what the per-column SQL is.

**Severity.** should-fix.

---

## S7. `/me/cost` has no caller-side cardinality / window bound

**Issue.** Design D10 acknowledges "if `/me/cost?group_by=day` over a year-wide window exceeds the cardinality budget" but punts the limit to a follow-up. Spec accepts arbitrary `from` / `to` and groups by day with no upper bound on `to - from`. A `?from=1900-01-01&to=2100-01-01&group_by=day` request would scan-aggregate ~73,050 days into a single JSON response. Even with cost_events being small in MVP this is a cheap DoS shape: large groupby keys, large response, large allocator pressure.

**Evidence.**
- design.md lines 116–117 (D10, no pagination)
- spec.md lines 55–66 (no max window stipulation)
- `cost_events_task_occurred_idx` covers the WHERE but the GROUP BY cardinality is unbounded.

**Suggested fix.** Add a soft cap to the spec: when `group_by` is present, `to - from` MUST be ≤ N days (e.g., 366) or the API returns `400 invalid_input` naming `to`. Default the window when both are absent so a no-arg `?group_by=day` doesn't accidentally span all time — e.g., default `to = now()`, `from = to - 30d`. This is a one-line guard in the HTTP layer, doesn't introduce real pagination, and removes the obvious cliff.

**Severity.** should-fix.

---

## S8. `OwnerCostRollup`'s discriminated payload as one Go struct will JSON-encode the wrong shape

**Issue.** Tasks 2.1 describes `OwnerCostRollup` as `Total *CostSummary` (nil when grouping) + `GroupBy string + Items []OwnerCostGroup` (zero-value when no group). Encoded naïvely, the no-group_by response will carry `{"total": {...}, "group_by": "", "items": null}` and the grouped response will carry `{"total": null, "group_by": "day", "items": [...]}`. Spec line 63 says `data = {total}` (one field) and line 65 says `data = {group_by, items}` (two fields) — explicitly discriminated.

**Evidence.**
- tasks.md line 13 ("OwnerCostRollup (discriminated: Total *CostSummary for no-group_by; GroupBy string + Items []OwnerCostGroup for grouped)")
- spec.md lines 63, 65 (separate shapes per branch).

**Suggested fix.** Either (a) tag the struct fields `,omitempty` and use pointer / nil-slice semantics carefully (`Items []OwnerCostGroup` will encode as `null` when nil — use `Items []OwnerCostGroup` initialised to `[]OwnerCostGroup{}` when grouping and nil otherwise; same for Total). Or (b) define two distinct return types `OwnerCostTotal` / `OwnerCostGrouped` and have the handler render whichever the service returns. (b) is cleaner; the application-layer signature can be `(*OwnerCostTotal, *OwnerCostGrouped, error)` or simpler, a sum-type via an `interface { isOwnerCost() }`. (a) works and is shorter — just make sure the test asserts JSON keys, not just decoded fields.

**Severity.** should-fix.

---

## S9. The three "Open Questions" in design.md are not actually open

**Issue.** Each of the three Open Questions has its own answer in the same paragraph ("Decision: ..."). They read as "decided in writing, but kept in the Open Questions section". That's fine for the historical narrative but pollutes the section's intent — a reviewer scanning Open Questions to see what still needs resolution gets misled.

**Evidence.**
- design.md lines 133–138 (three numbered items each starting "Decision: ...")

**Suggested fix.** Move each into the **Decisions** block as D11 / D12 / D13 (occurred_at vs created_at; no `?at=` time machine; LEFT JOIN includes versions without cost rows). Leave **Open Questions** empty (or remove the section).

**Severity.** nice-to-have.

---

## S10. `/pricing` decimal-string contract is correct but worth a unit test for the `0.015` edge case

**Issue.** The seeded value `0.015` (NUMERIC(18,8)) round-trips through pgtype.Numeric with `Int=1500000, Exp=-8`. `numericToDecimalString` produces `"0.01500000"` correctly — I traced the arithmetic: `exp=0`, `scaled=1500000`, pad-left to 9 chars, split at 8 from the right → `"0.01500000"`. The spec scenario binds this exact behavior (line 138–139), so it's covered. **No bug** — but the existing `numericToDecimalString` unit tests live in `read_dtos_test.go`; the pricing-seed value `0.015` is a different exponent shape (Exp=-8) than `0.06750000` (Exp=-8 with non-trailing-zero significand) and is worth a dedicated row in the test table to make the regression intent explicit.

**Evidence.**
- spec.md lines 137–139
- `api/internal/domain/task/read_dtos.go` lines 162–191
- `api/migrations/0005_seed_pricing.up.sql` lines 24, 33–35

**Suggested fix.** Add a numeric-rendering subtest in tasks 2.4 explicitly covering `0.015` → `"0.01500000"`, `0.0001` → `"0.00010000"`, and a negative (e.g. `-0.5` → `"-0.50000000"`) so the decimal-string contract for prices is locked.

**Severity.** nice-to-have.

---

## S11. `/pricing` is owner-agnostic; spec should explicitly note no auth / no rate limit

**Issue.** Spec line 123: "The endpoint is owner-agnostic: the pricing table is shared across tenants in MVP." But: there's no statement that any authenticated user can hit it, nor whether unauthenticated callers can. Today the dev-mode middleware injects a fixed `(tenant, user)` regardless, so it's moot, but the spec is the bind point for the eventual real-auth proposal. Worth saying explicitly: "any authenticated caller MAY GET /pricing; the response is identical for every caller; no per-tenant filtering is applied". Then a future per-tenant pricing tier proposal has something concrete to override.

Also AGENTS.md §6 red-line "不要修改 pricing 表已生效记录的单价" — the spec doesn't currently say *anywhere* that `/pricing` is read-only (no `POST` / `PUT` / `PATCH` / `DELETE` is implied by "the four GETs", but it's not bound). One sentence: "Pricing is read-only via this capability; mutation lives outside the API surface (DBA-only in MVP)." closes the loop.

**Evidence.**
- spec.md line 123 (current wording)
- AGENTS.md §6 ("不要修改 pricing 表已生效记录的单价；变更价格通过新增带 effective_at 的行")
- design.md line 26 ("Pricing CRUD (admin/ops surface; Post-MVP).") — explicit but in Non-Goals, not in the binding spec.

**Suggested fix.** Add a brief paragraph under the `/pricing` requirement stating: "All authenticated callers see the same response (no tenant filtering). The capability MUST NOT expose write verbs; pricing mutation is out of scope (cf. add-task-cost-api Non-Goals)." Move the "no mutation" sentence from design Non-Goals into the spec where it binds.

**Severity.** nice-to-have.

---

## S12. Spec doesn't pin what happens when `cost_events.task_id` references a task with no `tenant_id` / no `user_id` row

**Issue.** Design D4 / D5 join `cost_events → tasks` on `task_id`. Since `cost_events` has no FK to `tasks` (verify: `0003_init_cost_domain.up.sql` lines 37–58 confirms — only `pricing_id` FK), an orphaned `cost_events.task_id` would silently drop from `/me/cost` (the JOIN finds no row, the WHERE on tenant_id makes it disappear). This is the *right* behavior for MVP — orphans shouldn't be billed — but the spec doesn't say so. A future maintainer might "fix" it with `LEFT JOIN` and accidentally include orphaned rows in `/me/cost` totals.

**Evidence.**
- `api/migrations/0003_init_cost_domain.up.sql` lines 37–58 (no FK on `cost_events.task_id`)
- `openspec/specs/task-cost-ingest/spec.md` lines 67–75 (DLQs mismatched task_id, but only when version_id is known — an unknown task_id with a known version_id wouldn't actually be flagged because the settler only checks `version_id → task_id`).

**Suggested fix.** Add a one-line scenario or a parenthetical to D4: "Events whose `task_id` no longer maps to any `tasks` row are silently excluded from `/me/cost` rollups — by design, since they cannot be attributed to a caller. This should be impossible in steady state because of the cost-ingest task_id-immutability check, but the JOIN is intentionally inner-join'd as defense in depth."

**Severity.** nice-to-have.

---

## S13. Empty-value vs absent `?group_by=` — spec, design, and existing handler convention disagree

**Issue.** Design D5 line 69 says "any other value (including empty after explicit `?group_by=`) is rejected with 400". But the existing `TaskReadHandlers.listTasks` treats `c.Query("status")` returning `""` the same way for both absent and `?status=` — and the spec scenario "Invalid group_by returns 400" only mentions `group_by=hour`, not the empty case. Tasks 4.2 doesn't pin which approach to take.

Either approach is defensible; what's not defensible is "design says reject empty; spec doesn't bind it; tests aren't written for either; existing sibling handler does the opposite". One of those three has to give.

**Evidence.**
- design.md line 69 ("any other value (including empty after explicit `?group_by=`) is rejected")
- spec.md lines 107–109 (no empty case)
- `api/internal/interfaces/http/task_reads.go` line 58 (`if raw := c.Query("status"); raw != ""` — empty = treated as absent)

**Suggested fix.** Align with the existing sibling handler: treat empty as absent (no `group_by`). Remove the "(including empty after explicit `?group_by=`)" phrase from D5. The savings in user-friendliness are negligible and the cost in handler complexity (using `c.GetQuery` instead of `c.Query`) is real.

**Severity.** nice-to-have.

---

## S14. `numericToDecimalString` and negative prices

**Issue.** D7 says nothing about negative `unit_price_usd`. The DB has no CHECK against it (`api/migrations/0003_init_cost_domain.up.sql` line 22 — only `NUMERIC(18,8) NOT NULL`). The renderer produces `"-X.XXXXXXXX"` for negatives (read_dtos.go line 178–189 handles `neg`). A negative price in a seed migration error would surface as a negative string to the client, which might or might not be parsed correctly by JS decimal libraries. Probably nobody will ever seed a negative price, but: the spec is the bind point.

**Evidence.**
- `api/migrations/0003_init_cost_domain.up.sql` line 22 (no constraint)
- `api/internal/domain/task/read_dtos.go` lines 178–189 (negative-aware rendering)
- spec.md does not address this.

**Suggested fix.** Either add a DB CHECK in a future migration (out of scope for this change) or add a one-line clarification in the spec: "Negative `unit_price_usd` values, while not constrained by the schema, are not expected and are not specially handled; if one is encountered the renderer SHALL emit it with a leading `-` (no clamp to zero)." Closes the question without expanding scope.

**Severity.** nice-to-have.

---

## S15. Tasks 5.2 includes "cross-owner isolation" for `/me/cost` but spec scenarios already bind it — good; but the integration test should also cover `/pricing` being owner-agnostic explicitly

**Issue.** Tasks 5.2 covers `/me/cost` cross-owner isolation. Spec has the matching scenario (lines 102–105). Good. But for `/pricing`, tasks 5.2 says "returns seed rows + ordering + decimal-string unit_price_usd" — no explicit cross-owner test. Since `/pricing` is owner-agnostic by design (spec line 123), it's worth a one-line subtest that asserts: requests from `(devTenant, devUser)` and from `(otherTenant, otherUser)` return identical bodies. Cheap to add, prevents a future regression where someone accidentally adds an `owner = ?` filter.

**Evidence.**
- tasks.md line 32 (pricing test list omits owner-agnostic assertion)
- spec.md line 123 (binds owner-agnostic).

**Suggested fix.** Add a subtest under task 5.2's `/pricing` group: "two different (tenant, user) callers see identical `data.items`". The dev-mode middleware injects a single identity, so this requires a small handler-test fork or a per-request header — easiest with the handler-level test (5.1) rather than the integration test.

**Severity.** nice-to-have.

---

## Summary

**Overall assessment.** This is a careful, well-scoped proposal. It mostly inherits proven patterns from `add-task-read-api` and adds a thin but cleanly-shaped surface. The data sources are reasonable (cost_events for the rollup, task_costs for detail), the decimal-string convention is consistent, and the non-goals are honest. The integration-test plan (tasks 5.2) covers the right corners.

**Top 3 must-fix items.**
1. **S1** — `GetTaskCostWithOwner` join shape: the current design wording (`JOIN tasks`, no GROUP BY / no LEFT JOIN of task_costs) would 404 an owned-but-empty task and contradict the spec's own "no versions yet" scenario. Drive from `tasks LEFT JOIN task_costs GROUP BY t.id`.
2. **S2** — `date_trunc('day', occurred_at)` truncates in session timezone, not UTC; pin `AT TIME ZONE 'UTC'` in the SQL and the spec.
3. **S3 + S4** (paired) — Spec says "404 not_found" but the codebase emits `task_not_found` / `version_not_found`; tasks reference a non-existent `ErrNotFound` sentinel. Align spec subcodes and reuse the existing `ErrTaskNotFound` / `ErrVersionNotFound`.

**Implementable as written?** Almost. After folding in S1–S4 (which together are ~10 line changes across design.md / spec.md / tasks.md and don't change scope), the proposal can be implemented straight from the artifacts. S5–S15 are clarity / future-proofing nits that won't block merge, but S6 (token/calls/wall-time reconcilability) is worth at least writing down before someone tries to compare `/me/cost` with the sum of `/tasks/{id}/cost`.
