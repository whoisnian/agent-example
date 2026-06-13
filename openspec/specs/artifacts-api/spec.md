# artifacts-api Specification

## Purpose

HTTP read surface for task artifacts: an owner-scoped per-version metadata list, a short-lived API-signed download URL, and a download proxy route that streams artifact bytes from OSS through the API. The browser never contacts OSS — the API lists metadata (never leaking `oss_key`), mints single-artifact download tokens (ownership enforced at mint time), and proxies object bytes from the bucket to the client. This deliberately reverses the original "the API never proxies object bytes" stance: client reachability of `OSS_ENDPOINT` must never be a deployment constraint (change `add-artifact-download-proxy`). Established by archiving change `add-artifacts-api`; reworked by archiving change `add-artifact-download-proxy`.
## Requirements
### Requirement: Version Artifact List Endpoint

The API SHALL expose `GET /api/v1/versions/{version_id}/artifacts` returning the artifact metadata for one version. The response MUST be HTTP `200` with the unified envelope `{code, message, data, trace_id}` where `data = {version_id, artifacts}`:

- `artifacts` is an array of `{id, kind, path, mime, bytes, sha256, created_at}` ordered by `created_at ASC, id ASC` (reuses the existing `ListArtifactsByVersion` query). A version with no artifacts yet MUST return `data.artifacts = []` (the empty array, NOT `null`).
- The response MUST NOT include `oss_key`. The internal storage layout (bucket prefix, object key) is not part of the public contract; callers obtain bytes only through the presign endpoint.
- `path` is the artifact's version-relative file path (e.g. `index.html`, `css/style.css`); it is nullable (legacy rows) and MUST be serialized as `null` when absent, never omitted.
- `mime`, `bytes`, and `sha256` are nullable in the table and MUST be serialized as `null` when absent, never omitted.

The endpoint MUST be scoped to the caller's `(tenant_id, user_id)`. A `version_id` that does not exist, OR exists but whose owning task belongs to a different owner, MUST return HTTP `404` with envelope `code = "version_not_found"` — never `403`, never reveal existence (mirrors `task-read-api` §"Owner-Scoped Reads Hide Unowned Resources"). The ownership probe is `task_versions.task_id → tasks` joined on `(tenant_id, user_id)`.

Path-param validation runs before ownership resolution: a malformed `{version_id}` UUID MUST return `400 invalid_input` regardless of whether the resource exists or is owned (matches the `parseUUIDParam` short-circuit in `task-read-api`).

`kind` is opaque free-text recorded by the worker (today every produced file is written with `kind = "file"`); the endpoint returns it verbatim and MUST NOT validate or enumerate it.

#### Scenario: List artifacts of an owned version
- **GIVEN** an owned version with two artifacts (both `kind = "file"`, the value the worker writes)
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `200` with `data.artifacts` containing two entries ordered by `created_at ASC, id ASC`, each carrying its `path`, and no entry MUST contain an `oss_key` field

#### Scenario: Owned version with no artifacts
- **GIVEN** an owned version that has produced no artifacts yet (e.g., its run is still queued)
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `200` with `data.artifacts = []` (the empty array, NOT `null`)

#### Scenario: Nullable metadata is serialized as null
- **GIVEN** an owned version with an artifact whose `path`, `mime`, `bytes`, and `sha256` columns are NULL
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** that entry MUST contain `path: null`, `mime: null`, `bytes: null`, `sha256: null` (present-and-null, never omitted)

#### Scenario: Unowned or unknown version returns 404
- **GIVEN** a `version_id` that either does not exist OR is owned by a different user
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts`
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"` (never `403`, never differentiate the two cases)

