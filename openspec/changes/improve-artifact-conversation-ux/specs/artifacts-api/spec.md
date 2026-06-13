## MODIFIED Requirements

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

## ADDED Requirements

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
