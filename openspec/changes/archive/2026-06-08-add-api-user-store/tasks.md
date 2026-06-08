## 1. Schema & queries

- [ ] 1.1 Add migration `api/migrations/0007_init_identity.up.sql`: `CREATE TABLE tenants` (id UUID PK, name TEXT NOT NULL, created_at) and `CREATE TABLE users` (id UUID PK, tenant_id UUID NOT NULL REFERENCES tenants(id), email TEXT NOT NULL, password_hash TEXT NOT NULL, created_at, updated_at) + `CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email))`
- [ ] 1.2 Add `api/migrations/0007_init_identity.down.sql` dropping `users` then `tenants` (reverse order); no other table touched
- [ ] 1.3 Add `api/queries/users.sql` with `GetUserByEmail :one` (lookup by `lower(email) = lower($1)`), `UpsertTenant :exec` (ON CONFLICT (id) DO NOTHING), and `UpsertUser :exec` (ON CONFLICT (id) DO UPDATE refreshing tenant_id/email/password_hash/updated_at)
- [ ] 1.4 Run `make sqlc`; confirm generated `internal/infrastructure/persistence/sqlc` has the new methods and `make sqlc` reports no diff

## 2. Domain: identity

- [ ] 2.1 Create `api/internal/domain/identity/identity.go`: `User` entity (`{ID, TenantID uuid.UUID; Email, PasswordHash string}`), `Repository` port (`FindByEmail(ctx, normalizedEmail) (User, error)`), and sentinels `ErrUserNotFound` / `ErrInvalidCredentials`
- [ ] 2.2 Add `Authenticator` service with `Verify(ctx, email, password) (User, error)`: lowercase-normalize email, call repo, run `bcrypt.CompareHashAndPassword`; on `ErrUserNotFound` run a bcrypt compare against a package-level fixed dummy hash (no early return) and return `ErrInvalidCredentials`; collapse any verify failure to `ErrInvalidCredentials`
- [ ] 2.3 Add `golang.org/x/crypto/bcrypt` to `api/go.mod` (and `go mod tidy`); document the 72-byte truncation in the verifier doc comment
- [ ] 2.4 Unit tests (`identity_test.go`, fake repo): valid creds → user; wrong password → `ErrInvalidCredentials`; unknown email → `ErrInvalidCredentials` with the dummy-hash compare exercised; mixed-case email finds the lowercased row. Fixtures hash at `bcrypt.MinCost` (keep `go test -race` fast); the production dummy hash stays at `DefaultCost`

## 3. Infrastructure: user repository & seed

- [ ] 3.1 Add `UserRepository` in `api/internal/infrastructure/persistence/` over the sqlc `GetUserByEmail`, translating `pgx.ErrNoRows → identity.ErrUserNotFound`; satisfies `identity.Repository`
- [ ] 3.2 Add an idempotent dev-seed function (e.g. `SeedDevUser`) that hashes `AUTH_DEV_PASSWORD` with `bcrypt.DefaultCost` and runs `UpsertTenant` + `UpsertUser` from `DEV_TENANT_ID`/`DEV_USER_ID`/`lower(AUTH_DEV_EMAIL)`; never logs the password or hash
- [ ] 3.3 Integration test (testcontainers, `*_integration_test.go`): apply `0007`, seed, assert `GetUserByEmail` round-trip and that a second insert differing only by email case is rejected by `users_email_lower_key`

## 4. HTTP login rewire

- [ ] 4.1 Rewrite `api/internal/interfaces/http/auth_login.go`: drop `DevEmail/DevPassword/DevTenantID/DevUserID`; add an `Authenticator` field; `login` calls `Verify`, maps `ErrInvalidCredentials → 401 invalid_credentials`, issues the token for `user.TenantID`/`user.ID`, returns `data.user = {id, tenant_id, email}`; keep `400 invalid_input` for malformed/missing body; keep `auth_login_ok` log with the real `user_id`, password still never logged
- [ ] 4.2 Rewrite `auth_login_test.go` with a fake `Authenticator`: 200 envelope/shape, 401 `invalid_credentials` for both miss reasons, 400 `invalid_input` for bad body, and assert the password appears in no log attr
- [ ] 4.3 Update `auth_middleware_test.go:31-35`: its `AuthHandlers{...}` literal sets the removed `Dev*` fields and will fail to compile — replace with the new shape (a trivial/no-op `Authenticator`; that test exercises only the Bearer middleware on `_whoami`, never `/auth/login`)

## 5. Wiring & config

- [ ] 5.1 In `api/cmd/api/main.go` (`runServer` only — NOT `runMigrate`): construct the `UserRepository` from `queries`, build the `identity.Authenticator`, inject it into `AuthHandlers` (replacing the dev-principal fields); run `SeedDevUser` after pool init and after the conditional boot-migrate block. Map a Postgres `42P01` (undefined_table) seed failure to a legible `run 'api migrate up' first` boot error; fail boot on any other seed error
- [ ] 5.2 Update stale doc comments to the new seed-input model (no key renames): `config.go:65-78` (`AUTH_DEV_*`/`DEV_*` are **seed inputs**, keep `AUTH_DEV_PASSWORD` required + `AUTH_DEV_EMAIL` defaulted), `auth_login.go:15-18` (handler no longer compares config creds), and `main.go:198-199` ("verifies the configured dev credentials" → DB-backed lookup)

## 6. Gates

- [ ] 6.1 From `api/`: `go vet ./...`, `golangci-lint run ./...` (0 issues, gocritic strict incl. tests), `go test -race ./...`, `make sqlc` (no diff), `make test-integration`
- [ ] 6.2 `gofmt -w` only the touched files
- [ ] 6.3 `openspec validate add-api-user-store --strict` from repo root passes
