-- name: ListArtifactsByVersion :many
SELECT *
FROM artifacts
WHERE version_id = $1
ORDER BY created_at ASC, id ASC;
