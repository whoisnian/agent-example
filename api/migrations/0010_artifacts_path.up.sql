-- 0010_artifacts_path (up)
--
-- Capability: task-data-model (MODIFIED by improve-artifact-conversation-ux).
-- Adds the artifact's version-relative file path (e.g. index.html,
-- css/style.css) so the API can label artifacts, stream a version's files as a
-- zip with real names, and resolve relative asset references in the
-- directory-aware HTML preview route.
--
-- Backfill is exact: oss_key is deterministically `{tenant_id}/{task_id}/
-- {version_id}/{path}` (worker compute_oss_prefix), resolvable per row via the
-- task_versions → tasks join. A row whose oss_key does not match its expected
-- prefix, or whose stripped remainder is empty, is left path = NULL rather than
-- failing the migration. The partial UNIQUE index both backs the preview
-- route's (version_id, path) lookup and makes "at most one artifact per
-- relative path per version" a hard guarantee (previously only a soft
-- consequence of oss_key = prefix + path plus the (version_id, oss_key) UNIQUE).

BEGIN;

ALTER TABLE artifacts
    ADD COLUMN path TEXT;

UPDATE artifacts a
SET path = NULLIF(
        substring(a.oss_key FROM char_length(
            t.tenant_id::text || '/' || v.task_id::text || '/' || v.id::text || '/'
        ) + 1),
        ''
    )
FROM task_versions v
JOIN tasks t ON t.id = v.task_id
WHERE v.id = a.version_id
  AND a.oss_key LIKE (t.tenant_id::text || '/' || v.task_id::text || '/' || v.id::text || '/') || '%';

CREATE UNIQUE INDEX artifacts_version_path_key
    ON artifacts (version_id, path)
    WHERE path IS NOT NULL;

COMMIT;
