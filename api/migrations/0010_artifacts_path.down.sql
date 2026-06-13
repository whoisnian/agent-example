-- 0010_artifacts_path (down)

BEGIN;

DROP INDEX IF EXISTS artifacts_version_path_key;

ALTER TABLE artifacts
    DROP COLUMN path;

COMMIT;
