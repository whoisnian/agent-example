## Context

`add-api-auth-jwt` made the API's identity real on the *token* side: the Bearer middleware and WS handshake resolve a `Principal{tenant_id, user_id}` from JWT claims, and every business handler owner-scopes off that principal (`PrincipalFromContext`). But the *credential* side is still a stub: `POST /api/v1/auth/login` (`api/internal/interfaces/http/auth_login.go`) compares the posted `{email, password}` constant-time against `AuthHandlers.DevEmail`/`DevPassword` and, on success, issues a token for the fixed `DevTenantID`/`DevUserID`. There is no `tenants`/`users` table — the `0002` migration deliberately left `tasks.tenant_id`/`user_id` FK-less with the comment *"auth + multi-tenant proposals add the FK constraints via ALTER."*

The web client (`add-web-auth-login`) already POSTs real credentials and stores the returned `user = {id, tenant_id, email}`. So the only thing standing between the platform and genuine multi-user identity is a persistent user store. This change adds it with the minimum surface that keeps the existing login contract byte-for-byte while moving the credential source from process memory to PostgreSQL.

Constraints carried in from the existing system:
- Login's security contract (api-auth spec): unknown-email and wrong-password are **indistinguishable** (`401 invalid_credentials`), credential verification is **constant-time** w.r.t. the password, and the password never appears in a response body or log field.
- AGENTS §4.1 layering: `interfaces/ ↔ application/ ↔ domain/ ↔ infrastructure/`; SQL via sqlc only (no hand-rolled SQL in handlers); migrations via golang-migrate under `api/migrations/`.
- AGENTS §6 red lines: no well-known weak login shipped (today enforced by `AUTH_DEV_PASSWORD required:"true"`); secrets never logged.

## Goals / Non-Goals

**Goals:**
- A persistent `tenants` + `users` schema with bcrypt password hashes and case-insensitive unique email.
- DB-backed credential verification at login, issuing a token for the *looked-up* user's `(tenant_id, user_id)`.
- Preserve the login contract exactly (status codes, error codes, indistinguishability, constant-time, no secret logging).
- Keep existing dev workflows working via an idempotent boot-time seed driven by the *current* `AUTH_DEV_*` / `DEV_*` config — so no operator action is required and no weak password is baked into the repo.

**Non-Goals:**
- User registration / self-service signup, password reset/change, admin user CRUD (Post-MVP).
- Adding `tasks.* → users/tenants` FK constraints (deferred — see Decision 5).
- Roles / RBAC / permissions beyond the existing owner-scoping (`tenant_id, user_id`).
- Refresh tokens, "remember me", SSO (already out of scope per add-web-auth-login).

## Decisions

### Decision 1 — Schema: `tenants` and `users` in a new migration `0007`

```sql
CREATE TABLE tenants (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id),
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Case-insensitive uniqueness without the citext extension.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
```

