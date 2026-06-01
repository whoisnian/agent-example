## Why

The worker already persists every deliverable — generated code packages, research-report Markdown, images — into the `artifacts` table and the OSS `artifacts/` prefix (`worker/core/persistence.py::insert_artifact`, wired from `agents/base.py`). But no HTTP endpoint exposes them: a finished task produces output the user can neither list nor download. This is the single gap in the core loop (submit → execute → observe → **retrieve result**) with no fallback — unlike live status, which degrades gracefully to polling. This change adds the artifact read surface so the front-end (`web` VersionTree / TaskDetail) can finally surface and hand back what a task produced.

## What Changes

- Add `GET /api/v1/versions/{version_id}/artifacts` returning the artifact metadata list for one version — `{id, kind, mime, bytes, sha256, created_at}` per row, ordered `created_at ASC, id ASC` (reuses the existing `ListArtifactsByVersion` query). Owner-scoped through `task_versions → tasks`; never returns `oss_key` to the client (internal storage layout is not part of the contract).
- Add `GET /api/v1/artifacts/{artifact_id}/presign` returning a short-lived S3 presigned **GET** URL `{url, expires_at, bytes, mime, sha256}` so the browser downloads directly from OSS without proxying bytes through the API. Owner-scoped through `artifacts → task_versions → tasks`.
- Introduce an API-side S3-compatible OSS client (`infrastructure/oss`) wrapping `aws-sdk-go-v2` + `s3.PresignClient` — the API has no OSS dependency today. New config block under the existing `config.Config`, **reusing the worker's exact env names** for the four shared keys (`OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET`) plus API-only `OSS_REGION` / `OSS_USE_PATH_STYLE` / `OSS_PRESIGN_TTL`; the four shared keys are `required:"true"` (fail-fast boot, matching `DATABASE_URL`). SeaweedFS speaks the S3 protocol, same endpoint the worker writes to.
- Add new sqlc query `GetArtifactWithOwner :one` (joins `artifacts → task_versions → tasks` to resolve `oss_key` + ownership in one round-trip). Owner-scoped 404 (`artifact_not_found`) mirrors `add-task-read-api` — envelope code, never 403, never reveal existence.
- **Explicitly deferred**: `POST /api/v1/uploads/sts` (§5.1) ships in a separate follow-up change. It is the input-*upload* side (SeaweedFS STS temporary credentials bound to a path prefix), a heavier and distinct mechanism from presigned download, and there is no input-file ingestion flow in the current MVP loop. Keeping this change to the read path holds it focused and small (AGENTS.md §3 / §7).

## Capabilities

### New Capabilities

- `artifacts-api`: HTTP read endpoints for task artifacts — per-version metadata list and short-lived presigned download URL, both owner-scoped.

### Modified Capabilities

(none — `task-data-model` already defines the `artifacts` table; the worker write path is unchanged. These endpoints are pure read consumers.)

## Impact

- New code: `api/internal/infrastructure/oss/` (S3/presign client + config wiring), `api/internal/domain/task/artifact_read_*.go` (DTOs + read service), `api/internal/application/task/artifact_queries.go`, `api/internal/interfaces/http/artifact_reads.go` (2 GET handlers).
- New SQL: `api/queries/artifacts.sql` (+ `GetArtifactWithOwner`).
- New dependency: `github.com/aws/aws-sdk-go-v2` (config, credentials, service/s3) + the Go testcontainers **MinIO** module for the integration round-trip (the API suite has no S3 fixture today and the worker tests use MinIO, not SeaweedFS). First OSS dependency in `api/`.
- Config: new `OSS_*` env keys + `oss:` yaml block in `config.Config`; four keys shared with the worker become `required:"true"` — a standalone API deploy must now supply them (co-deployed stacks already have them).
- New metric: `OSSPresignTotal{outcome}` on the `Metrics` struct (AGENTS.md §7).
- Touches `cmd/api/main.go` to construct the OSS client and wire the handler set; `interfaces/http/server.go` `ServerDeps` gains `ArtifactHandlers` (same pattern as `TaskReadHandlers` / `TaskCostHandlers`).
- No migrations, no MQ topology changes.
- Unblocks the artifact column / download affordance in `add-web-cost-views` and the VersionTree result view.
- Reuses everything from `add-task-read-api`: unified `{code, message, data, trace_id}` envelope, owner-scoped 404, `Owner` value type, slog field discipline (`trace_id` / `task_id`).
