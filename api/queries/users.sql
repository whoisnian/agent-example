-- name: GetUserByEmail :one
-- Credential lookup for POST /auth/login. Matched case-insensitively against
-- the users_email_lower_key index; the caller passes an already-lowercased
-- email but lower($1) keeps the predicate aligned with the index regardless.
-- Returns no rows for an unknown email; the repository maps pgx.ErrNoRows to
-- identity.ErrUserNotFound (collapsed to ErrInvalidCredentials by the verifier).
SELECT *
FROM users
WHERE lower(email) = lower($1);

-- name: UpsertTenant :exec
-- Idempotent dev-seed of the tenant. Keyed on id so repeated boots converge;
-- the name is left untouched on conflict (the seed only guarantees existence).
INSERT INTO tenants (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING;

-- name: UpsertUser :exec
-- Idempotent dev-seed of the user. Keyed on id so repeated boots converge and a
-- changed configured password refreshes the stored hash. password_hash holds a
-- bcrypt digest — never the plaintext.
INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    tenant_id     = EXCLUDED.tenant_id,
    email         = EXCLUDED.email,
    password_hash = EXCLUDED.password_hash,
    updated_at    = now();
