# web-cost-views Specification

## Purpose
TBD - created by archiving change add-web-cost-views. Update Purpose after archive.

## Requirements
### Requirement: Caller Cost Rollup Data Access

The web client SHALL provide a `features/costs/` data-access slice exposing typed access to `GET /api/v1/me/cost` through the existing `apiFetch` + React Query pattern. The slice MUST model the discriminated response faithfully: the parsed value is `{ total: CostSummary }` when no grouping was requested, or `{ group_by: CostGroupBy; items: CostGroupItem[] }` when grouping was requested, where `CostGroupBy ∈ {day, task_type, model}` and each `CostGroupItem` is `{ key: string; totals: CostSummary }`. Consumers MUST branch on the presence of the `group_by` key, never on optional fields.

Every monetary value (`amount_usd` on any `CostSummary`) MUST be treated as a decimal STRING and MUST NOT be parsed to `number` for display, consistent with the existing `CostBadge` rule. The React Query key MUST incorporate the grouping and the time window so that changing either is a normal cache-keyed refetch.

The slice MUST preserve the server-provided ordering of `items` (the backend returns them `key` ascending for every grouping) and MUST NOT re-sort them client-side (e.g. by amount), so the rendered order stays reconcilable with the backend contract.

The slice MUST also expose typed access to `GET /api/v1/tasks/{task_id}/cost` returning `{ task_id, total: CostSummary, by_version: Array<{ version_id, version_no, created_at, cost: CostSummary }> }`, scoped to the task owner. A 404 MUST skip retry and suppress the React Query cache toast (`meta.silent`), surfacing as a no-op/zero render state rather than a thrown unhandled error. `by_version` MAY be modeled in the type for response fidelity even though no surface consumes it this round.

#### Scenario: Ungrouped (Total) rollup omits both grouping and window
- **WHEN** the slice requests the Total rollup
- **THEN** the request MUST omit the `group_by` query parameter entirely AND MUST omit `from`/`to` (the lifetime open window, matching the backend's ungrouped default), AND the parsed result MUST be the `{ total }` shape with `total.amount_usd` kept as the raw decimal string

#### Scenario: Grouped rollup parses to the items branch in server order
- **WHEN** the slice requests `/me/cost?group_by=day` for a window with two settled days
- **THEN** the parsed result MUST be the `{ group_by: "day", items }` shape, `items` MUST be rendered in the server-provided `key`-ascending order WITHOUT client re-sorting, and each `item.totals.amount_usd` MUST be kept as the raw decimal string

#### Scenario: Grouped window is sent explicitly as RFC3339 within the 366d cap
- **WHEN** the slice requests a grouped rollup for a window preset of N days (N ∈ {7, 30, 90})
- **THEN** the request MUST send `from` and `to` as RFC3339 timestamps computed client-side (`to = now`, `from = to − Nd`) so the displayed window label and the returned data agree, AND every preset MUST satisfy `0 < to − from ≤ 366 days` so a grouped request never trips the backend's window-cap `400`

### Requirement: Cost Dashboard Page

The web client SHALL render a Cost Dashboard at the `/cost` route, replacing the placeholder. The page MUST source its data from the caller cost rollup (`GET /me/cost`) and MUST be owner-scoped (it shows only the caller's spend).

The page MUST provide a grouping selector with the choices **Total** (ungrouped), **By day**, **By task type**, and **By model**. **Total MUST request the lifetime cumulative spend** — it sends neither `group_by` nor `from`/`to` (matching the backend's open-window ungrouped default and ARCHITECTURE §3.1 "累计成本"). A time-window preset selector (at least **7d**, **30d**, **90d**, default **30d**) MUST be presented and apply **only** for the grouped choices; while **Total** is selected the window control MUST be hidden or inert and MUST NOT bound the total. Changing either control MUST re-query via the cache key without a full reload.

When ungrouped (Total), the page MUST display the lifetime total as a `CostSummary` breakdown (amount plus input / output / cached tokens, tool calls, wall time). When grouped, the page MUST display the window total header PLUS a list of per-`key` rows, each showing the `key`, the row's amount, and a proportional bar, rendered in the server-provided `key`-ascending order (no client re-sort). For **By model**, the backend's synthetic `key = "other"` bucket (non-LLM cost) MUST render as an ordinary row labeled `other`, not be hidden or special-cased. Bar geometry MAY derive a bounded `[0,1]` number from the amount string for width only (as a fraction of the max item amount); when every item amount is zero the bars MUST all be zero-width (no `NaN`), and the rows MUST still render. Every DISPLAYED amount MUST remain the original decimal string (truncated for display exactly as `CostBadge` does, never rounded from a float).

The render path MUST be exhaustive over the two `CostRollup` branches, selecting the branch by the presence of the `group_by` key (a malformed payload carrying neither/both keys MUST fail loudly, not render blank).

The page MUST render distinct loading, empty, and error states. An empty result (the API returns `total = zeroCost()` / `items = []`, never 404) MUST render a zero/empty state (e.g. "no spend"), NOT a not-found screen. The `/me/cost` read MUST own its error UX in-page: it MUST suppress BOTH the transport toast (`toastOnError:false`) AND the React Query cache toast (`meta.silent`), so a transport/server error renders a single in-page generic retry message with no duplicate toast.

#### Scenario: Default load is the ungrouped lifetime total
- **WHEN** the user navigates to `/cost`
- **THEN** the grouping selector MUST default to **Total**, AND the page MUST request `/me/cost` with NO `group_by` and NO `from`/`to`, rendering the lifetime total `CostSummary` breakdown; the window preset control MUST be hidden or inert in this state

#### Scenario: Switching to a grouping reveals and applies the window
- **GIVEN** the dashboard is showing the ungrouped lifetime total
- **WHEN** the user selects **By day**
- **THEN** the window preset control MUST become active (default **30d**), AND the page MUST request `/me/cost?group_by=day` with `from`/`to` spanning the selected window, rendering one bar row per returned `key` in the server-provided ascending order

#### Scenario: By model renders the "other" bucket as a normal row
- **GIVEN** the caller has both LLM and non-LLM settled cost so `/me/cost?group_by=model` returns a real model `key` plus a `key = "other"` item
- **WHEN** the user selects **By model**
- **THEN** the page MUST render the `other` item as an ordinary labeled row in its server-provided position, not omit or specially hide it

#### Scenario: Empty result renders zero state, not 404
- **GIVEN** the caller has no settled cost
- **WHEN** the dashboard loads (Total)
- **THEN** it MUST render a zero/empty state with `amount_usd` shown as `"$0.0000"` (from the `"0.00000000"` string), and MUST NOT render a not-found or error screen

#### Scenario: Amounts are never parsed to float for display
- **GIVEN** a grouped item with `totals.amount_usd = "0.06750000"`
- **THEN** the displayed amount MUST be derived from that string by truncation (e.g. `"$0.0675"`), never from `parseFloat`, AND the bar width MAY use a bounded numeric fraction computed solely for geometry

#### Scenario: Server/transport error shows a single generic retry
- **WHEN** `/me/cost` fails with a 5xx or network error
- **THEN** the dashboard MUST render a generic error/retry message, MUST NOT crash the route, and MUST NOT also fire a transport/global toast (the in-page message is the sole error surface)
