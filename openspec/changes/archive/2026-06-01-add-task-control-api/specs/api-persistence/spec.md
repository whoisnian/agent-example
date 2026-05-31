## MODIFIED Requirements

### Requirement: Outbox Table Schema

The initial migration set MUST create the `outbox` table conforming to `docs/ARCHITECTURE.md §4.2` (columns: `id BIGSERIAL`, `aggregate TEXT`, `aggregate_id UUID`, `topic TEXT`, `payload JSONB`, `status TEXT DEFAULT 'pending'`, `attempts INT DEFAULT 0`, `next_retry_at TIMESTAMPTZ`, `created_at TIMESTAMPTZ DEFAULT now()`), plus an index `(status, next_retry_at)` to support relayer scans.

A subsequent migration `0006_outbox_exchange` (shipped by `add-task-control-api`) MUST add an `exchange TEXT NOT NULL DEFAULT 'task.exchange'` column so the Outbox Relayer can route each row to its destination exchange (see `api-messaging` §"Outbox Relayer"). The up migration MUST use `ADD COLUMN IF NOT EXISTS` so a re-run after the down step is a no-op. The default makes the migration safe for existing rows — every previously-written row had been routed to `task.exchange` implicitly by the relayer's old constant.

`outbox.topic` continues to mean "routing key on the row's exchange"; the column's name is preserved for backwards compatibility with already-applied migrations. Callers MUST always supply `exchange` explicitly when inserting new rows; the `DEFAULT` exists only to handle the schema-evolution case.

The `.down.sql` for `0006_outbox_exchange` SHALL be **a no-op** (forward-only schema evolution). Dropping the `exchange` column after `'task.control'` rows have been written would silently re-route those rows to `'task.exchange'` on a subsequent re-up, which is worse than leaving the harmless `NOT NULL DEFAULT` column in place. Operators who genuinely need to delete the column must do so out-of-band; this is a deliberate design trade-off documented in the change's design D-Migration Plan and reviewer S6.

#### Scenario: Outbox table is created with the expected columns
- **WHEN** the migration set is applied to a fresh database
- **THEN** the `outbox` table MUST exist with all required columns, and the `(status, next_retry_at)` index MUST be present

#### Scenario: Exchange column exists after migration
- **WHEN** migration `0006_outbox_exchange.up.sql` has been applied
- **THEN** `outbox` MUST have a column `exchange TEXT NOT NULL DEFAULT 'task.exchange'`, AND every existing row's `exchange` value MUST equal `'task.exchange'`

#### Scenario: Down migration is a no-op
- **WHEN** `0006_outbox_exchange.down.sql` is applied (e.g., `migrate -1`)
- **THEN** the `exchange` column MUST still be present, AND no rows MUST have been modified or dropped, AND `schema_migrations.version` MUST decrement so a subsequent `migrate up` re-runs the `0006_outbox_exchange.up.sql` (which is itself a no-op due to `ADD COLUMN IF NOT EXISTS`)

#### Scenario: Up→down→up sequence is idempotent
- **WHEN** `0006_outbox_exchange.up.sql` then `.down.sql` then `.up.sql` is run against a database (fresh or populated)
- **THEN** the final schema MUST have the `exchange` column, AND `schema_migrations.dirty` MUST be `false` at each step, AND no `outbox` row's `exchange` value MUST have changed across the sequence
