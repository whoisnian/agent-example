## ADDED Requirements

### Requirement: Semantic Title Generation

The runtime SHALL provide a `TitleGenerator` component that generates a semantic task title from the consumed message's `prompt` via a single LLM call and emits it as a `task.events` event with `kind="title"` and payload `{"title": "<generated title>"}` through the run's `EventPublisher` (normal per-run `seq`, idempotency key `<run_id>:<seq>`).

The generator MUST be invoked only when ALL of the following hold; otherwise no LLM call occurs and no `title` event is published:
- the `TaskExecuteMessage` carries `gen_title=true`;
- the run is fresh — `ctx.checkpoint_store.latest()` returns `None` (a redelivered or taken-over run that already wrote a checkpoint MUST NOT regenerate the title; correctness does not depend on this guard alone — a fresh redelivery that crashed before its first checkpoint regenerates once, and the ingest side applies last-write-wins, see `task-event-ingest`);
- `ctx.cancel_token` is not set (a cancelled run skips generation; this counts as a skip, not a failure);
- the `AgentRegistry` has an agent registered for the message's `task_type` (a DLX-bound `unimplemented` message MUST NOT burn an LLM call).

When invoked, the generator runs after the run is claimed and the `status=running` event is published, and before the agent is dispatched. The whole generation (LLM call + event publish) MUST be bounded by a 10s timeout. Title generation is best-effort: any failure (LLM error, timeout, publish failure, empty sanitized output) MUST be logged at WARN, MUST increment `worker_title_generation_failures_total`, and MUST NOT fail or delay the run beyond the timeout — the agent dispatch proceeds and the placeholder title persists.

The chat model MUST be obtained via `ModelFactory.get(<title model key>)` — resolved from the `WORKER_TITLE_MODEL_KEY` environment variable, falling back to the model key of the agent registered for the message's `task_type` when unset — and the call MUST attach `ctx.cost_meter` as a callback so the cost is emitted as a regular `cost.llm` event; the generator MUST NOT call `CostEventPublisher` directly and MUST NOT import a provider SDK directly.

The model input MUST be bounded to the first 2000 characters of `prompt`. The raw model output MUST pass through a pure sanitization function: take the first non-empty line, strip wrapping quotes, collapse internal whitespace, then truncate on a rune boundary so that the final string — including the `…` appended when truncation occurs — is at most 64 runes AND at most 200 bytes; when the sanitized result is empty, no `title` event is published.

#### Scenario: Flagged fresh run emits a title event
- **WHEN** a fresh run (no prior checkpoint) with `gen_title=true` is claimed and the LLM call returns a non-empty title
- **THEN** the worker MUST publish a `task.events` event with `kind="title"` and `payload.title` equal to the sanitized output before the agent is dispatched, AND a `cost.llm` event for the call MUST be emitted via the `CostMeter` callback

#### Scenario: Unflagged run skips generation entirely
- **WHEN** a run with `gen_title=false` (or absent) is claimed
- **THEN** the `TitleGenerator` MUST NOT be invoked, no extra LLM call occurs, and no `kind="title"` event is published

#### Scenario: Redelivered run with a checkpoint does not regenerate the title
- **GIVEN** a run with `gen_title=true` that already wrote at least one checkpoint (crash redelivery or stale-heartbeat takeover)
- **WHEN** the message is consumed again and `ctx.checkpoint_store.latest()` is not `None`
- **THEN** the `TitleGenerator` MUST NOT be invoked — no LLM call and no `kind="title"` event — and the run resumes from the checkpoint normally

#### Scenario: Cancelled run skips generation
- **WHEN** a run with `gen_title=true` is claimed but `ctx.cancel_token` is already set before generation starts
- **THEN** no LLM call occurs and no `kind="title"` event is published, and the skip is not counted as a generation failure

#### Scenario: Unregistered task_type does not burn an LLM call
- **WHEN** a message with `gen_title=true` carries a `task_type` with no registered agent
- **THEN** the `TitleGenerator` MUST NOT be invoked and the existing `unimplemented` → DLX path proceeds unchanged

#### Scenario: Generation failure does not affect the run
- **WHEN** a fresh run with `gen_title=true` is claimed and the LLM call raises or exceeds the 10s timeout
- **THEN** the worker MUST log at WARN and increment `worker_title_generation_failures_total`, AND the agent MUST still be dispatched so the run proceeds normally with no `kind="title"` event

#### Scenario: Oversized model output is sanitized and truncated
- **WHEN** the LLM returns a multi-line, quote-wrapped title exceeding 64 runes
- **THEN** the published `payload.title` MUST be the first non-empty line, unquoted, truncated on a rune boundary with a trailing `…` such that the final string including the `…` is within 64 runes and 200 bytes

#### Scenario: Empty sanitized output suppresses the event
- **WHEN** the LLM returns output that sanitizes to an empty string
- **THEN** no `kind="title"` event is published, the failure counter is incremented, and the run proceeds
