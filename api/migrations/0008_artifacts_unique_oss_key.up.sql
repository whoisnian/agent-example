-- 0008_artifacts_unique_oss_key: one artifact row per (version_id, oss_key).
--
-- Capability: task-data-model (MODIFIED by add-worker-rollback-handling).
-- Makes artifact recording idempotent under at-least-once delivery and under
-- overwrite: the worker upserts on this key (ON CONFLICT (version_id, oss_key)),
-- so re-inheriting a parent's artifacts after a redelivery, or the agent
-- overwriting a produced file, collapses to a single row instead of appending
-- a duplicate.
--
-- PRECONDITION: fails if the artifacts table already holds duplicate
-- (version_id, oss_key) rows. Fresh MVP/dev databases have none; an existing
-- environment with duplicates must de-dup before applying.

BEGIN;

CREATE UNIQUE INDEX artifacts_version_oss_key_key ON artifacts (version_id, oss_key);

COMMIT;