- `users.tenant_id REFERENCES tenants(id)` — a real FK *within* the new tables (no chicken-and-egg, both created together).
- **Email uniqueness is case-insensitive** via `UNIQUE INDEX ... (lower(email))` rather than the `citext` extension, to avoid a `CREATE EXTENSION` dependency (matching the project's preference for plain SQL). The application normalizes email to lowercase before both insert and lookup so the stored value and the lookup key agree with the index.
- `password_hash` is a bcrypt digest string (`$2a$...`), never the raw password.
- **Alternative considered:** `citext` column — rejected to avoid the extension and keep migrations portable across the testcontainers / prod Postgres without a privileged `CREATE EXTENSION`.

### Decision 2 — Password hashing: `golang.org/x/crypto/bcrypt`

bcrypt with `bcrypt.DefaultCost`. `bcrypt.CompareHashAndPassword` is constant-time *with respect to the password* for a given hash, which satisfies the api-auth requirement. The cost factor is fixed (not configurable) for MVP.

- **Alternative considered:** argon2id (`golang.org/x/crypto/argon2`) — stronger, but needs explicit parameter/encoding management and a parsing layer. bcrypt's self-describing hash and stdlib-adjacent helper is the lower-risk MVP choice; the hash column can hold any scheme later.

### Decision 3 — Indistinguishable unknown-email vs wrong-password (timing-safe)

The handler must not leak — via response *or* timing — whether the email existed. bcrypt comparison only runs when a row is found, so a naive "user not found → return early" path is measurably faster than the wrong-password path. Mitigation: when the lookup misses, the verifier compares the supplied password against a **fixed dummy bcrypt hash** (a package-level constant computed once over a throwaway password) and discards the result, then returns the same `ErrInvalidCredentials`. Both branches therefore perform exactly one bcrypt comparison.

- The single sentinel `ErrInvalidCredentials` is returned for *both* unknown-email and bad-password; the handler maps it to `401 invalid_credentials` with no field indication, exactly as today.
- Repository "no rows" (`pgx.ErrNoRows`) is mapped to a `ErrUserNotFound` inside the repo and collapsed to `ErrInvalidCredentials` by the verifier — the handler never branches on which.

### Decision 4 — Layering: `domain/identity` + infrastructure repo, called from the login handler

Mirror the task slice's shape:
- `domain/identity/` — `User` entity (`{ID, TenantID, Email, PasswordHash}`), a `Repository` port (`FindByEmail(ctx, normalizedEmail) (User, error)` returning `ErrUserNotFound`), an `Authenticator`/credential-verification service (`Verify(ctx, email, password) (User, error)`) that normalizes email, calls the repo, runs the bcrypt compare (with the dummy-hash fallback), and returns the user or `ErrInvalidCredentials`. Pure, unit-testable with a fake repo.
- `infrastructure/persistence/` — a `UserRepository` over sqlc-generated queries (`GetUserByEmail`), translating `pgx.ErrNoRows → identity.ErrUserNotFound`. Lives beside the existing `OutboxStore`.
- `interfaces/http/auth_login.go` — `AuthHandlers` drops `DevEmail/DevPassword/DevTenantID/DevUserID` and gains an `Authenticator` (the identity service) + the existing `Issuer`. On `Verify` success it issues a token for `user.TenantID`/`user.ID` and returns `user = {id, tenant_id, email}`. The `auth_login_ok` log keeps `user_id` (now the real one); the password is still never logged.

This keeps SQL out of the handler (AGENTS §4.1) and the verification logic unit-testable independent of HTTP and Postgres.

### Decision 5 — Defer the `tasks → users/tenants` FK constraints

Adding `tasks.tenant_id → tenants(id)` / `tasks.user_id → users(id)` is the natural "close the loop," and the `0002` comment anticipates it. But it creates a deploy-ordering trap: an existing dev DB may already hold `tasks` rows referencing the dev ids before the seed inserts the matching `users` row, so an `ALTER ... ADD CONSTRAINT` at migration time would fail. Resolving that cleanly (seed-in-migration vs boot-seed ordering, backfill for orphans) is more than this change should carry. **Deferred to a dedicated follow-up**; the schema comment in `0002` stays accurate. Owner-scoping does not depend on the FK — it is enforced by query predicates already.

### Decision 6 — Bootstrap via an idempotent boot-time dev seed (keep `AUTH_DEV_*` / `DEV_*` as seed inputs)

With no registration endpoint, the store needs an initial user. Rather than baking a hash into a seed migration (which would ship a known password — violates §6), the API **seeds at boot**: after migrations, it idempotently upserts the dev tenant and user from config:

```sql
-- seed tenant
INSERT INTO tenants (id, name) VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING;
-- seed/update user
INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
  SET tenant_id = EXCLUDED.tenant_id,
      email = EXCLUDED.email,
      password_hash = EXCLUDED.password_hash,
      updated_at = now();
```

- `id`s come from `DEV_TENANT_ID` / `DEV_USER_ID`, email from `AUTH_DEV_EMAIL` (lowercased), `password_hash = bcrypt(AUTH_DEV_PASSWORD)`. Because `AUTH_DEV_PASSWORD` stays `required:"true"`, the build still ships **no well-known weak login**, and the password lives only in the operator's env → hash in DB (never the repo).
- Idempotent on the configured `id`, so repeated boots and a changed config password both converge (the `DO UPDATE` refreshes email/hash).
- The config keys keep their names and required-ness; only their *role* changes (runtime comparison → seed input). This makes the change a drop-in for existing dev/compose setups.

**Placement and ordering (resolves the `DB_MIGRATE_ON_BOOT` hazard).** The boot migrator at `main.go:111` runs **only** when `DB_MIGRATE_ON_BOOT=true` (default `false` — `config.go:39`); the common production path applies migrations out-of-band via `api migrate up` and boots with the flag off. So the seed must NOT assume the boot migrator ran:
- The seed runs in **`runServer` only** (after pool init, and after the conditional boot-migrate block so the dev `DB_MIGRATE_ON_BOOT=true` path is one coherent "migrate + seed" mode). It MUST NOT run in `runMigrate` (`main.go:447`), which deliberately builds a `DATABASE_URL`-only config and tolerates missing `AUTH_DEV_PASSWORD`/`OSS_*`/`RABBITMQ_URL` so `api migrate up` needs no app secrets — folding the seed in would break that contract.
- The seed assumes the schema exists (same assumption every other query makes), but a boot before any migration must fail *legibly*: if the upsert returns Postgres `42P01` (undefined_table), the API MUST fail boot with a clear `users table not found — run 'api migrate up' first` message rather than a raw driver error. This keeps the "boot with `DB_MIGRATE_ON_BOOT=false` and migrations already applied externally" path working (the table exists → seed succeeds) while turning the misordered-boot mistake into an actionable error.
- **Email-collision edge:** if `AUTH_DEV_EMAIL` collides by `lower(email)` with an existing row whose `id ≠ DEV_USER_ID`, the `id`-keyed `UpsertUser` attempts an INSERT that violates `users_email_lower_key`; the boot fails with that unique-violation. This is acceptable for a single-seed dev path — document the operator-facing meaning (pick a `DEV_USER_ID` matching the existing row, or a distinct email) so it is not later mistaken for a bug.
- **Alternative considered:** a CLI `api seed-user` subcommand or a seed migration — rejected for MVP: the boot seed needs zero extra operator steps and keeps the existing one-command dev start working. A CLI is the natural Post-MVP path once registration exists.

### Decision 7 — Test strategy

- **Domain unit tests** (`domain/identity`): fake repo; assert (a) valid creds → user; (b) wrong password → `ErrInvalidCredentials`; (c) unknown email → `ErrInvalidCredentials` *and* the dummy-hash compare path runs (no early return); (d) email normalization (mixed-case login finds a lowercased row). Test fixtures MUST hash at `bcrypt.MinCost` to keep `go test -race ./...` fast (a `DefaultCost` compare is ~50–100 ms each); the verifier's production dummy-hash constant stays at `DefaultCost` so the prod timing-equivalence property holds.
- **HTTP test** (`auth_login_test.go`, rewritten): inject a fake `Authenticator`; assert 200 shape `{token, expires_at, user}`, 401 `invalid_credentials` for both miss reasons, 400 `invalid_input` for malformed body, and password absent from any log attr.
- **Integration test** (testcontainers, matching existing `*_integration_test.go`): run migration `0007`, seed a user, exercise `GetUserByEmail` + a full login round-trip; assert `lower(email)` uniqueness rejects a dup with different case.
- `make sqlc` must produce no diff; `go vet` / `golangci-lint` (gocritic strict) / `go test -race` green.

## Risks / Trade-offs

- **[Timing leak via the not-found path]** → fixed dummy-hash bcrypt compare on the miss branch so both branches do exactly one comparison (Decision 3); covered by a domain test.
- **[Boot seed could mask a real misconfig]** (e.g. operator expects a different user) → seed is keyed on the explicit `DEV_USER_ID`/`DEV_TENANT_ID` and `DO UPDATE`s email/hash, so the configured identity is authoritative; log an `auth_dev_user_seeded` info line (no secrets) for visibility.
- **[`lower(email)` index vs app normalization drift]** → normalize in exactly one place (the identity service, before both seed-upsert and lookup) so the stored value and the index expression always agree; integration test asserts case-insensitive uniqueness.
- **[bcrypt 72-byte truncation]** → documented stdlib behavior; acceptable for MVP dev credentials. Note in the verifier doc comment.
- **[Deferred FK lets an orphaned `tasks.user_id` exist]** → owner-scoping is by query predicate, not referential integrity, so correctness holds; the follow-up change adds the constraint with a backfill.

## Migration Plan

1. Ship migration `0007_init_identity` (up creates `tenants`/`users` + the `lower(email)` unique index; down drops them).
2. Deploy the API with the boot seed enabled; on first boot the dev tenant/user are upserted from existing `AUTH_DEV_*`/`DEV_*` config — **no config changes required**.
3. Rollback: revert the binary; run `0007` down. Because no other table FKs the new ones, the down migration is clean. Existing `tasks` rows are untouched throughout.

## Open Questions

- None blocking. (Roles/permissions, registration, and the `tasks → users` FK are explicitly deferred to named follow-ups.)
