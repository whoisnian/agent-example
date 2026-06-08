## Why

The API authenticates the single configured dev principal by comparing `{email, password}` constant-time against `AUTH_DEV_EMAIL`/`AUTH_DEV_PASSWORD` in process memory (`auth_login.go`); there is no `tenants`/`users` table, so two real users cannot coexist and every token resolves to the same hardcoded `DEV_TENANT_ID`/`DEV_USER_ID`. `add-api-auth-jwt` flagged this as the next step, and the web login (`add-web-auth-login`) is already wired to consume real users. This change introduces the persistent identity store the rest of the platform's owner-scoping has always assumed.

## What Changes

- Add persistent `tenants` and `users` tables (migration `0007`), `users.password_hash` holding a **bcrypt** digest, `users.email` unique (case-insensitive via app-level normalization).
- Add a credential-verification path backed by the `users` table: `POST /api/v1/auth/login` now looks up the user by email, verifies the bcrypt hash, and issues a token for **that user's** `tenant_id`/`user_id` — no longer the process-global dev principal.
- Preserve the existing security contract verbatim: unknown-email and wrong-password both return `401 invalid_credentials` (indistinguishable), the comparison stays constant-time (bcrypt + a dummy-hash compare on the unknown-email branch so the timing of "no such user" matches "wrong password"), and the password never enters a response body or log field.
- Bridge the existing config to the new store with an **idempotent boot-time dev seed**: on startup the API upserts a tenant + user from `DEV_TENANT_ID`/`AUTH_DEV_EMAIL`/`AUTH_DEV_PASSWORD` (hashing the password), so the build still ships **no well-known weak login** (the password is operator-supplied and required) and existing dev workflows keep working unchanged. The direct `DevEmail`/`DevPassword`/`DevTenantID`/`DevUserID` fields on `AuthHandlers` are removed.
- Promote `golang.org/x/crypto` from an indirect to a direct dependency (already in `go.sum` at `v0.51.0`) by importing `golang.org/x/crypto/bcrypt`; add sqlc queries for user lookup and seed upsert.
- **Out of scope (Post-MVP / follow-ups):** user registration / self-service endpoints, password reset, multi-user admin CRUD, and adding the `tasks.tenant_id`/`tasks.user_id` → `users`/`tenants` FK constraints (deferred to avoid the seed-ordering chicken-and-egg; the `0002` schema comment already anticipates a later ALTER).

## Capabilities

### New Capabilities
- `api-user-store`: the persistent identity store — `tenants`/`users` schema, bcrypt password hashing, case-insensitive email uniqueness, the DB-backed credential lookup used by login, and the idempotent boot-time dev seed.

### Modified Capabilities
- `api-auth`: the "JWT Issuance via Login" credential source changes from the in-memory configured dev principal to the `users` table (the issued token now carries the looked-up user's `tenant_id`/`user_id`); the "JWT and Credential Configuration" requirement changes the role of `AUTH_DEV_*`/`DEV_*` from a runtime comparison to seed inputs. The indistinguishability, constant-time, and no-secret-logging guarantees are retained. The other api-auth requirements — `Bearer Token Authentication`, `Authenticated Principal Drives Ownership`, and `WebSocket Token Authentication` — are **intentionally NOT modified**: they already read identity from token claims (not the credential source), so removing the last `AuthHandlers.DevTenantID/DevUserID` fields does not change their behavior.

## Impact

- **Code:** `api/migrations/0007_*` (new); `api/queries/users.sql` (new); `api/internal/domain/identity/` (new — `User` entity + repository port + credential-verification service); `api/internal/infrastructure/persistence/` (new user repository over sqlc); `api/internal/interfaces/http/auth_login.go` + `auth_login_test.go` (rewired to the store); `api/internal/interfaces/http/auth_middleware_test.go` (constructs `AuthHandlers` with the removed `Dev*` fields — must be updated to the new shape or it fails to compile); `api/cmd/api/main.go` (wire the repo, run the boot seed; fix the stale `auth_login.go`/`main.go` doc comments); `api/internal/infrastructure/config/config.go` (doc/role of `AUTH_DEV_*`/`DEV_*` as seed inputs).
- **Dependencies:** add `golang.org/x/crypto/bcrypt`.
- **Schema:** new `tenants`/`users` tables; no change to existing tables (FKs deferred).
- **Behavior:** login responses, error codes, and HTTP status are unchanged from the client's perspective; only the credential source moves from config to DB. WS/Bearer middleware unaffected (they already read identity from token claims).
