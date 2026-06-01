## ADDED Requirements

### Requirement: Version Artifact List Endpoint

The API SHALL expose `GET /api/v1/versions/{version_id}/artifacts` returning the artifact metadata for one version. The response MUST be HTTP `200` with the unified envelope `{code, message, data, trace_id}` where `data = {version_id, artifacts}`:

- `artifacts` is an array of `{id, kind, mime, bytes, sha256, created_at}` ordered by `created_at ASC, id ASC` (reuses the existing `ListArtifactsByVersion` query). A version with no artifacts yet MUST return `data.artifacts = []` (the empty array, NOT `null`).
- The response MUST NOT include `oss_key`. The internal storage layout (bucket prefix, object key) is not part of the public contract; callers obtain bytes only through the presign endpoint.
- `mime`, `bytes`, and `sha256` are nullable in the table and MUST be serialized as `null` when absent, never omitted.

The endpoint MUST be scoped to the caller's `(tenant_id, user_id)`. A `version_id` that does not exist, OR exists but whose owning task belongs to a different owner, MUST return HTTP `404` with envelope `code = "version_not_found"` — never `403`, never reveal existence (mirrors `task-read-api` §"Owner-Scoped Reads Hide Unowned Resources"). The ownership probe is `task_versions.task_id → tasks` joined on `(tenant_id, user_id)`.

Path-param validation runs before ownership resolution: a malformed `{version_id}` UUID MUST return `400 invalid_input` regardless of whether the resource exists or is owned (matches the `parseUUIDParam` short-circuit in `task-read-api`).

`kind` is opaque free-text recorded by the worker (today every produced file is written with `kind = "file"`); the endpoint returns it verbatim and MUST NOT validate or enumerate it.

#### Scenario: List artifacts of an owned version
- **GIVEN** an owned version with two artifacts (both `kind = "file"`, the value the worker writes)
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `200` with `data.artifacts` containing two entries ordered by `created_at ASC, id ASC`, and no entry MUST contain an `oss_key` field

#### Scenario: Owned version with no artifacts
- **GIVEN** an owned version that has produced no artifacts yet (e.g., its run is still queued)
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `200` with `data.artifacts = []` (the empty array, NOT `null`)

#### Scenario: Nullable metadata is serialized as null
- **GIVEN** an owned version with an artifact whose `mime`, `bytes`, and `sha256` columns are NULL
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** that entry MUST contain `mime: null`, `bytes: null`, `sha256: null` (present-and-null, never omitted)

#### Scenario: Unowned or unknown version returns 404
- **GIVEN** a `version_id` that either does not exist OR is owned by a different user
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"` (never `403`, never differentiate the two cases)

