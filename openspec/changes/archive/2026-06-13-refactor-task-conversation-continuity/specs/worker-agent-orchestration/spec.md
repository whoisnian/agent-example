# worker-agent-orchestration Specification (Delta)

## ADDED Requirements

### Requirement: Conversation Context Injection

When the consumed message carries a non-empty `history` and/or the run inherited parent artifacts, the loop SHALL assemble a conversation-context block and prepend it to the planner input and to the executor's per-step input. The block MUST contain, in order:

1. **Conversation history**: every history turn rendered oldest→newest as the user's prompt for that version followed by its result summary (an explicit "no summary" marker when the turn's `summary` is null); a turn whose `status` is not `succeeded` MUST be rendered with an explicit failure/cancellation marker so the model does not treat it as a successful prior result;
2. **Inherited artifact inventory** (only when inheritance copied at least one object): the path and byte size of every inherited artifact, and for small text artifacts (single object ≤ 8 KiB, identified by MIME/extension) the file content, subject to a total content budget of 24 KiB. Excerpt selection MUST be deterministic: eligible files are included in ascending byte-size order, ties broken by lexicographic path order, until the budget is exhausted; artifacts beyond the budget appear in the inventory without content. Inventory data MUST be obtained from the keys `inherit_parent_artifacts` actually copied (and their content via `ctx.oss_client`); the agent MUST NOT query business tables for it and MUST NOT derive the inventory from a fresh listing of its own run prefix (which would misattribute this run's own outputs as inherited).

The context block MUST be assembled exactly once per run, on the fresh path after inheritance completes and before planning, and persisted as part of the `step_seq=0` (plan) checkpoint state. A resumed run (`attempt_no > 1` or any redelivery with a prior checkpoint) MUST restore the block from the checkpoint instead of re-assembling it, so every attempt of a run injects byte-identical context.

The critic input MUST remain unchanged (it reviews step results only). When `history` is empty AND no artifacts were inherited, the context block MUST be omitted entirely and the role inputs MUST be byte-identical to the pre-change behavior, so create runs and legacy messages are unaffected. The context block MUST be assembled deterministically from the message and the inherited objects (no LLM call), preserving fake-model test determinism.

#### Scenario: Planner sees the conversation history
- **GIVEN** an execute message whose `history` contains turns for v1 and v2
- **WHEN** the loop invokes the planner
- **THEN** the planner's input MUST contain both turns' prompts and summaries (oldest first) followed by the current request's prompt

#### Scenario: Failed prior turns are marked
- **GIVEN** an execute message whose `history` contains a turn with `status = "failed"`
- **WHEN** the context block is rendered
- **THEN** that turn MUST carry an explicit failure marker distinguishing it from succeeded turns

#### Scenario: Executor sees inherited artifacts
- **GIVEN** a run that inherited `index.html` (2 KiB, text) and `logo.png` (300 KiB) from its parent
- **WHEN** the loop invokes the executor for a step
- **THEN** the executor's input MUST list both paths with sizes AND include the content of `index.html`, while `logo.png` appears without content

#### Scenario: Content budget caps excerpt volume deterministically
- **GIVEN** inherited text artifacts whose combined size exceeds the 24 KiB content budget
- **WHEN** the context block is assembled
- **THEN** file contents MUST be included in ascending size order (ties by path) only up to the budget and every remaining artifact MUST still appear in the inventory by path and size

#### Scenario: Resume restores the context block from the checkpoint
- **GIVEN** a run whose first attempt assembled a context block and wrote the plan checkpoint, then crashed
- **WHEN** the message is redelivered and the run resumes from the checkpoint
- **THEN** the executor inputs MUST use the checkpointed context block verbatim, AND the worker MUST NOT re-list OSS or re-read artifact contents to rebuild it

#### Scenario: Empty history and no inheritance leaves inputs unchanged
- **GIVEN** an execute message with no `history` and no `parent_version_id`
- **WHEN** the loop invokes the planner and executor
- **THEN** their inputs MUST be byte-identical to the pre-change composition (no context block, no extra markers)

### Requirement: Run Summary Event

On successful completion — after artifact upload, before returning — the agent SHALL emit exactly one `task.events` event with `kind="summary"` whose `payload.summary` is a deterministic concatenation of the run's per-step executor summaries (one line per step, prefixed by the step sequence), truncated on a rune boundary to at most 2048 bytes. The summary MUST be produced without an additional LLM call.

To make the concatenation resume-safe, each completed step's checkpoint state SHALL record that step's executor summary (capped at the existing 500-character step-summary cap) in its plan-state entry, so a resumed run reassembles the full set of step summaries — including steps executed by earlier attempts — from the restored checkpoint rather than only from in-memory state.

A failed or cancelled run MUST NOT emit a summary event. When every step summary is empty, the event MUST still be emitted with an empty `payload.summary` (the ingest side skips the column update but persists the event row).

#### Scenario: Successful run emits one summary event
- **WHEN** a 3-step run completes successfully with non-empty step summaries
- **THEN** exactly one `kind="summary"` event MUST be emitted whose `payload.summary` contains the three step summaries in step order, AND it MUST be emitted after artifact rows are written

#### Scenario: Resumed run summarizes all steps, not just its own
- **GIVEN** a run whose attempt 1 completed steps 1–2 (checkpointing each step's summary) and crashed, and whose attempt 2 resumed and completed steps 3–4
- **WHEN** attempt 2 finishes successfully
- **THEN** the emitted `payload.summary` MUST contain the summaries of steps 1–4 in step order

#### Scenario: Failed run emits no summary
- **WHEN** a run fails (error event path) or is cancelled
- **THEN** no `kind="summary"` event MUST be emitted for that run

#### Scenario: Oversized summary is truncated
- **WHEN** the concatenated step summaries exceed 2048 bytes
- **THEN** the emitted `payload.summary` MUST be truncated on a rune boundary to at most 2048 bytes with a trailing `…`

### Requirement: Resume-Safe Event Sequencing

Each checkpoint's state SHALL record the run's event-sequence high-water mark (the last `task.events` seq emitted) at the moment the checkpoint is written. On resume, the loop MUST initialize the run's event-sequence counter from the restored checkpoint before emitting any event, so events emitted by a later attempt continue the sequence instead of restarting at 1 — without this, the ingest side's `(run_id, seq)` idempotency silently drops every post-resume event (including the summary event), and a same-process publisher rejects the non-increasing seq as a programming error.

#### Scenario: Post-resume events continue the sequence
- **GIVEN** a run whose attempt 1 emitted events up to `seq=5` and checkpointed, then crashed
- **WHEN** attempt 2 resumes from that checkpoint and emits its next event
- **THEN** that event's `seq` MUST be greater than 5, AND the ingest side MUST persist it as a new `task_events` row rather than dropping it as a duplicate

#### Scenario: Fresh runs are unaffected
- **WHEN** a run starts with no prior checkpoint
- **THEN** event sequencing MUST start from 1 exactly as before this change
