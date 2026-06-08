-- Reverse 0007_init_identity. Drop users (which holds the FK) before tenants.
-- No other table references these, so the teardown is clean.

BEGIN;

DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;

COMMIT;
