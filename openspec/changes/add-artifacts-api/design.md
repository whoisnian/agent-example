## Context

The worker writes artifacts to the `artifacts` table (`id, version_id, kind, oss_key, mime, bytes, sha256, created_at`) and uploads the bytes to the OSS `artifacts/` prefix; this is the one business table the worker may write (AGENTS.md §4.2). The read side is missing: there is no HTTP endpoint, and the API process has never talked to OSS — `api/go.mod` has no AWS/S3 dependency and `config.Config` has no OSS block. SeaweedFS (the MVP OSS) speaks the S3 protocol, the same endpoint the worker's `aioboto3` client writes to.

This change adds the artifact read surface. It reuses the read-API plumbing already in place: the `{code, message, data, trace_id}` envelope, the `Owner{tenant_id, user_id}` value type, owner-scoped 404 (`task_not_found` / `version_not_found`), and slog field discipline (`add-task-read-api`, `add-task-cost-api`).

## Goals / Non-Goals

**Goals:**
- List a version's artifact metadata, owner-scoped, without leaking internal storage keys.
- Hand the browser a short-lived, single-object presigned GET URL so large bytes never proxy through the API.
- Introduce a minimal, reusable API-side OSS client so future presign/STS needs share one wiring point.

**Non-Goals:**
- `POST /uploads/sts` (input-upload credentials) — deferred to a separate change (see D5).
- Streaming/proxying artifact bytes through the API. The API mints URLs; OSS serves bytes.
- Artifact mutation (delete, re-tag) — artifacts are INSERT-only by contract.
- Async packaging / large-artifact streaming (ARCHITECTURE §13 open question #4) — out of MVP.
- Nice download filenames / forced `Content-Disposition`. The presigned GET serves whatever `Content-Type`/`Content-Disposition` the worker set on the object at upload time, so a report may open inline with a key-derived name. Post-MVP we can derive a filename from the `oss_key` basename and pass it via the signed `response-content-disposition` query param; not done here to keep scope tight. HEAD/Range need no work — presigned GET supports Range at the OSS layer natively.

## Decisions

### D1 — Presigned GET URL, not API byte-proxy
The presign endpoint returns a time-limited S3 presigned URL; the browser downloads straight from OSS. **Why:** artifacts are code bundles / reports / images that can be large; proxying them through the API wastes API bandwidth, memory, and request-timeout budget, and couples download latency to API health. **Alternative considered:** stream bytes via `GET /artifacts/{id}/download`. Rejected for MVP — presign is the documented OSS pattern (ARCHITECTURE §3.5 "使用 STS 临时凭证；前端直传时绑定 path 前缀") and keeps the API stateless w.r.t. payload size.

### D2 — `aws-sdk-go-v2` + `s3.PresignClient`, path-style addressing
Use the official v2 SDK's `s3.NewPresignClient(...).PresignGetObject`. **Why:** SeaweedFS is S3-protocol-compatible; the v2 SDK is the maintained path and supports a custom endpoint resolver + `UsePathStyle = true` (SeaweedFS does not do virtual-host buckets). The TTL is passed via `s3.WithPresignExpires`. **Alternative:** minio-go. Rejected for the *client* to avoid a second S3 abstraction in the monorepo and to stay vendor-neutral; the worker already proves S3 path-style works.

`PresignGetObject` returns only the URL (the expiry is the relative `X-Amz-Expires` baked into the signed query, anchored to the SDK's `X-Amz-Date`); it does **not** hand back an absolute instant. The handler therefore captures one `now()` and computes `expires_at = now + TTL` itself, reporting it advisorily (see `expires_at` semantics in the spec and the clock-skew Risk). OSS remains the authority on actual expiry.

### D2a — MinIO testcontainers as the integration test double; SeaweedFS only in compose
The API integration suite has no S3/OSS fixture today (`api/go.mod` pulls only the Postgres testcontainers module), and the worker's tests use **MinIO** (`testcontainers.minio`), not SeaweedFS — SeaweedFS exists only as a docker-compose dev service. So the presign+GET round-trip integration test stands up a **MinIO container via the Go testcontainers MinIO module** (S3 path-style, the same surface SeaweedFS exposes and the same double the worker already trusts). **Why not a real SeaweedFS fixture:** there is no Go testcontainers SeaweedFS module; wiring a generic container + healthcheck + bucket pre-create is non-trivial and would push this PR past the ~500-line budget for no protocol-coverage gain over MinIO. SeaweedFS stays the compose/manual target. **Alternative:** mock the presigner entirely in a unit test and skip the real round-trip — kept as the fallback split (see D5a / tasks) if even the MinIO fixture proves heavy.

### D3 — Single-object presign scope; never prefix/bucket
`PresignGetObject` is inherently scoped to one `{bucket, key}`. The handler resolves `oss_key` from `GetArtifactWithOwner` and presigns exactly that key. **Why:** a prefix- or bucket-wide URL would let a leaked link walk a tenant's whole artifact namespace. The owner check happens in SQL *before* the key is ever presigned, so an unowned artifact never produces a URL.

### D4 — Ownership resolved in one SQL join, mapped to existing 404 sentinels
New `GetArtifactWithOwner :one` joins `artifacts → task_versions → tasks` and returns exactly `oss_key, bytes, mime, sha256, tenant_id, user_id` — the presign response (`{url, expires_at, bytes, mime, sha256}`) needs no more, so `id`/`kind`/`created_at` are deliberately left out of the select list to keep the sqlc row from carrying unused columns into DTO assembly (defends the never-serialize-`oss_key` invariant against struct-embedding drift). The service compares `(tenant_id, user_id)` to the caller's `Owner` and maps both "no row" and "wrong owner" to a single 404 — no information leak. The list endpoint reuses `ListArtifactsByVersion` but gates it behind an ownership probe of the version (same `ownedVersion`-style guard `task-read-api` uses), so an unowned version returns `version_not_found` before any artifact row is read. New error code `artifact_not_found` is added alongside the existing `task_not_found` / `version_not_found`.

### D5 — Defer `POST /uploads/sts` to a follow-up change
ARCHITECTURE §5.1 lists `/uploads/sts`, but it is the input-*upload* path: it issues SeaweedFS STS temporary credentials bound to a write prefix, a distinct and heavier mechanism (STS assume-role via `aws-sdk-go-v2/service/sts` + `stscreds` vs. request presigning) than download presigning. The current MVP creates tasks from `prompt + params` (text) with no file-upload flow, so nothing consumes uploaded inputs yet. **Why defer:** keeps this change focused and under the ~500-line PR budget (AGENTS.md §3/§7), and avoids designing the STS trust model before there is a consumer. The `infrastructure/oss` package is the natural home for the future STS issuer — it shares endpoint/region/credential config — though STS uses a distinct SDK client, so this is a config-reuse convenience, not a structural extension point we design for now.

### D5a — PR-size guard / split plan
Non-test, non-generated surface (oss client + config + two DTOs + two read-service methods + application orchestration + two handlers + error-code + ServerDeps/main wiring) is plausibly 400–500 lines before the MinIO fixture. If the fixture (D2a) proves heavy, land the read endpoints with a mocked-presigner unit/contract test first and the real-OSS round-trip as a fast follow-up change — the unit/contract layer fully covers the wire contract; the round-trip only guards S3-protocol drift.

### D6 — `oss_key` is internal; never serialized
The list and presign responses expose `kind/mime/bytes/sha256/created_at` but never `oss_key`. **Why:** the object-key layout (`{tenant}/{task}/{version}/{type}/{file}`) is an internal storage convention (ARCHITECTURE §3.5); leaking it invites key-guessing and couples clients to the bucket scheme. Clients reach bytes only through presign.

### D7 — Config + startup wiring (env names match the worker; required-at-startup)
Add an `OSS` struct to `config.Config`. The four credential/location keys **reuse the worker's exact env names** — `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET` (verified against `worker/worker/core/config.py`; note `..._KEY_SECRET`, not the AWS-idiomatic `..._SECRET_ACCESS_KEY`) — so one shared config drives both processes; a divergent name would silently leave the API unconfigured. New API-only keys: `OSS_REGION` (default `us-east-1`, matching the worker's `storage.py`), `OSS_USE_PATH_STYLE` (default `true`), `OSS_PRESIGN_TTL` (default `5m`). The four shared keys are `required:"true"`, matching the `DATABASE_URL` / `RABBITMQ_URL` pattern in `config.go` — **fail-fast at boot**, not a soft per-request `oss_unconfigured` path (chosen over the originally-sketched soft fallback because it matches the house pattern, surfaces misconfig immediately, and removes a never-exercised error branch). `cmd/api/main.go` constructs the OSS client once and injects it into `ArtifactHandlers` (new `ServerDeps` field, same shape as `TaskReadHandlers` / `TaskCostHandlers`). Credentials are static creds for MVP; they are never logged (the OSS struct is excluded from any config-dump log line).

### D8 — Observability: presign is an external call, so it gets a metric
AGENTS.md §7 requires every new external interaction to add a metric/log field. Presign mints a URL via the S3 SDK, so add `OSSPresignTotal` (CounterVec by `outcome ∈ {success, error}`) to the `Metrics` struct, mirroring the existing `MQPublishFailures` / `MQPublishDuration` convention; increment per presign attempt. Each handler also logs with `trace_id` + `artifact_id`/`version_id` (no `oss_key`, no creds). This makes an OSS-down or surging-presign incident visible rather than silent.

## Risks / Trade-offs

- **[Clock skew between API and OSS makes `expires_at` slightly inaccurate]** → `expires_at` is advisory for the client UI; OSS is the authority on expiry. TTL default is a few minutes, well above expected skew. Document that the client should re-request on a 403 from the URL rather than trust `expires_at` to the second.
- **[Leaked presigned URL is replayable until TTL]** → bounded by a short default TTL (D2) and single-object scope (D3). Acceptable for MVP; tighter controls (one-time URLs, IP binding) are post-MVP.
- **[S3-protocol presign quirks vs. AWS]** → path-style + explicit endpoint resolver (D2) is the known-good config the worker already uses; an integration test against a **MinIO** testcontainer (D2a) exercises a real presign+GET round-trip to catch protocol drift. SeaweedFS-specific quirks (if any beyond the shared S3 surface) surface in compose/manual testing, not CI.
- **[Presign of a row whose OSS object is gone]** → presign does not check existence; the URL returns 200 here and 404 from OSS at download. Low-risk today (`artifacts/` has no documented lifecycle TTL and the worker inserts the row only after a successful upload), spec'd explicitly so the client surfaces the OSS error.
- **[New AWS SDK dependency surface]** → scope the import to `config`, `credentials`, and `service/s3` only; no broader AWS surface enters the module.
- **[`oss_key` accidentally serialized via struct embedding]** → the HTTP DTO is a hand-written struct that does not embed the sqlc row; a contract test asserts the JSON body has no `oss_key` field.

## Migration Plan

- Additive only: no DB migration (table exists), no MQ topology change, no change to the worker write path.
- **Deployment note (behavior change):** the four shared `OSS_*` keys become `required:"true"` for the API (D7), so the API will refuse to boot until they are set — same env the worker already requires, so a co-deployed stack already has them, but a standalone API deployment must now supply them. Document in the API README / compose.
- Rollback: revert the change; the worker keeps writing artifacts, no data is affected.

## Open Questions

- Should `expires_at` TTL be per-request-overridable (e.g. a longer link for a CI download) or fixed by config? MVP: fixed by config; revisit if a use case appears.
- Artifact `kind` is free-text from the worker, which **today writes only `kind = "file"`** for every produced artifact (`worker/agents/base.py`). Listing returns it verbatim; a richer taxonomy (`report` / `bundle` / `image` / …) would be a future worker change, and only then would API-side enumeration/validation make sense. Out of scope here.
