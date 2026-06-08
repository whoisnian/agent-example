## MODIFIED Requirements

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