#### Scenario: Malformed version_id returns 400
- **WHEN** the `{version_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the offending path field

### Requirement: Artifact Presigned Download Endpoint

The API SHALL expose `GET /api/v1/artifacts/{artifact_id}/presign` returning a short-lived, **API-relative signed download URL** so the browser fetches the object through the API's same-origin download proxy route — never directly from OSS. The response MUST be HTTP `200` with `data = {url, expires_at, bytes, mime, sha256}`:

- `url` is the relative URL `/api/v1/artifacts/{artifact_id}/download?token=<jwt>`, where the token is an HS256 JWT signed with `AUTH_JWT_SECRET` carrying `iss = "agent-api"`, `aud = "artifact-download"`, `sub = <artifact_id>`, and `exp` = mint instant + the configured `OSS_PRESIGN_TTL`. Minting is a local signing operation: no OSS/network call occurs on this path. Clients MUST treat `url` as an opaque string.
- `expires_at` is the RFC3339 UTC instant at which the URL stops working, equal to the time the URL was minted plus the configured TTL. Because JWT `exp` has second granularity, the mint instant MUST be truncated to whole seconds before adding the TTL so that `expires_at` and the token's `exp` denote the same instant (verification leeway may extend acceptance slightly but never shortens it).
- `bytes`, `mime`, `sha256` echo the artifact row so the client can label the download without a second call; they follow the same nullable serialization as the list endpoint.

The endpoint MUST resolve the artifact, its existence, and ownership in a single query (`GetArtifactWithOwner` joins `artifacts → task_versions → tasks`). An `artifact_id` that does not exist, OR whose owning task belongs to a different owner, MUST return HTTP `404` with envelope `code = "artifact_not_found"` — never `403`, never reveal existence. The signed token MUST grant read access ONLY to the single requested artifact; it MUST NOT be scoped to a prefix, a version, or bucket-wide. Path-param validation runs before ownership resolution: a malformed `{artifact_id}` UUID MUST return `400 invalid_input` regardless of whether the artifact exists.

Minting MUST NOT verify object existence in OSS. A URL minted for an artifact row whose underlying OSS object is missing returns HTTP `200` here and a `502 oss_unavailable` from the download route at fetch time; surfacing that is the client's responsibility.

#### Scenario: Presign an owned artifact

- **GIVEN** an owned artifact with `bytes = 1024`, `mime = "text/markdown"`
- **WHEN** the caller `GET /api/v1/artifacts/{id}/presign`
- **THEN** the response MUST be HTTP `200` with `data.url` a relative `/api/v1/artifacts/{id}/download?token=...` URL whose token is scoped to exactly that artifact, `data.expires_at` equal to the mint instant + configured TTL, and `data.bytes = 1024`, `data.mime = "text/markdown"`

#### Scenario: Signed URL is single-artifact scoped

- **GIVEN** an owned artifact
- **WHEN** the signed download URL is generated
- **THEN** its token MUST authorize a GET of only that artifact (`sub` pins the artifact id), MUST carry `aud = "artifact-download"`, and MUST expire after the configured TTL

#### Scenario: Unowned or unknown artifact returns 404

- **GIVEN** an `artifact_id` that does not exist OR is owned by a different user
- **WHEN** the caller `GET /api/v1/artifacts/{id}/presign`
- **THEN** the response MUST be HTTP `404` with `code = "artifact_not_found"` (never `403`, never differentiate the two cases)

#### Scenario: Malformed artifact_id returns 400

- **WHEN** the `{artifact_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the offending path field

### Requirement: Artifact Download Proxy Endpoint

The API SHALL expose `GET /api/v1/artifacts/{artifact_id}/download?token=...` which validates a short-lived signed download token and then streams the artifact's object bytes from OSS through the API to the client. The browser never contacts OSS directly; `OSS_ENDPOINT` only needs to be reachable from the API and Worker processes, never from clients.

The route MUST be registered as a public route (no Bearer middleware — `<img>` / `<iframe>` / navigation cannot carry an Authorization header, matching the WS `?token=` precedent) and MUST authenticate exclusively via the `token` query parameter:

- The token MUST be an HS256 JWT signed with `AUTH_JWT_SECRET`, carrying `iss = "agent-api"`, `aud = "artifact-download"`, `sub = <artifact_id>`, and a required `exp`. Verification MUST pin the algorithm and issuer, require the audience, and require `sub` to equal the path's `{artifact_id}`. Isolation between token kinds MUST be explicit and bidirectional: access tokens (no `aud`) MUST NOT be accepted here, AND the access-token verifier (`Verifier.Parse`) MUST explicitly reject any token carrying a non-empty `aud` claim — it MUST NOT rely on incidental claim-shape differences (e.g. a missing `tid`) to keep download tokens out of the Bearer path.
- A missing, malformed, expired, or otherwise invalid token — including an `sub`/path mismatch — MUST return HTTP `403` with envelope `code = "invalid_download_token"`, with a single undifferentiated code for every failure reason (mirrors `auth.ErrInvalidToken` non-enumerability and S3's 403-on-expired-presign behavior).
- A malformed `{artifact_id}` UUID MUST return `400 invalid_input` before token validation (matches `parseUUIDParam` short-circuit).
- A valid token whose artifact row no longer exists MUST return `404 artifact_not_found`.
- An OSS read failure (connection error, NoSuchKey, etc.) MUST return `502` with `code = "oss_unavailable"`; the response MUST NOT leak the `oss_key` or OSS error internals.

Ownership is enforced at mint time (the presign endpoint), not re-checked at download time: possession of an unexpired single-artifact token is the authorization, exactly like an S3 presigned URL.

On success the response MUST stream the object body (no full buffering in memory) with status `200` and headers:

- `Content-Type`: the artifact row's `mime`, or `application/octet-stream` when null (DB metadata is authoritative; the OSS-reported content type MUST NOT be trusted).
- `Content-Length`: set whenever the OSS object size is known, including a legitimate `0` for empty objects (non-nil check, never `>0`).
- `Content-Security-Policy: sandbox allow-scripts` — REQUIRED on every download response: artifacts are now served same-origin, and this header forces the document into an opaque origin even when opened as a top-level navigation, so stored HTML cannot script against the API origin. `allow-scripts` preserves the sandboxed-iframe rendered preview.
- `Referrer-Policy: no-referrer` — a served HTML document can still make network loads and could override the browser's default referrer policy to exfiltrate its own tokenized URL; this header forecloses that channel.
- `X-Content-Type-Options: nosniff` and `Cache-Control: private, no-store`.

A stream failure after headers are sent cannot produce an error envelope; the handler MUST abort the connection and record it via log + metric. The API MUST NOT log the request query string (token) on this route; logging the path and `artifact_id` is permitted. The handler MUST record a download metric labeled by outcome (`success` / `token_invalid` / `not_found` / `oss_error` / `stream_aborted`) and a bytes-streamed counter.

#### Scenario: Valid token streams the object

- **GIVEN** an unexpired download token minted for artifact `A` whose row has `mime = "text/html"`
- **WHEN** the client `GET /api/v1/artifacts/{A}/download?token=...`
- **THEN** the response MUST be HTTP `200` streaming the object bytes with `Content-Type: text/html`, `Content-Security-Policy: sandbox allow-scripts`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, and `Cache-Control: private, no-store`

#### Scenario: Invalid or expired token returns one undifferentiated 403

- **WHEN** the client calls the download route with a missing token, an expired token, an access token, or a token whose `sub` names a different artifact
- **THEN** every case MUST return HTTP `403` with `code = "invalid_download_token"`, never distinguishing the failure reason

#### Scenario: Access token is not a download token

- **WHEN** the client supplies a valid login access token (no `aud` claim) as the `token` query parameter
- **THEN** the response MUST be HTTP `403 invalid_download_token` (audience check fails)

#### Scenario: Download token is not an access token

- **WHEN** a client presents an unexpired download token as a Bearer credential on any authenticated API route
- **THEN** the request MUST be rejected as unauthenticated (`401`) because the access-token verifier explicitly rejects tokens carrying a non-empty `aud` claim — even if the download token were minted with extra claims matching the access-token shape

#### Scenario: Valid token for a deleted artifact returns 404

- **GIVEN** an unexpired token whose artifact row has been deleted since minting
- **WHEN** the client calls the download route
- **THEN** the response MUST be HTTP `404` with `code = "artifact_not_found"`

#### Scenario: OSS failure returns 502 without leaking internals

- **GIVEN** a valid token for an existing artifact whose OSS object is missing or whose OSS endpoint is unreachable
- **WHEN** the client calls the download route
- **THEN** the response MUST be HTTP `502` with `code = "oss_unavailable"`, and neither the `oss_key` nor the raw OSS error may appear in the body

#### Scenario: Token never reaches the access log

- **WHEN** any request hits the download route
- **THEN** the access log entry MUST NOT contain the query string or token value (path and artifact id only)

### Requirement: API OSS Client Configuration

The API SHALL construct an S3-compatible OSS client at startup from configuration, used to read artifact objects for the download proxy route (object bytes flow from OSS through the API to the client; the API still never writes objects). The four credential/location keys MUST reuse the worker's existing env contract verbatim so a single shared configuration drives both processes: `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET` (note `..._KEY_SECRET`, not `..._SECRET_ACCESS_KEY`). The API additionally reads `OSS_REGION` (default `us-east-1`), `OSS_USE_PATH_STYLE` (default `true`; SeaweedFS/MinIO require path-style), and `OSS_PRESIGN_TTL` (default `5m`), which now bounds the lifetime of API-signed download tokens. `OSS_ENDPOINT` MUST only be reachable from the API process — client/browser reachability is NOT required and MUST NOT be assumed anywhere. All keys MUST be settable via environment and a `oss:` YAML block, consistent with the existing `config.Config` precedence.

The four shared OSS keys MUST be `required:"true"` at startup, matching the `DATABASE_URL` / `RABBITMQ_URL` pattern: the API MUST fail to boot (clear load error) when they are absent, rather than starting and failing per-request. Credentials MUST NOT be logged or returned in any response (AGENTS.md §6); the OSS config block MUST be excluded from any config-dump log line. The token TTL MUST have a bounded default (a few minutes) so that leaked URLs expire quickly.

#### Scenario: Token TTL drives expires_at

- **GIVEN** the configured `OSS_PRESIGN_TTL` is `300s`
- **WHEN** an artifact download URL is minted at the handler's mint instant `T`
- **THEN** `data.expires_at` MUST equal `truncate-to-second(T) + 300s` (UTC, RFC3339) and the embedded token's `exp` MUST denote the same second-granularity instant

#### Scenario: Missing OSS configuration fails startup

- **WHEN** the API process starts with any of the four required `OSS_*` keys absent
- **THEN** configuration load MUST fail with a clear error naming the missing key, and the process MUST NOT begin serving (no per-request `oss_unconfigured` path exists)

#### Scenario: Credentials never surface

- **WHEN** any artifacts-api endpoint responds, OR the API logs a request
- **THEN** the OSS access key id and secret MUST NOT appear in the response body or any log field

### Requirement: Version Artifact Archive Download

The API SHALL expose a version-level archive download pair mirroring the single-artifact presign/download split:

**Mint** — `GET /api/v1/versions/{version_id}/artifacts/archive/presign` (Bearer-authenticated) returns `data = {url, expires_at}` where `url` is the relative URL `/api/v1/versions/{version_id}/artifacts/archive?token=<jwt>`. The token is an HS256 JWT signed with `AUTH_JWT_SECRET` carrying `iss = "agent-api"`, `aud = "artifact-archive"`, `sub = <version_id>`, and `exp` = mint instant (truncated to whole seconds) + `OSS_PRESIGN_TTL`. Ownership is enforced at mint time with the same single-probe rules as the artifact list: unknown or unowned versions return `404 version_not_found`; a malformed UUID returns `400 invalid_input`. Minting performs no OSS call and MUST NOT verify object existence.

**Download** — `GET /api/v1/versions/{version_id}/artifacts/archive?token=...` is a public route (no Bearer middleware) authenticated exclusively by the `token` query parameter, validated like the artifact download proxy (algorithm + issuer pinned, `aud = "artifact-archive"` required, `sub` must equal the path's `{version_id}`; access tokens rejected). Every token failure returns one undifferentiated `403 invalid_download_token`. On success the handler streams a ZIP archive of the version's artifacts: entries are read from OSS one at a time and written through `archive/zip` to the response (no full buffering); each entry's name is the artifact's `path` (UTF-8 entry names), falling back to `artifact-<id>` when `path` is NULL. Response headers: `Content-Type: application/zip`, `Content-Disposition: attachment; filename="artifacts-<version_id>.zip"`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`. A version with zero artifacts yields a valid empty ZIP (HTTP `200`). An OSS failure before the first byte returns `502 oss_unavailable`; a failure mid-stream aborts the connection and is recorded via log + metric (a ZIP cannot carry an error envelope after bytes are sent). The token rides in the query string, so the route MUST NOT log the request query string (the standard middleware logging only the path satisfies this); the handler MUST record an archive-download metric labeled by outcome and a bytes-streamed counter.

#### Scenario: Archive presign mints a version-scoped token

- **GIVEN** an owned version
- **WHEN** the caller `GET /api/v1/versions/{id}/artifacts/archive/presign`
- **THEN** the response MUST be HTTP `200` with `data.url` a relative archive URL whose token carries `aud = "artifact-archive"` and `sub` pinned to exactly that version, and `data.expires_at` equal to the mint instant + configured TTL

#### Scenario: Valid token streams a zip with path-named entries

- **GIVEN** an unexpired archive token for a version with artifacts `index.html` and `css/style.css`
- **WHEN** the client fetches the archive URL
- **THEN** the response MUST be HTTP `200` `application/zip` whose entries are named `index.html` and `css/style.css` with each entry's bytes matching the stored object

#### Scenario: Invalid, expired, or cross-version token returns one 403

- **WHEN** the archive route receives a missing token, an expired token, an access token, a single-artifact download token, or an archive token whose `sub` names a different version
- **THEN** every case MUST return HTTP `403` with `code = "invalid_download_token"`, never distinguishing the failure reason

#### Scenario: Unowned version cannot mint an archive token

- **GIVEN** a `version_id` owned by a different user (or unknown)
- **WHEN** the caller hits the archive presign endpoint
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"`

#### Scenario: Zero-artifact version yields an empty zip

- **GIVEN** a valid archive token for an owned version with no artifacts
- **WHEN** the client fetches the archive URL
- **THEN** the response MUST be HTTP `200` with a structurally valid empty ZIP archive

#### Scenario: Archive token never reaches the access log

- **WHEN** any request hits the archive download route
- **THEN** the access log entry MUST NOT contain the query string or token value (path only)

### Requirement: Directory-Aware Version Preview Route

The API SHALL expose a version-level preview surface that lets a sandboxed HTML preview resolve **relative** asset references (css/js/img) against sibling artifacts of the same version. Because relative URLs resolve against the document's path and never inherit a query string, the token rides in a **path segment**:

**Mint** — `GET /api/v1/versions/{version_id}/preview` (Bearer-authenticated) returns `data = {base_url, expires_at}` where `base_url` is the relative prefix `/api/v1/versions/{version_id}/preview/<jwt>`. The token is an HS256 JWT signed with `AUTH_JWT_SECRET` carrying `iss = "agent-api"`, `aud = "version-preview"`, `sub = <version_id>`, and `exp` = mint instant (truncated to whole seconds) + `OSS_PRESIGN_TTL`. Ownership and validation rules mirror the archive presign (`404 version_not_found`, `400 invalid_input`).

**Serve** — `GET /api/v1/versions/{version_id}/preview/{token}/{filepath...}` is a public route authenticated exclusively by the `{token}` path segment (same verification discipline; every failure is one undifferentiated `403 invalid_download_token`). The `{filepath...}` remainder is URL-decoded and sanitized: it MUST be cleaned (`path.Clean` semantics), and a path that is empty / resolves to `.` (no file named), or contains `..` segments, a backslash, or a leading `/`, MUST be rejected with `404 artifact_not_found` (no path escapes the version's artifact namespace). The sanitized path is resolved by exact match against the version's `artifacts.path`; no matching row (including `path IS NULL` rows, which are unreachable by an exact non-empty match) returns `404 artifact_not_found`. On a hit the handler streams the object bytes from OSS with the download proxy's exact security headers: `Content-Type` from the artifact row (`application/octet-stream` when null), `Content-Security-Policy: sandbox allow-scripts`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`. OSS failures return `502 oss_unavailable` without leaking `oss_key`. Because the token is a **path segment** (not a query param), the route MUST NOT rely on the default middleware (which logs the path verbatim and would capture the token): it MUST log a token-redacted form (e.g. the route template `…/preview/:token/*filepath` rather than the resolved path) and MUST NOT log the query string.

A document loaded from `<base_url>/index.html` that references `./css/style.css` therefore resolves to `<base_url>/css/style.css` — the same token prefix — and is served iff a sibling artifact with `path = "css/style.css"` exists. This directory-aware resolution covers **tag-sourced** subresources (`<link href>`, `<script src>`, `<img src>`, etc.), which load without a CORS gate. It does NOT cover script-initiated `fetch()` / `XMLHttpRequest` of relative URLs: the previewed document runs in an opaque origin (sandbox without `allow-same-origin`, plus the `CSP: sandbox` header), so such requests are cross-origin to the API and — by deliberate design — receive no CORS headers and fail. Absolute references (`/css/style.css`) escape the token prefix by design and are likewise NOT supported.

#### Scenario: Preview mint returns a tokenized base URL

- **GIVEN** an owned version
- **WHEN** the caller `GET /api/v1/versions/{id}/preview`
- **THEN** the response MUST be HTTP `200` with `data.base_url = /api/v1/versions/{id}/preview/<token>` where the token carries `aud = "version-preview"` and `sub` pinned to that version

#### Scenario: Relative asset reference resolves to the sibling artifact

- **GIVEN** a version with artifacts `index.html` and `css/style.css` and a valid preview token
- **WHEN** the client fetches `<base_url>/index.html` and the rendered document then requests `css/style.css` relative to it
- **THEN** both requests MUST return HTTP `200` with each artifact's bytes, the asset request landing on `<base_url>/css/style.css` under the same token

#### Scenario: Path traversal or empty path is rejected

- **WHEN** the preview route receives a filepath containing `..` (e.g. `../other-version/secret.txt`), a backslash, a leading `/`, or an empty / `.`-resolving path
- **THEN** the response MUST be HTTP `404` with `code = "artifact_not_found"` and no OSS read may occur

#### Scenario: Preview token never reaches the access log

- **WHEN** any request hits the preview serve route (token in the path segment)
- **THEN** no log field MUST contain the token segment value (the route logs a redacted template/path form, never the resolved path or query string)

#### Scenario: Unknown path under a valid token is a quiet 404

- **GIVEN** a valid preview token for a version without `missing.js`
- **WHEN** the client fetches `<base_url>/missing.js`
- **THEN** the response MUST be HTTP `404` with `code = "artifact_not_found"` (an in-iframe asset 404 renders quietly; no other side effect)

#### Scenario: Preview responses carry the sandbox headers

- **WHEN** any preview file is served
- **THEN** the response MUST carry `Content-Security-Policy: sandbox allow-scripts`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, and `Cache-Control: private, no-store`, with `Content-Type` taken from the artifact row

#### Scenario: Wrong-audience or cross-version token is one 403

- **WHEN** the preview route receives an access token, a download/archive token, an expired preview token, or a preview token whose `sub` names a different version
- **THEN** every case MUST return HTTP `403` with `code = "invalid_download_token"`

