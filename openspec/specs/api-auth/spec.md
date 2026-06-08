# api-auth Specification

## Purpose

The API's authentication capability: HS256 JWT issuance at `POST /api/v1/auth/login`, Bearer-token validation that injects a request-context `Principal{tenant_id, user_id}`, identity-from-claims for owner-scoping every business read/write, WebSocket `?token=` validation, and JWT/credential configuration. It replaces the pass-through auth stub and the process-global dev identity so that two different tokens resolve to two different owners. The credential source is the persistent `users` table (capability `api-user-store`): login resolves the principal from the looked-up user, and the MVP dev account is a row seeded at boot from configuration rather than an in-memory comparison. Established by archiving change `add-api-auth-jwt`; the credential source moved to the user store by archiving `add-api-user-store`.

## Requirements

### Requirement: JWT Issuance via Login

The API SHALL expose `POST /api/v1/auth/login` accepting `{email, password}` and, on valid credentials, returning HTTP `200` with the unified envelope `{code, message, data, trace_id}` where `data = {token, expires_at, user}`:

- `token` is a signed JWT (HS256) whose claims carry at least the caller's `tenant_id`, `user_id`, an issued-at, and an expiry.
- `expires_at` is the RFC3339 UTC instant the token stops being valid (issue time + configured TTL).
- `user` is `{id, tenant_id, email}` — the authenticated principal, never including the password or any secret.

The credential source SHALL be the persistent `users` table (capability `api-user-store`): login looks up the user by normalized email, verifies the password against the stored bcrypt hash, and on success issues the token for **that user's** `tenant_id`/`user_id` (no longer a process-global dev principal). The dev principal now exists as a seeded `users` row, not an in-memory comparison.

The login route MUST be **public** (reachable without a prior token) and MUST be exempt from the Bearer-token middleware. Credential verification MUST be constant-time with respect to the password and MUST NOT reveal whether the email or the password was the mismatch: an email with no matching user and a wrong password MUST both return HTTP `401` with `code = "invalid_credentials"`, and the no-such-user path MUST perform an equivalent bcrypt comparison (dummy hash) so it is not measurably faster. A malformed body MUST return `400 invalid_input`. The password MUST NOT appear in any response body or log field.

#### Scenario: Valid credentials return a signed token and principal
- **GIVEN** a user row in the `users` table with a bcrypt hash of its password
- **WHEN** the caller `POST`s `/api/v1/auth/login` with that user's matching `{email, password}`
- **THEN** the response MUST be HTTP `200` with `data.token` a verifiable HS256 JWT carrying that user's `tenant_id`/`user_id`, `data.expires_at` equal to the issue instant plus the configured TTL, and `data.user = {id, tenant_id, email}` with no password field

#### Scenario: Wrong password and unknown email are indistinguishable
- **WHEN** the caller `POST`s `/api/v1/auth/login` with a wrong password OR an email that has no matching user
- **THEN** both MUST return HTTP `401` with `code = "invalid_credentials"`, with no indication of which field mismatched and no measurable timing difference, and the password MUST NOT be logged

#### Scenario: Malformed login body returns 400
- **WHEN** the request body is not valid JSON or is missing `email`/`password`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"`

### Requirement: Bearer Token Authentication

The API SHALL replace the pass-through auth stub with a middleware that authenticates every non-public request via the `Authorization: Bearer <jwt>` header. The middleware MUST verify the JWT signature against the configured secret and reject an expired token. On success it MUST resolve a `Principal{tenant_id, user_id}` from the token claims and store it in the request context for downstream handlers. On a missing, malformed, wrong-signature, or expired token it MUST return HTTP `401` with the envelope `code = "unauthenticated"` and MUST NOT invoke the handler.

The middleware MUST exempt a fixed allowlist of public routes from authentication, matched on `(method, route-template)` so only the intended verb is public: `GET /healthz`, `GET /readyz`, `GET /metrics`, and `POST /api/v1/auth/login`. All other routes — every `/api/v1/*` business endpoint — MUST require a valid token. (Matching the route template, not the raw path, prevents trailing-slash/query spoofing.) The `401` body MUST carry the standard `{code, message, data, trace_id}` envelope so the existing web client's `401 → clear token + redirect /login` path triggers unchanged.

#### Scenario: Valid token authenticates and injects the principal
- **WHEN** a request to a protected route carries a valid, unexpired `Authorization: Bearer <jwt>`
- **THEN** the middleware MUST resolve the principal from the claims, place it in the request context, and invoke the handler (which reads identity from that context)

#### Scenario: Missing or invalid token is rejected with 401
- **WHEN** a request to a protected route has no `Authorization` header, a non-`Bearer` scheme, a wrong-signature token, or an expired token
- **THEN** the response MUST be HTTP `401` with `code = "unauthenticated"` and the handler MUST NOT run

#### Scenario: Public routes bypass authentication
- **WHEN** a request hits `/healthz`, `/readyz`, `/metrics`, or `POST /api/v1/auth/login` with no token
- **THEN** the middleware MUST NOT reject it for missing auth; the route proceeds normally

