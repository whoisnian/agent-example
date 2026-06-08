package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/whoisnian/agent-example/api/internal/domain/identity"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// UserRepository adapts the sqlc-generated user queries to identity.Repository.
type UserRepository struct {
	q *sqlc.Queries
}

// NewUserRepository builds a UserRepository over the sqlc queries.
func NewUserRepository(q *sqlc.Queries) *UserRepository {
	return &UserRepository{q: q}
}

// FindByEmail resolves a user by normalized email, translating pgx.ErrNoRows
// (unknown email) into identity.ErrUserNotFound so the verifier collapses it to
// ErrInvalidCredentials. Any other error is a real infra failure and propagates.
func (r *UserRepository) FindByEmail(ctx context.Context, normalizedEmail string) (identity.User, error) {
	row, err := r.q.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.User{}, identity.ErrUserNotFound
		}
		return identity.User{}, err
	}
	return identity.User{
		ID:           uuid.UUID(row.ID.Bytes),
		TenantID:     uuid.UUID(row.TenantID.Bytes),
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
	}, nil
}

// ErrSchemaNotMigrated is returned by SeedDevUser when the users table does not
// exist yet (Postgres 42P01). The boot path turns this into an actionable
// "run 'api migrate up' first" message instead of a raw driver error.
var ErrSchemaNotMigrated = errors.New("identity schema not migrated")

// SeedDevUser idempotently upserts the development tenant + user from config:
// a tenant row keyed on tenantID and a user row keyed on userID whose
// password_hash is bcrypt(password). Re-running converges (UpsertUser refreshes
// email/hash). The plaintext password and the resulting hash are never logged.
// A missing users table (42P01) maps to ErrSchemaNotMigrated.
func SeedDevUser(ctx context.Context, q *sqlc.Queries, tenantID, userID uuid.UUID, email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash dev password: %w", err)
	}
	if err := q.UpsertTenant(ctx, sqlc.UpsertTenantParams{
		ID:   pgtype.UUID{Bytes: tenantID, Valid: true},
		Name: "dev",
	}); err != nil {
		return classifySeedErr(err)
	}
	if err := q.UpsertUser(ctx, sqlc.UpsertUserParams{
		ID:           pgtype.UUID{Bytes: userID, Valid: true},
		TenantID:     pgtype.UUID{Bytes: tenantID, Valid: true},
		Email:        identity.NormalizeEmail(email),
		PasswordHash: string(hash),
	}); err != nil {
		return classifySeedErr(err)
	}
	return nil
}

// classifySeedErr maps a missing-table error to ErrSchemaNotMigrated; all other
// errors pass through unchanged.
func classifySeedErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
		return ErrSchemaNotMigrated
	}
	return err
}
