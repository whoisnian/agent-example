# task-write-api Specification

## Purpose
TBD - created by archiving change add-task-create-api. Update Purpose after archive.
## Requirements
### Requirement: Create Task Endpoint

The API SHALL expose `POST /api/v1/tasks` that, in a single PostgreSQL transaction, inserts one row into `tasks` (status `pending`, `current_version` pointing at the new version), one row into `task_versions` (parent_id `NULL`, version_no `1`, status `pending`), one row into `task_runs` (attempt_no `1`, status `queued`, `idempotency_key` equal to the textual run id), and one row into `outbox` whose `topic` is `execute.<task_type>.<lane>` and whose `payload` JSONB matches the execute message contract in `docs/ARCHITECTURE.md §5.3`. The endpoint MUST return HTTP `201` with the unified envelope and `data = {task_id, version_id, version_no, status}`.

The `title` field is OPTIONAL. When `title` is absent or trims to empty, the service MUST derive a **placeholder** title deterministically from `prompt`: the first non-empty line of the trimmed prompt, truncated on a rune boundary to at most 64 runes AND at most 200 bytes (an ellipsis `…` is appended when truncation occurs); when the derivation yields an empty string (all-whitespace prompt), the title MUST be the literal `Untitled task`. When a non-empty `title` is supplied, the existing validation (trimmed, 1..200) applies unchanged. Title derivation MUST NOT involve an LLM call.

When the title was derived (not explicitly supplied), the execute outbox `payload` MUST include `"gen_title": true`, signalling the worker to generate a semantic title asynchronously (see `worker-execution-runtime`). When a non-empty `title` is explicitly supplied, the payload MUST NOT set `gen_title` to true. `gen_title` is a create-only flag with whitelist semantics: an execute payload that omits the field means `false` (`docs/ARCHITECTURE.md §5.3`), so this derived-title create path is the only producer that sets it — other execute producers (iterate, rollback, and any future API republish such as the §6.3 resume path) simply never set the field and need no contract change.

Server-minted ids (task, version, run, outbox payload `msg_id`) MUST be UUIDv7. `task_runs.idempotency_key` MUST equal the textual form of `task_runs.id`. The `lane` field MUST come from the request body when present (matching the slug pattern `^[a-z0-9-]{1,32}$`); otherwise the service MUST fall back to the `DEFAULT_LANE` environment variable (default literal `default`).

#### Scenario: Happy path persists task, version, run, and outbox atomically
- **WHEN** a client `POST`s `/api/v1/tasks` with a valid body (`task_type`, `prompt`, optional `title`)
- **THEN** the response MUST be HTTP `201` with envelope `{code:0, message:"ok", data:{task_id, version_id, version_no:1, status:"pending"}, trace_id}` AND exactly one row MUST exist in each of `tasks`, `task_versions`, `task_runs`, `outbox` referencing that task_id, with `task_versions.is_active=true`, `task_runs.status='queued'`, and `outbox.status='pending'`

#### Scenario: Absent title is derived from the prompt
- **WHEN** a client `POST`s `/api/v1/tasks` with a valid `task_type` and `prompt` whose first non-empty line is `build a music app` and no `title` field
- **THEN** the response MUST be HTTP `201` AND the persisted `tasks.title` MUST equal `build a music app`

#### Scenario: Derived title marks the execute payload for semantic generation
- **WHEN** a client `POST`s `/api/v1/tasks` omitting `title` (or with a `title` that trims to empty)
- **THEN** the resulting `outbox.payload->>'gen_title'` MUST equal `true`

#### Scenario: Explicit title suppresses semantic generation
- **WHEN** a client `POST`s `/api/v1/tasks` with a valid non-empty `title`
- **THEN** the persisted `tasks.title` MUST equal the trimmed supplied title AND the resulting execute `outbox.payload` MUST NOT contain `gen_title: true`

#### Scenario: Derived title is truncated on a rune boundary
- **WHEN** the request omits `title` and the prompt's first non-empty line exceeds 64 runes or 200 bytes
- **THEN** the persisted `tasks.title` MUST be a prefix of that line cut on a rune boundary within both limits, suffixed with `…`, AND MUST NOT exceed 200 bytes

#### Scenario: All-whitespace prompt falls back to the literal title
- **WHEN** the request omits `title` and supplies a `prompt` consisting only of whitespace
- **THEN** the response MUST be HTTP `201` AND the persisted `tasks.title` MUST equal `Untitled task`

