## ADDED Requirements

### Requirement: Persistent Identity Schema

The API SHALL persist identities in PostgreSQL via a `tenants` table and a `users` table, managed by a versioned golang-migrate migration. `users` SHALL carry at least `id` (UUID primary key), `tenant_id` (UUID, NOT NULL, referencing `tenants(id)`), `email` (TEXT, NOT NULL), `password_hash` (TEXT, NOT NULL), and creation/update timestamps. `tenants` SHALL carry at least `id` (UUID primary key) and a name.

Email uniqueness MUST be **case-insensitive**: a unique index over `lower(email)` SHALL prevent two users whose emails differ only by case. The `password_hash` column MUST hold a one-way password digest (bcrypt), never the plaintext password. The migration MUST provide a reversible `down` that drops both tables; no existing table is altered by this migration (the `tasks` → `users`/`tenants` foreign keys remain deferred).

#### Scenario: Migration creates the identity tables
- **WHEN** migration `0007` is applied to a database
- **THEN** the `tenants` and `users` tables MUST exist, `users.tenant_id` MUST reference `tenants(id)`, and a unique index over `lower(users.email)` MUST be present

#### Scenario: Case-insensitive email uniqueness is enforced
- **GIVEN** a user row exists with email `Dev@Example.com`
- **WHEN** a second user row is inserted with email `dev@example.com`
- **THEN** the insert MUST be rejected by the `lower(email)` unique index

#### Scenario: Down migration is clean
- **WHEN** migration `0007` is rolled back
- **THEN** the `tenants` and `users` tables MUST be dropped and no other table MUST be affected

### Requirement: Database-Backed Credential Verification

The API SHALL verify login credentials against the `users` table rather than an in-memory configured principal. Given a posted `{email, password}`, the verifier MUST normalize the email (lowercase) and look up the user by that normalized email; on a found user it MUST verify the password against the stored bcrypt hash; on success it MUST yield that user's `{id, tenant_id, email}` so the caller can issue a token for **that** principal.

Verification MUST remain indistinguishable and timing-safe: an unknown email and a wrong password MUST both resolve to a single `invalid-credentials` outcome with no indication of which field mismatched, and the unknown-email path MUST perform an equivalent bcrypt comparison (against a fixed dummy hash) so its timing matches the wrong-password path — there MUST NOT be an early return that skips hashing when the user is absent. The plaintext password and the stored hash MUST NEVER appear in a response body or any log field.

#### Scenario: Valid credentials resolve to the stored user's principal
- **GIVEN** a user row with email `alice@example.com` and a bcrypt hash of her password
- **WHEN** the verifier is asked to verify `{alice@example.com, <correct password>}` (any letter-casing of the email)
- **THEN** it MUST return that user with the row's `id` and `tenant_id`, suitable for issuing a token scoped to that principal

#### Scenario: Wrong password and unknown email are indistinguishable
- **WHEN** the verifier is asked to verify a wrong password for an existing email OR any password for an email with no matching user
- **THEN** both MUST resolve to the single `invalid-credentials` outcome, and the unknown-email path MUST still perform a bcrypt comparison against a dummy hash (no early return), so neither the result nor the timing reveals which field mismatched

#### Scenario: Secrets never surface
- **WHEN** verification succeeds or fails, and the request is logged
- **THEN** the plaintext password and the stored `password_hash` MUST NOT appear in the response body or any log field

### Requirement: Idempotent Dev User Seed

To bootstrap the store without a registration endpoint, the API SHALL, during server startup, idempotently upsert a development tenant and user from configuration: the tenant/user `id`s from `DEV_TENANT_ID`/`DEV_USER_ID`, the email from `AUTH_DEV_EMAIL` (normalized to lowercase), and `password_hash` computed as `bcrypt(AUTH_DEV_PASSWORD)`. The upsert MUST be keyed on the configured `id` so repeated boots converge and a changed configured password refreshes the stored hash. Because `AUTH_DEV_PASSWORD` remains required at startup, the build MUST ship no well-known weak login: the seed password is operator-supplied and never committed to the repository, and the hash (not the plaintext) is what reaches the database.

The seed MUST run in the server boot path only, never in the `api migrate` subcommand path (which intentionally requires only `DATABASE_URL`). The seed MUST NOT assume the boot-time auto-migrator ran (`DB_MIGRATE_ON_BOOT` defaults to off, the common path applying migrations out-of-band): it succeeds whenever the `users` table already exists, and when the table is absent it MUST fail startup with a legible error instructing the operator to apply migrations first, rather than surfacing a raw driver error.

#### Scenario: First boot seeds the dev principal
- **GIVEN** an empty `users` table and the configured `DEV_*` / `AUTH_DEV_*` values
- **WHEN** the API starts
- **THEN** a tenant row with `id = DEV_TENANT_ID` and a user row with `id = DEV_USER_ID`, `email = lower(AUTH_DEV_EMAIL)`, and a bcrypt hash of `AUTH_DEV_PASSWORD` MUST exist, and the user MUST be able to log in with `{AUTH_DEV_EMAIL, AUTH_DEV_PASSWORD}`

#### Scenario: Re-boot is idempotent and refreshes the password hash
- **GIVEN** the dev user already exists from a previous boot
- **WHEN** the API starts again, possibly with a changed `AUTH_DEV_PASSWORD`
- **THEN** no duplicate row MUST be created (upsert keyed on `id`), and the stored `password_hash` MUST reflect the current configured password

#### Scenario: Seed password is never the repository's
- **WHEN** the build is inspected and the API starts with `AUTH_DEV_PASSWORD` unset
- **THEN** no hard-coded credential MUST be shipped, and startup MUST fail naming the missing `AUTH_DEV_PASSWORD` (the seed has no built-in default password)

#### Scenario: Boot with migrations applied out-of-band seeds successfully
- **GIVEN** `DB_MIGRATE_ON_BOOT=false` and migration `0007` already applied via `api migrate up`
- **WHEN** the API server starts
- **THEN** the seed MUST upsert the dev tenant/user against the existing `users` table without requiring the boot-time auto-migrator

#### Scenario: Boot before any migration fails legibly
- **GIVEN** the `users` table does not yet exist (no migration applied)
- **WHEN** the API server starts and attempts the seed
- **THEN** startup MUST fail with a clear error instructing the operator to run migrations first (e.g. `api migrate up`), not a raw Postgres `undefined_table` error
