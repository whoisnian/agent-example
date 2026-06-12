# worker-messaging Specification (Delta)

## MODIFIED Requirements

### Requirement: Task Execute Consumer

The Worker SHALL consume from `q.task.execute.<lane>` (default lane: `default`) on `task.exchange` with routing key `execute.<task_type>.<lane>`. The consumer MUST set `prefetch_count=1` (one in-flight message per worker channel) and MUST use manual ack mode.

The consumer SHALL parse each delivery into a typed `TaskExecuteMessage` matching the envelope defined in `docs/ARCHITECTURE.md §5.3`: `{msg_id, idempotency_key, task_id, version_id, run_id, attempt_no, task_type, prompt, params, parent_version_id, parent_artifact_root, deadline_ts, gen_title, history}`. The `gen_title` field is OPTIONAL and MUST default to `false` when absent. The `history` field is OPTIONAL and MUST default to an empty list when absent; when present it MUST parse as a list of turns `{version_no: int, prompt: str, summary: str | null, status: str}` (see `task-conversation-history`). A structurally invalid `history` MUST NOT poison the message: the consumer MUST degrade it to an empty list, log a warning naming the parse failure, and increment `worker_invalid_history_total` — the run then executes without conversation context rather than dead-lettering every iterate on a producer bug. The parser MUST tolerate unknown extra fields (a message containing fields the worker does not recognise MUST NOT be treated as poison). Failure to parse the envelope itself MUST result in `nack(requeue=false)` (poison message → DLX) plus a `worker_invalid_message_total` increment.

#### Scenario: Prefetch limits concurrency to one
- **WHEN** the worker is processing a task message
- **THEN** the broker MUST NOT deliver a second message to the same channel until the first is ack'd or nack'd

#### Scenario: Poison message routed to DLX
- **WHEN** a delivery body cannot be parsed as `TaskExecuteMessage`
- **THEN** the consumer MUST `nack(requeue=false)` so the broker routes it to `task.dlx`, AND `worker_invalid_message_total` MUST be incremented

#### Scenario: Message without gen_title parses with the default
- **WHEN** a delivery body matches the execute envelope but omits `gen_title`
- **THEN** parsing MUST succeed AND the resulting `TaskExecuteMessage.gen_title` MUST be `false`

#### Scenario: Message without history parses with an empty list
- **WHEN** a delivery body matches the execute envelope but omits `history`
- **THEN** parsing MUST succeed AND the resulting `TaskExecuteMessage.history` MUST be an empty list

#### Scenario: History turns parse with nullable summaries
- **WHEN** a delivery carries `history: [{"version_no": 1, "prompt": "build a music app", "summary": null, "status": "failed"}]`
- **THEN** parsing MUST succeed AND the message MUST expose one history turn whose `summary` is `None` and whose `status` is `"failed"`

#### Scenario: Structurally invalid history degrades instead of poisoning
- **WHEN** a delivery matches the execute envelope but its `history` is not a valid turn list (e.g. a turn missing `prompt`)
- **THEN** parsing MUST succeed with `history` degraded to an empty list, a warning MUST be logged, `worker_invalid_history_total` MUST be incremented, AND the message MUST NOT be nack'd to the DLX for this reason

#### Scenario: Unknown extra field is not poison
- **WHEN** a delivery body matches the execute envelope and additionally carries a field the worker does not recognise
- **THEN** parsing MUST succeed AND the message MUST be processed normally
