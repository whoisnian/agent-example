package persistence

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // register pg driver
	_ "github.com/golang-migrate/migrate/v4/source/file"      // register file source
)

// Migrator wraps golang-migrate. Construct via NewMigrator and dispose with Close.
type Migrator struct {
	m *migrate.Migrate
}

// NewMigrator builds a migrator for the given migrations directory (a file://
// URL) and database URL. golang-migrate handles its own connection — we do
// NOT share the pgxpool here because the driver wants database/sql.
func NewMigrator(migrationsDir, databaseURL string) (*Migrator, error) {
	sourceURL := "file://" + migrationsDir
	m, err := migrate.New(sourceURL, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return &Migrator{m: m}, nil
}

// Up applies all up-migrations. ErrNoChange is treated as a no-op success.
func (m *Migrator) Up() error {
	if err := m.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down rolls back one migration step.
func (m *Migrator) Down() error {
	if err := m.m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Version returns the current applied version and dirty flag.
func (m *Migrator) Version() (version uint, dirty bool, err error) {
	v, d, err := m.m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, fmt.Errorf("migrate version: %w", err)
	}
	return v, d, nil
}

// Force sets the migration version manually (used to recover from dirty state).
func (m *Migrator) Force(version int) error {
	if err := m.m.Force(version); err != nil {
		return fmt.Errorf("migrate force %d: %w", version, err)
	}
	return nil
}

// Close releases the migrator's source and database connections.
func (m *Migrator) Close() error {
	srcErr, dbErr := m.m.Close()
	switch {
	case srcErr != nil && dbErr != nil:
		return fmt.Errorf("migrate close: source=%w db=%v", srcErr, dbErr) //nolint:errorlint // multiple errors; %w wraps source only
	case srcErr != nil:
		return fmt.Errorf("migrate close source: %w", srcErr)
	case dbErr != nil:
		return fmt.Errorf("migrate close db: %w", dbErr)
	}
	return nil
}
