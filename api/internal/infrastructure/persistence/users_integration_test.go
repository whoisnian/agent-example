//go:build integration

// Integration tests for the identity user store — drives SeedDevUser and the
// sqlc user queries against a real postgres:18.4 container (migrations applied
// from disk, so 0007_init_identity runs). Run with: make test-integration
package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

func newUserPool(t *testing.T) (*pgxpool.Pool, *sqlc.Queries) {
	t.Helper()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, sqlc.New(pool)
}

func TestSeedDevUser_RoundTrip(t *testing.T) {
	pool, q := newUserPool(t)
	ctx := context.Background()
	tenantID, userID := mustUUID(t), mustUUID(t)
	const email, password = "Dev@Example.com", "correct-horse"

	if err := persistence.SeedDevUser(ctx, q, tenantID, userID, email, password); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Lookup is case-insensitive and the stored email is normalized to lowercase.
	row, err := q.GetUserByEmail(ctx, "dev@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if uuid.UUID(row.ID.Bytes) != userID || uuid.UUID(row.TenantID.Bytes) != tenantID {
		t.Errorf("ids mismatch: got user=%v tenant=%v", row.ID, row.TenantID)
	}
	if row.Email != "dev@example.com" {
		t.Errorf("stored email = %q, want normalized lowercase", row.Email)
	}
	// The stored hash verifies against the seed password and is not the plaintext.
	if row.PasswordHash == password {
		t.Error("password stored as plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)); err != nil {
		t.Errorf("stored hash does not verify: %v", err)
	}

	// Re-seeding with a changed password is idempotent (no dup row) and refreshes
	// the hash.
	if err := persistence.SeedDevUser(ctx, q, tenantID, userID, email, "new-password"); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("user count = %d, want 1 (idempotent upsert)", count)
	}
	row, _ = q.GetUserByEmail(ctx, "dev@example.com")
	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte("new-password")); err != nil {
		t.Errorf("re-seed did not refresh the hash: %v", err)
	}
}

func TestUsersEmailLowerKey_RejectsCaseDuplicate(t *testing.T) {
	pool, q := newUserPool(t)
	ctx := context.Background()
	tenantID, userID := mustUUID(t), mustUUID(t)

	if err := persistence.SeedDevUser(ctx, q, tenantID, userID, "alice@example.com", "pw"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A second user (distinct id, same tenant) whose email differs only by case
	// must be rejected by users_email_lower_key.
	_, err := pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash) VALUES ($1, $2, $3, $4)`,
		mustUUID(t), tenantID, "ALICE@example.com", "x",
	)
	if err == nil {
		t.Fatal("expected unique-violation on case-only email duplicate, got nil")
	}
}
