## MODIFIED Requirements

### Requirement: Task Execute Consumer

The Worker SHALL consume from `q.task.execute.<lane>` (default lane: `default`) on `task.exchange` with routing key `execute.<task_type>.<lane>`. The consumer MUST set `prefetch_count=1` (one in-flight message per worker channel) and MUST use manual ack mode.

The consumer SHALL parse each delivery into a typed `TaskExecuteMessage` matching the envelope defined in `docs/ARCHITECTURE.md §5.3`: `{msg_id, idempotency_key, task_id, version_id, run_id, attempt_no, task_type, prompt, params, parent_version_id, parent_artifact_root, deadline_ts, gen_title}`. The `gen_title` field is OPTIONAL and MUST default to `false` when absent; the parser MUST tolerate unknown extra fields (a message containing fields the worker does not recognise MUST NOT be treated as poison). Failure to parse MUST result in `nack(requeue=false)` (poison message → DLX) plus a `worker_invalid_message_total` increment.

#### Scenario: Prefetch limits concurrency to one
- **WHEN** the worker is processing a task message
- **THEN** the broker MUST NOT deliver a second message to the same channel until the first is ack'd or nack'd

#### Scenario: Poison message routed to DLX
- **WHEN** a delivery body cannot be parsed as `TaskExecuteMessage`
- **THEN** the consumer MUST `nack(requeue=false)` so the broker routes it to `task.dlx`, AND `worker_invalid_message_total` MUST be incremented

#### Scenario: Message without gen_title parses with the default
- **WHEN** a delivery body matches the execute envelope but omits `gen_title`
- **THEN** parsing MUST succeed AND the resulting `TaskExecuteMessage.gen_title` MUST be `false`

#### Scenario: Unknown extra field is not poison
- **WHEN** a delivery body matches the execute envelope and additionally carries a field the worker does not recognise
- **THEN** parsing MUST succeed AND the message MUST be processed normally
