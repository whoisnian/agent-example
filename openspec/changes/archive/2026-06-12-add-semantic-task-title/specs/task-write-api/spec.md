## MODIFIED Requirements

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