#### Scenario: Outbox topic encodes routing key
- **WHEN** the request supplies `task_type="code-gen"` and `lane="default"`
- **THEN** the resulting `outbox.topic` MUST equal `execute.code-gen.default` AND `outbox.payload->>'task_type'` MUST equal `code-gen` AND `outbox.payload->>'lane'` MUST equal `default`

#### Scenario: Lane defaults from environment
- **WHEN** the request omits `lane` and the service runs with `DEFAULT_LANE=default`
- **THEN** the resulting `outbox.topic` MUST end in `.default` AND the JSON envelope returned to the client MUST still report `status:"pending"`

### Requirement: Iterate Task Endpoint

The API SHALL expose `POST /api/v1/tasks/{task_id}/iterate` that derives a new version from a base version, in a single PostgreSQL transaction. The transaction MUST: (a) `SELECT … FOR UPDATE` the `tasks` row; (b) resolve the base version (request `base_version_id` if present, else `tasks.current_version`); (c) insert one row into `task_versions` (parent_id = base, version_no = MAX(version_no)+1 within the task, status `pending`); (d) insert one row into `task_runs`; (e) insert one row into `outbox` with the same payload contract as create plus `parent_version_id` and `parent_artifact_root` filled from the base row, plus a `history` array assembled from the base version's parent chain per the `task-conversation-history` capability (oldest→newest, bounded); (f) `UPDATE tasks SET status='pending', current_version=$new, updated_at=now()`. The endpoint MUST return HTTP `201` with envelope `data = {version_id, version_no, status}`.

#### Scenario: Happy iterate derives a child version
- **WHEN** a client `POST`s `/api/v1/tasks/{task_id}/iterate` with a valid `prompt` while the task has no active version and `current_version` points at a terminal version
- **THEN** the response MUST be HTTP `201` AND a new `task_versions` row MUST exist whose `parent_id` equals the resolved base, whose `version_no` is the previous max plus one, and whose `is_active` is true AND `tasks.current_version` MUST now equal the new version id

#### Scenario: Explicit base_version_id is honored
- **WHEN** the request body supplies `base_version_id` that belongs to the path `task_id` and is a terminal version
- **THEN** the new `task_versions.parent_id` MUST equal the supplied `base_version_id` (not `tasks.current_version`)

#### Scenario: Outbox payload carries parent context
- **WHEN** an iterate succeeds against base version `vB` that has `artifact_root='oss://bucket/tenant/task/vB/'`
- **THEN** `outbox.payload->>'parent_version_id'` MUST equal `vB.id::text` AND `outbox.payload->>'parent_artifact_root'` MUST equal `oss://bucket/tenant/task/vB/`

#### Scenario: Outbox payload carries conversation history
- **GIVEN** a base version `vB` whose parent chain is v1←v2 (vB = v2) with `summary` set on v1 and v2
- **WHEN** an iterate succeeds against `vB`
- **THEN** `outbox.payload->'history'` MUST be a JSON array of exactly two turns ordered `[v1, v2]`, each carrying `version_no`, `prompt`, `summary`, and `status` per the `task-conversation-history` bounds

#### Scenario: Iterate from a v1-only task carries a single-turn history
- **GIVEN** a task whose only version is v1 (terminal)
- **WHEN** an iterate succeeds with `tasks.current_version` as the implicit base
- **THEN** `outbox.payload->'history'` MUST be a one-element array whose turn carries v1's `version_no`, `prompt`, `status`, and `summary` (null when v1 has no summary)

### Requirement: Task-Level Mutex Translated to 409

When the application detects an active version for the target task (either by reading `tasks.status` and the active version row before insert, or by catching SQLSTATE `23505` with constraint name `one_active_version_per_task` after insert), the request MUST be rejected with HTTP `409 Conflict` and envelope `{code:"active_version_exists", message:<human readable>, data:{active_version_id, active_version_status}, trace_id}`. The transaction MUST roll back so no partial state is written.

The DB unique partial index `one_active_version_per_task` is the source of truth; the application-level pre-check is a friendliness path that MUST NOT replace catching the SQLSTATE error.

#### Scenario: Iterate during running version returns 409
- **WHEN** a client `POST`s `/iterate` on a task whose current version has status `running`
- **THEN** the response MUST be HTTP `409` with envelope `code="active_version_exists"`, `data.active_version_id` equal to the running version id, `data.active_version_status="running"`, AND no new rows MUST exist in `task_versions` / `task_runs` / `outbox`

