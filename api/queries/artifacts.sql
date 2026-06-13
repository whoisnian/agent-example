-- name: ListArtifactsByVersion :many
SELECT *
FROM artifacts
WHERE version_id = $1
ORDER BY created_at ASC, id ASC;

-- name: GetArtifactWithOwner :one
-- Resolves an artifact's storage key + owning identity in one round-trip
-- (artifacts → task_versions → tasks). Selects only what the presign endpoint
-- needs; id/kind/created_at are intentionally omitted so unused columns never
-- reach DTO assembly (design D4 — defends the never-serialize-oss_key invariant).
SELECT a.oss_key, a.bytes, a.mime, a.sha256, t.tenant_id, t.user_id
FROM artifacts a
JOIN task_versions v ON v.id = a.version_id
JOIN tasks t ON t.id = v.task_id
WHERE a.id = $1;

-- name: ListArtifactObjectsByVersion :many
-- Storage keys + relative paths for streaming a version's artifacts as a zip
-- archive (artifact-archive download). Includes oss_key by design: the archive
-- handler streams object bytes, it does NOT assemble a metadata DTO, so the
-- never-serialize-oss_key invariant (which guards DTO assembly) does not apply.
SELECT id, oss_key, path, mime
FROM artifacts
WHERE version_id = $1
ORDER BY created_at ASC, id ASC;

-- name: GetArtifactObjectByVersionPath :one
-- Resolves one artifact's storage key + content type by (version_id, path) for
-- the directory-aware version preview route. Ownership was enforced at preview
-- mint time (the token's sub pins the version); the serve route only needs the
-- object key and authoritative mime. The partial UNIQUE index on
-- (version_id, path) WHERE path IS NOT NULL guarantees at most one match.
SELECT oss_key, mime
FROM artifacts
WHERE version_id = $1 AND path = $2;
