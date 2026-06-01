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