### Requirement: Authenticated Principal Drives Ownership

Business handlers SHALL derive the caller's `tenant_id`/`user_id` from the authenticated principal in the request context (via a `PrincipalFromContext` accessor), NOT from a process-global dev identity. The hardcoded `DevTenantID`/`DevUserID` handler fields MUST be removed. All existing owner-scoping behavior (e.g. `task-read-api`'s `404 task_not_found` / `version_not_found` for unowned resources, the `/me/cost` owner scope, artifact ownership) MUST continue to hold verbatim — only the source of the identity changes from a constant to the per-request token, so two different tokens now resolve to two different owners.

#### Scenario: Two tokens resolve to two distinct owners
- **GIVEN** two valid tokens whose claims carry different `(tenant_id, user_id)` pairs
- **WHEN** each calls the same owner-scoped endpoint (e.g. `GET /api/v1/tasks`)
- **THEN** each MUST see only its own resources, and neither MUST see the other's — enforced by the same ownership checks, now keyed on the token principal rather than a shared constant

#### Scenario: Ownership 404 semantics are preserved
- **WHEN** an authenticated caller requests a `task_id` owned by a different principal
- **THEN** the response MUST remain HTTP `404` with `code = "task_not_found"` (never `403`), exactly as specified by `task-read-api`

### Requirement: WebSocket Token Authentication

The `GET /api/v1/ws` endpoint SHALL validate the `?token=<jwt>` query parameter as a real JWT rather than merely checking its presence. The handshake MUST verify the signature and expiry and resolve the connection's `Principal{tenant_id, user_id}` from the claims. A missing, empty, malformed, wrong-signature, or expired token MUST close the WebSocket with code `4001` and MUST NOT register the connection. The subscribe-time ownership boundary is unchanged and now resolves against the connection's token-derived principal (per-connection isolation is real, not a shared stub identity). The token MUST be read via the query string only (never logged), and the `Origin` allowlist check still runs first.

#### Scenario: Valid token registers the connection with its principal
- **WHEN** a client opens `GET /api/v1/ws?token=<valid-jwt>` from an allowed origin
- **THEN** the handshake MUST succeed and the connection MUST be tracked with the principal resolved from the token claims, with zero subscriptions until a `subscribe` frame

#### Scenario: Invalid or expired WS token closes 4001
- **WHEN** a client opens `GET /api/v1/ws` with a missing/empty token, OR a token with a bad signature or past expiry
- **THEN** the server MUST close the WebSocket with code `4001` and MUST NOT register the connection or deliver any event

### Requirement: JWT and Credential Configuration

The API SHALL construct its JWT signer/verifier at startup from configuration. `AUTH_JWT_SECRET` MUST be `required:"true"` (fail-fast at boot with a clear error naming the key, matching the `DATABASE_URL` / `OSS_*` pattern). `AUTH_JWT_TTL` MUST have a bounded default (e.g. a day) controlling token lifetime.

The dev credential configuration SHALL act as **seed input** for the persistent user store rather than a runtime comparison: at startup the API idempotently upserts a `users` row with `id = DEV_USER_ID`, `tenant_id = DEV_TENANT_ID`, `email = AUTH_DEV_EMAIL`, and `password_hash = bcrypt(AUTH_DEV_PASSWORD)` (see capability `api-user-store`). `AUTH_DEV_PASSWORD` MUST remain `required:"true"` (no default, so the build ships no well-known weak login); `AUTH_DEV_EMAIL` MAY have a default. All keys MUST be settable via environment and the YAML config block, consistent with the existing `config.Config` precedence.

`AUTH_JWT_SECRET` and `AUTH_DEV_PASSWORD` MUST NEVER appear in any response body or log field, and the seeded `password_hash` MUST NOT be logged. The project currently has NO config-dump/log line (`config.go` notes the loader prints none, and the existing `OSS_*` credentials are likewise never printed); this requirement is a standing invariant rather than reuse of an existing redaction path — IF any configuration print is ever introduced, it MUST exclude these secrets alongside the `OSS_*` credentials.

#### Scenario: Missing required auth config fails startup
- **WHEN** the API process starts with `AUTH_JWT_SECRET` OR `AUTH_DEV_PASSWORD` absent
- **THEN** configuration load MUST fail with a clear error naming the missing key, and the process MUST NOT begin serving (no default weak password is shipped, and no dev user can be seeded without one)

#### Scenario: Secret never surfaces
- **WHEN** any endpoint responds OR the API logs its configuration
- **THEN** the JWT secret, the dev password, and the seeded password hash MUST NOT appear in the response body or any log field

#### Scenario: TTL drives token expiry
- **GIVEN** a configured `AUTH_JWT_TTL` of `T`
- **WHEN** a token is issued at instant `I`
- **THEN** the token's `exp` claim and the login `expires_at` MUST equal `I + T`, and the Bearer middleware MUST reject the token after that instant