#### Scenario: Malformed version_id returns 400
- **WHEN** the `{version_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the offending path field

### Requirement: Artifact Presigned Download Endpoint

The API SHALL expose `GET /api/v1/artifacts/{artifact_id}/presign` returning a short-lived, S3-compatible presigned **GET** URL so the browser downloads the object directly from OSS without proxying bytes through the API. The response MUST be HTTP `200` with `data = {url, expires_at, bytes, mime, sha256}`:

- `url` is the presigned GET URL for the artifact's `oss_key` within the configured bucket.
- `expires_at` is the RFC3339 UTC instant at which the URL stops working, equal to the time the URL was minted plus the configured presign TTL.
- `bytes`, `mime`, `sha256` echo the artifact row so the client can label the download without a second call; they follow the same nullable serialization as the list endpoint.

The endpoint MUST resolve the artifact, its `oss_key`, and ownership in a single query (`GetArtifactWithOwner` joins `artifacts → task_versions → tasks`). An `artifact_id` that does not exist, OR whose owning task belongs to a different owner, MUST return HTTP `404` with envelope `code = "artifact_not_found"` — never `403`, never reveal existence. The presigned URL MUST grant read access ONLY to the single requested object key; it MUST NOT be scoped to a prefix or bucket-wide. Path-param validation runs before ownership resolution: a malformed `{artifact_id}` UUID MUST return `400 invalid_input` regardless of whether the artifact exists.

`expires_at` is advisory: it is the handler's mint instant plus the configured TTL, returned so the client can label the link. OSS is the authority on actual expiry (the signature encodes a relative `X-Amz-Expires` anchored to the SDK's signing time); a client receiving a `403` from the URL MUST re-request rather than trust `expires_at` to the second.

Presigning is a pure signing operation and MUST NOT verify object existence. A URL minted for an artifact row whose underlying OSS object is missing (e.g. lifecycle-expired) returns HTTP `200` here and a `404` from OSS at download time; surfacing that is the client's responsibility.

#### Scenario: Presign an owned artifact
- **GIVEN** an owned artifact with `oss_key = "t/v/file/index.md"`, `bytes = 1024`, `mime = "text/markdown"`
- **WHEN** the caller `GET /api/v1/artifacts/{id}/presign`
- **THEN** the response MUST be HTTP `200` with `data.url` a presigned GET URL for that exact object key, `data.expires_at` equal to the handler's mint instant + configured TTL, and `data.bytes = 1024`, `data.mime = "text/markdown"`

#### Scenario: Presigned URL is single-object scoped
- **GIVEN** an owned artifact
- **WHEN** the presigned URL is generated
- **THEN** the URL MUST authorize a GET of only that artifact's object key (not a prefix or the whole bucket), and MUST expire after the configured TTL

#### Scenario: Unowned or unknown artifact returns 404
- **GIVEN** an `artifact_id` that does not exist OR is owned by a different user
- **WHEN** the caller `GET /api/v1/artifacts/{id}/presign`
- **THEN** the response MUST be HTTP `404` with `code = "artifact_not_found"` (never `403`, never differentiate the two cases)

#### Scenario: Malformed artifact_id returns 400
- **WHEN** the `{artifact_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the offending path field

### Requirement: API OSS Client Configuration

The API SHALL construct an S3-compatible OSS client at startup from configuration, used solely for minting presigned URLs (no object reads or writes flow through the API process). The four credential/location keys MUST reuse the worker's existing env contract verbatim so a single shared configuration drives both processes: `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET` (note `..._KEY_SECRET`, not `..._SECRET_ACCESS_KEY`). The API additionally introduces `OSS_REGION` (default `us-east-1`), `OSS_USE_PATH_STYLE` (default `true`; SeaweedFS/MinIO require path-style), and `OSS_PRESIGN_TTL` (default `5m`). All keys MUST be settable via environment and a `oss:` YAML block, consistent with the existing `config.Config` precedence.

The four shared OSS keys MUST be `required:"true"` at startup, matching the `DATABASE_URL` / `RABBITMQ_URL` pattern: the API MUST fail to boot (clear load error) when they are absent, rather than starting and failing per-request. Credentials MUST NOT be logged or returned in any response (AGENTS.md §6); the OSS config block MUST be excluded from any config-dump log line. The presign TTL MUST have a bounded default (a few minutes) so that leaked URLs expire quickly.

#### Scenario: Presign TTL drives expires_at
- **GIVEN** the configured presign TTL is `300s`
- **WHEN** an artifact is presigned at the handler's mint instant `T`
- **THEN** `data.expires_at` MUST equal `T + 300s` (UTC, RFC3339); OSS rejects the URL after the signature's own expiry, which `expires_at` reports advisorily

#### Scenario: Missing OSS configuration fails startup
- **WHEN** the API process starts with any of the four required `OSS_*` keys absent
- **THEN** configuration load MUST fail with a clear error naming the missing key, and the process MUST NOT begin serving (no per-request `oss_unconfigured` path exists)

#### Scenario: Presign SDK failure returns 500
- **GIVEN** a configured OSS client whose signing operation fails at request time (e.g. transient SDK/credential error)
- **WHEN** the caller `GET /api/v1/artifacts/{id}/presign` for an owned artifact
- **THEN** the response MUST be HTTP `500` with `code = "internal_error"` and the OSS credentials MUST NOT appear in the body or logs

#### Scenario: Credentials never surface
- **WHEN** any artifacts-api endpoint responds, OR the API logs a request
- **THEN** the OSS access key id and secret MUST NOT appear in the response body or any log field
