## ADDED Requirements

### Requirement: Task Detail Cost Panel With Token Breakdown

The Task Detail page SHALL render a cost panel that shows the task's aggregate `CostSummary` breakdown — the amount plus the `input` / `output` / `cached` token counts, `tool_calls`, and `wall_time_ms` — sourced from `GET /api/v1/tasks/{task_id}/cost` (`data.total`). This is in addition to the existing inline `CostBadge`; the badge (driven by the already-loaded read DTO `cost`) MUST remain.

The amount displayed in the panel MUST follow the same decimal-string display rule as `CostBadge` (treat `amount_usd` as a string, truncate for display, keep the full value available), and MUST NOT parse the amount to a float. Token / tool / wall fields are JSON numbers and render as integers.

The panel MUST render loading and zero states gracefully: a task with no settled cost yet (the endpoint returns a zero-filled `CostSummary`, never 404 for an owned task) MUST show an all-zero breakdown, not an error. The cost query MUST skip retry on 404 and suppress its React Query cache toast (`meta.silent`), mirroring the existing task query. Because the page is already gated by the task query (which resolves the task before the panel mounts), a panel-only `404 task_not_found` implies a mid-flight ownership/race change and MUST be treated as a defensive no-op — the panel renders nothing/zero and MUST NOT raise a second not-found screen; the task-level not-found handling stays authoritative.

#### Scenario: Cost panel shows the token breakdown
- **GIVEN** a Task Detail page for an owned task whose `/tasks/{id}/cost` total has `amount_usd = "1.72000000"`, input/output/cached tokens, tool calls, and wall time
- **THEN** the page MUST render a cost panel showing the amount (e.g. `"$1.7200"`) alongside the input / output / cached token counts, tool calls, and wall time

#### Scenario: Zero-cost task renders an all-zero panel
- **GIVEN** an owned task whose `/tasks/{id}/cost` total is the zero-filled `CostSummary` (`amount_usd = "0.00000000"`)
- **THEN** the cost panel MUST render with all zeros and MUST NOT show an error state

#### Scenario: Panel amount is not parsed to float
- **GIVEN** a total with `amount_usd = "0.06750000"`
- **THEN** the displayed amount MUST be derived from the string by truncation (`"$0.0675"`), never via `parseFloat`

#### Scenario: Inline badge and panel coexist
- **WHEN** the Task Detail page renders
- **THEN** both the inline `CostBadge` (from the read DTO `cost`) and the cost panel (from `/tasks/{id}/cost`) MUST be present; they MAY momentarily differ during settle and MUST converge on the next refetch
