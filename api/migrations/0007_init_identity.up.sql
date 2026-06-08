-- 0007_init_identity: tenants + users, the persistent identity store.
--
-- Capability: api-user-store (add-api-user-store).
-- Replaces the config-seeded dev-principal comparison at POST /auth/login with
-- a real lookup. Key invariants enforced here:
--   * Case-insensitive email uniqueness via UNIQUE INDEX over lower(email)
--     (no citext extension — keeps migrations portable / unprivileged).
--   * users.tenant_id REFERENCES tenants(id) — a real FK WITHIN the new tables.
-- The tasks.tenant_id / tasks.user_id FKs remain deferred (see the 0002 note);
-- this migration touches no existing table.

BEGIN;

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

-- Case-insensitive uniqueness: two emails differing only by case collide.
-- The application normalizes email to lowercase before insert AND lookup so
-- the stored value agrees with this index expression.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

COMMIT;
