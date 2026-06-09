## MODIFIED Requirements

### Requirement: Artifacts Table

The schema SHALL define an `artifacts` table for OSS object metadata. Each row MUST carry: `id UUID PRIMARY KEY`, `version_id UUID NOT NULL REFERENCES task_versions(id)`, `kind TEXT NOT NULL` (e.g., `code-bundle`, `report`, `image`, `log`), `oss_key TEXT NOT NULL`, `mime TEXT`, `bytes BIGINT`, `sha256 TEXT`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

The table MUST enforce **at most one row per `(version_id, oss_key)`** via a UNIQUE constraint, so artifact recording is idempotent under at-least-once message delivery and under overwrite (a re-recorded or overwritten object updates the single row rather than appending a duplicate). Writers SHALL upsert on this key.

#### Scenario: Artifact rows are addressable by version
- **WHEN** a client queries `SELECT * FROM artifacts WHERE version_id = $1`
- **THEN** PostgreSQL MUST return all rows whose `version_id` matches, with no FK orphans (orphan rejection enforced by `REFERENCES task_versions(id)`)

#### Scenario: One row per object per version
- **WHEN** a writer records the same `(version_id, oss_key)` twice (e.g. a redelivered run re-inheriting a parent artifact, or an overwrite of a produced file)
- **THEN** the table MUST hold exactly one row for that pair (the second write upserts), never two
