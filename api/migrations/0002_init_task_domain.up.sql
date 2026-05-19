-- 0002_init_task_domain: tasks, versions, runs, events, checkpoints, artifacts.
--
-- Capability: task-data-model.
-- Key invariants enforced by this migration:
--   * Task-level mutex via UNIQUE partial index `one_active_version_per_task`
--     over the generated stored column `task_versions.is_active`.
--   * Per-run idempotency keys on `task_runs.idempotency_key`, `task_events
--     (run_id, seq)`, `task_checkpoints (run_id, step_seq)`.
--   * Version tree integrity via `task_versions.parent_id`
--     REFERENCES task_versions(id).

BEGIN;

-- ---------------------------------------------------------------------------
-- tasks
-- ---------------------------------------------------------------------------
CREATE TABLE tasks (
    id              UUID PRIMARY KEY,
    -- tenant_id / user_id are FK-less for MVP (no tenants / users tables yet);
    -- auth + multi-tenant proposals add the FK constraints via ALTER.
    tenant_id       UUID NOT NULL,
    user_id         UUID NOT NULL,
    title           TEXT NOT NULL,
    task_type       TEXT NOT NULL,
    status          TEXT NOT NULL,
    current_version UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tasks_status_check CHECK (
        status IN ('pending', 'running', 'paused', 'cancelled', 'succeeded', 'failed')
    )
);
CREATE INDEX tasks_tenant_user_status_idx ON tasks (tenant_id, user_id, status);

-- ---------------------------------------------------------------------------
-- task_versions: version tree per task, with the task-level mutex constraint.
-- ---------------------------------------------------------------------------
CREATE TABLE task_versions (
    id            UUID PRIMARY KEY,
    task_id       UUID NOT NULL REFERENCES tasks(id),
    parent_id     UUID REFERENCES task_versions(id),
    version_no    INT  NOT NULL,
    prompt        TEXT NOT NULL,
    params        JSONB NOT NULL DEFAULT '{}'::jsonb,
    status        TEXT NOT NULL,
    -- Generated stored column. The expression is pure (status IN (...)) so it
    -- can sit under a UNIQUE index. STORED variant lets PostgreSQL include the
    -- column in indexes; VIRTUAL would not (PG 18 still requires STORED here).
    is_active     BOOLEAN GENERATED ALWAYS AS (
        status IN ('pending', 'queued', 'running', 'paused', 'cancelling')
    ) STORED,
    artifact_root TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_versions_status_check CHECK (
        status IN (
            'pending', 'queued', 'running', 'paused',
            'cancelling', 'cancelled', 'succeeded', 'failed'
        )
    ),
    CONSTRAINT task_versions_task_version_no_key UNIQUE (task_id, version_no)
);
CREATE INDEX task_versions_task_parent_idx ON task_versions (task_id, parent_id);

-- The load-bearing constraint: at most one active version per task.
-- PostgreSQL rejects concurrent inserts with SQLSTATE 23505 / constraint name
-- `one_active_version_per_task`. See task-data-model spec § "Task-Level Mutex".
CREATE UNIQUE INDEX one_active_version_per_task
    ON task_versions (task_id)
    WHERE is_active;

-- ---------------------------------------------------------------------------
-- task_runs: one execution attempt per version.
-- ---------------------------------------------------------------------------
CREATE TABLE task_runs (
    id              UUID PRIMARY KEY,
    version_id      UUID NOT NULL REFERENCES task_versions(id),
    attempt_no      INT  NOT NULL,
    worker_run_id   UUID,
    status          TEXT NOT NULL,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    last_heartbeat  TIMESTAMPTZ,
    error           JSONB,
    idempotency_key TEXT NOT NULL,
    CONSTRAINT task_runs_status_check CHECK (
        status IN (
            'queued', 'running', 'paused', 'cancelling',
            'cancelled', 'succeeded', 'failed'
        )
    ),
    CONSTRAINT task_runs_idempotency_key_key UNIQUE (idempotency_key),
    CONSTRAINT task_runs_version_attempt_key UNIQUE (version_id, attempt_no)
);
CREATE INDEX task_runs_status_heartbeat_idx ON task_runs (status, last_heartbeat);

-- ---------------------------------------------------------------------------
-- task_events: append-only event log. No FKs (hot path; see design D12).
-- ---------------------------------------------------------------------------
CREATE TABLE task_events (
    id         BIGSERIAL PRIMARY KEY,
    task_id    UUID NOT NULL,
    version_id UUID NOT NULL,
    run_id     UUID,
    seq        BIGINT NOT NULL,
    kind       TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX task_events_run_seq_key ON task_events (run_id, seq);
CREATE INDEX task_events_task_id_idx ON task_events (task_id, id);

-- ---------------------------------------------------------------------------
-- task_checkpoints: worker checkpoints, idempotent on (run_id, step_seq).
-- ---------------------------------------------------------------------------
CREATE TABLE task_checkpoints (
    id         UUID PRIMARY KEY,
    run_id     UUID NOT NULL REFERENCES task_runs(id),
    step_seq   INT  NOT NULL,
    step_name  TEXT NOT NULL,
    state      JSONB NOT NULL,
    oss_key    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT task_checkpoints_run_step_key UNIQUE (run_id, step_seq)
);

-- ---------------------------------------------------------------------------
-- artifacts: OSS object metadata per version.
-- ---------------------------------------------------------------------------
CREATE TABLE artifacts (
    id         UUID PRIMARY KEY,
    version_id UUID NOT NULL REFERENCES task_versions(id),
    kind       TEXT NOT NULL,
    oss_key    TEXT NOT NULL,
    mime       TEXT,
    bytes      BIGINT,
    sha256     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX artifacts_version_idx ON artifacts (version_id);

COMMIT;
