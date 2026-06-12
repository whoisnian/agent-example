# task-conversation-history Specification (Delta)

## ADDED Requirements

### Requirement: History Derived from the Version Parent Chain

A task's conversation history SHALL be derived exclusively from the `task_versions` parent chain: starting at the resolved base version and following `parent_id` to the root, each version contributes one turn `(version_no, prompt, summary, status)`. The assembled history MUST be ordered oldest→newest. No separate conversation store SHALL be introduced; the chain is the single source of truth so that rollback-`branch` (which parents on a historical version) automatically yields the correct branch-local history.

Each turn's `status` MUST be the version's terminal status at assembly time (e.g. `succeeded`, `failed`, `cancelled`), so consumers can distinguish a failed prior attempt from a successful one. A version whose `summary` is NULL (failed run, pre-migration row, or a summary event not yet consumed) MUST still contribute a turn with `summary = null`; history assembly MUST NOT block on or wait for summary availability.

#### Scenario: History follows the parent chain of the base version
- **GIVEN** a task with versions v1←v2←v3 (v3 parented on v2, v2 on v1)
- **WHEN** history is assembled for an iterate based on v3
- **THEN** the history MUST be `[v1, v2, v3]` in that order, each turn carrying that version's `prompt`, `summary`, and `status`

#### Scenario: Rollback-branch history excludes abandoned branches
- **GIVEN** a task with chain v1←v2←v3
- **WHEN** history is assembled for a rollback-`branch` targeting v2
- **THEN** the history MUST be `[v1, v2]` and MUST NOT include v3

#### Scenario: Missing summary degrades to a prompt-only turn
- **GIVEN** a chain containing a version whose `summary` is NULL
- **WHEN** history is assembled
- **THEN** that version's turn MUST appear with its `prompt`, its `status`, and `summary = null`, and assembly MUST succeed

#### Scenario: Failed versions are marked in the history
- **GIVEN** a chain v1 (succeeded) ← v2 (failed) and an iterate based on v2
- **WHEN** history is assembled
- **THEN** the v2 turn MUST carry `status = "failed"`, so the consumer can render it as a failed prior attempt rather than a successful result

### Requirement: Bounded History Assembly

History assembly SHALL enforce hard bounds, applied in this order: (a) the parent-chain walk reads at most 20 versions, counted from the base version backwards (nearest-first) — this is a DB traversal depth bound, not a guaranteed turn count; (b) each turn's `prompt` and `summary` truncated on a rune boundary to at most 1024 bytes each (with `…` appended when truncation occurs); (c) if the serialized `history` array exceeds 16 KiB, whole turns MUST be dropped from the oldest end until it fits — so a chain of maximally-sized turns retains roughly the 7 most recent. The bound constants MUST be defined in one place in the API codebase.

#### Scenario: Deep chains keep the most recent turns
- **GIVEN** a parent chain of 25 versions whose prompts and summaries are short enough that 20 turns serialize under 16 KiB
- **WHEN** history is assembled
- **THEN** the history MUST contain exactly the 20 most recent turns, oldest of the retained turns first

#### Scenario: Oversized serialization drops oldest turns whole
- **GIVEN** a chain whose per-turn-truncated history serializes above 16 KiB
- **WHEN** history is assembled
- **THEN** turns MUST be removed from the oldest end (never the newest) until the serialized size is at most 16 KiB

### Requirement: History Field in the Execute Payload

The execute message contract (`docs/ARCHITECTURE.md §5.3`) SHALL gain an OPTIONAL `history` field: a JSON array of `{version_no, prompt, summary, status}` objects (with `summary` nullable), ordered oldest→newest. Producers that derive a new version from a base (iterate, rollback-`branch`) MUST populate it per the assembly rules above. The create path (no prior version) MUST omit the field. An absent `history` MUST be interpreted by consumers as an empty list.

`history` is assembled once, when the version's execute outbox row is written; any later republish of an execute message for the same version (retry, redelivery, or a future API resume/republish path such as `docs/ARCHITECTURE.md §6.3`) MUST carry the originally assembled `history` unchanged rather than reassembling or dropping it.

#### Scenario: Iterate payload carries history
- **WHEN** an iterate succeeds against a base version with a non-empty parent chain
- **THEN** the resulting execute outbox `payload` MUST contain a `history` array conforming to the turn shape and bounds above

#### Scenario: Create payload omits history
- **WHEN** a task is created via `POST /api/v1/tasks`
- **THEN** the resulting execute outbox `payload` MUST NOT contain a `history` field

#### Scenario: Republished execute keeps its original history
- **GIVEN** a version whose execute outbox payload was written with a `history` array
- **WHEN** the execute message for that same version is published again by any producer
- **THEN** the message MUST carry the same `history` content as the original payload
