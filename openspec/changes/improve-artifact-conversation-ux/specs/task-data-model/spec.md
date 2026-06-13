## MODIFIED Requirements

### Requirement: Artifacts Table

The schema SHALL define an `artifacts` table for OSS object metadata. Each row MUST carry: `id UUID PRIMARY KEY`, `version_id UUID NOT NULL REFERENCES task_versions(id)`, `kind TEXT NOT NULL` (e.g., `code-bundle`, `report`, `image`, `log`), `oss_key TEXT NOT NULL`, `path TEXT` (the artifact's version-relative file path, e.g. `index.html` / `css/style.css` — what the producing tool was asked to write; nullable for legacy rows only, new writes MUST set it), `mime TEXT`, `bytes BIGINT`, `sha256 TEXT`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

The table MUST enforce **at most one row per `(version_id, oss_key)`** via a UNIQUE constraint, so artifact recording is idempotent under at-least-once message delivery and under overwrite (a re-recorded or overwritten object updates the single row rather than appending a duplicate). Writers SHALL upsert on this key, and the upsert MUST update `path` along with the other metadata columns.

The migration introducing `path` MUST backfill existing rows by stripping the deterministic per-version prefix `{tenant_id}/{task_id}/{version_id}/` from `oss_key` (the layout `compute_oss_prefix` guarantees, resolvable per row via the `task_versions → tasks` join). A row whose `oss_key` does not match its expected prefix, OR whose stripped remainder is empty, MUST be left with `path = NULL` rather than failing the migration; the down migration drops the column (and the index below).

The migration MUST also add a partial UNIQUE index on `(version_id, path) WHERE path IS NOT NULL`. This both backs the preview route's `(version_id, path)` lookup and makes "at most one artifact per relative path per version" a hard guarantee (today it is only a soft consequence of `oss_key = prefix + path` plus the `(version_id, oss_key)` UNIQUE). It cannot conflict with backfilled data: distinct `oss_key`s under one version map to distinct non-empty paths, and multiple `NULL` paths are permitted by the partial predicate.

#### Scenario: Artifact rows are addressable by version
- **WHEN** a client queries `SELECT * FROM artifacts WHERE version_id = $1`
- **THEN** PostgreSQL MUST return all rows whose `version_id` matches, with no FK orphans (orphan rejection enforced by `REFERENCES task_versions(id)`)

#### Scenario: One row per object per version
- **WHEN** a writer records the same `(version_id, oss_key)` twice (e.g. a redelivered run re-inheriting a parent artifact, or an overwrite of a produced file)
- **THEN** the table MUST hold exactly one row for that pair (the second write upserts, refreshing `path`/`mime`/`bytes`/`sha256`), never two

#### Scenario: Migration backfills path from the deterministic oss_key layout
- **GIVEN** a pre-existing artifact row with `oss_key = "{tenant_id}/{task_id}/{version_id}/css/style.css"` for its owning version
- **WHEN** the `path` migration runs
- **THEN** that row's `path` MUST become `css/style.css`, and a row whose `oss_key` does not start with its version's expected prefix (or strips to an empty remainder) MUST end with `path = NULL` (the migration completes without error)

#### Scenario: At most one row per (version_id, path) for non-null paths
- **WHEN** a writer attempts to record two rows with the same non-null `path` under one `version_id`
- **THEN** the partial UNIQUE index on `(version_id, path) WHERE path IS NOT NULL` MUST reject the second, while multiple rows with `path = NULL` under one version remain permitted