#### Scenario: Concurrent iterates: at most one succeeds
- **WHEN** two clients concurrently `POST /iterate` against the same task with the prior version in a terminal state
- **THEN** exactly one MUST receive HTTP `201` AND the other MUST receive HTTP `409 active_version_exists` AND `task_versions` MUST contain exactly one new active row for that task

#### Scenario: Create on a task that does not yet exist cannot conflict
- **WHEN** a fresh `POST /api/v1/tasks` is processed against a database where no `tasks` row shares the new id
- **THEN** the unique partial index MUST NOT raise; the response MUST be HTTP `201`

### Requirement: 404 and 400 Outcomes

The API MUST return HTTP `404 task_not_found` when `POST /iterate` references a `task_id` that has no row. The API MUST return HTTP `404 version_not_found` when `POST /iterate` supplies a `base_version_id` that does not belong to the path `task_id`, OR when `base_version_id` is absent and `tasks.current_version` is `NULL`. The API MUST return HTTP `400 invalid_input` when the request body fails validation (missing required field, type mismatch, length / pattern violation, or `params` JSON exceeding 32 KiB serialized). An absent or empty `title` on `POST /api/v1/tasks` is NOT a validation failure (the title is derived, see "Create Task Endpoint"); an explicitly supplied `title` longer than 200 characters remains one.

#### Scenario: Unknown task
- **WHEN** a client iterates against `task_id` that has no row
- **THEN** the response MUST be HTTP `404` with envelope `code="task_not_found"`

#### Scenario: Base version belongs to a different task
- **WHEN** the request supplies `base_version_id` that exists but whose `task_id` does not match the path
- **THEN** the response MUST be HTTP `404 version_not_found` AND no rows MUST be inserted

#### Scenario: Missing required field
- **WHEN** `POST /api/v1/tasks` is called with empty `prompt`
- **THEN** the response MUST be HTTP `400` with envelope `code="invalid_input"` AND `message` MUST name the offending field

#### Scenario: Oversized explicit title still rejected
- **WHEN** `POST /api/v1/tasks` is called with an explicit `title` whose trimmed length exceeds 200 characters
- **THEN** the response MUST be HTTP `400` with envelope `code="invalid_input"` AND `message` MUST name `title`

### Requirement: Transactional Outbox Insertion

The outbox row MUST be written in the same database transaction as the `task_versions` row whose execution it triggers. Direct AMQP publishing in the request path is FORBIDDEN; delivery is the Outbox Relayer's responsibility (`api-messaging`). Therefore, a request that returns `201` MUST have left exactly one matching `outbox` row with `status='pending'` and the routing key embedded in `outbox.topic`.

#### Scenario: Outbox row appears with the response
- **WHEN** a client receives `201` from `POST /api/v1/tasks`
- **THEN** an `outbox` row whose `aggregate='task_version'` AND `aggregate_id=<returned version_id>` AND `status='pending'` MUST be visible to a fresh `SELECT` issued by an observer

#### Scenario: Conflict leaves no outbox row
- **WHEN** an iterate request is rejected with `409 active_version_exists`
- **THEN** no `outbox` row whose `aggregate_id` matches the rejected version attempt MUST exist (the transaction must have rolled back)

### Requirement: Observability for Write Endpoints

Each write endpoint MUST emit a Prometheus counter and structured log line on every terminal outcome. Counters: `tasks_created_total{task_type}` for `POST /tasks` success; `tasks_iterated_total{outcome="success"|"conflict"|"not_found"|"invalid"}` for `POST /iterate` regardless of HTTP status. Every log line emitted within the handler scope MUST carry `trace_id`, `request_id`, `task_id`, and (when known) `version_id`.

#### Scenario: Success counter
- **WHEN** a successful create of `task_type="research"` returns `201`
- **THEN** `tasks_created_total{task_type="research"}` MUST be incremented by 1

#### Scenario: Conflict counter
- **WHEN** an iterate returns `409 active_version_exists`
- **THEN** `tasks_iterated_total{outcome="conflict"}` MUST be incremented by 1 AND the log line MUST include the `active_version_id` from the response envelope

#### Scenario: Trace propagation
- **WHEN** a request arrives with a valid `traceparent` header
- **THEN** the handler span MUST be a child of that trace context AND the `trace_id` log field MUST equal the inbound trace id
