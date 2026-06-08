package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// fakeRepo is an in-memory Repository keyed on normalized (lowercased) email.
type fakeRepo struct {
	byEmail map[string]User
	err     error // when non-nil, FindByEmail returns it (infra-error path)
}

func (f *fakeRepo) FindByEmail(_ context.Context, normalizedEmail string) (User, error) {
	if f.err != nil {
		return User{}, f.err
	}
	u, ok := f.byEmail[normalizedEmail]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return u, nil
}

// hashAtMinCost keeps the test suite fast (DefaultCost is ~50-100ms/compare);
// the production dummy hash stays at DefaultCost so prod timing-equivalence holds.
func hashAtMinCost(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return string(h)
}

func TestVerify_ValidCredentials(t *testing.T) {
	t.Parallel()
	want := User{ID: uuid.New(), TenantID: uuid.New(), Email: "alice@example.com", PasswordHash: hashAtMinCost(t, "s3cret")}
	a := NewAuthenticator(&fakeRepo{byEmail: map[string]User{"alice@example.com": want}})

	got, err := a.Verify(context.Background(), "alice@example.com", "s3cret")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ID != want.ID || got.TenantID != want.TenantID {
		t.Errorf("got %+v, want id=%s tenant=%s", got, want.ID, want.TenantID)
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	t.Parallel()
	u := User{ID: uuid.New(), TenantID: uuid.New(), Email: "alice@example.com", PasswordHash: hashAtMinCost(t, "s3cret")}
	a := NewAuthenticator(&fakeRepo{byEmail: map[string]User{"alice@example.com": u}})

	if _, err := a.Verify(context.Background(), "alice@example.com", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err=%v, want ErrInvalidCredentials", err)
	}
}

func TestVerify_UnknownEmailIsInvalidCredentials(t *testing.T) {
	t.Parallel()
	// Empty repo → every lookup misses. The dummy-hash compare runs (no early
	// return) and the result collapses to the same sentinel as a wrong password.
	a := NewAuthenticator(&fakeRepo{byEmail: map[string]User{}})
	if _, err := a.Verify(context.Background(), "nobody@example.com", "whatever"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err=%v, want ErrInvalidCredentials", err)
	}
}

func TestVerify_EmailNormalization(t *testing.T) {
	t.Parallel()
	// Row stored under the lowercased key; a mixed-case + padded login finds it.
	u := User{ID: uuid.New(), TenantID: uuid.New(), Email: "alice@example.com", PasswordHash: hashAtMinCost(t, "s3cret")}
	a := NewAuthenticator(&fakeRepo{byEmail: map[string]User{"alice@example.com": u}})

	if _, err := a.Verify(context.Background(), "  Alice@Example.COM  ", "s3cret"); err != nil {
		t.Errorf("mixed-case/padded login did not resolve: %v", err)
	}
}

func TestVerify_InfraErrorPropagates(t *testing.T) {
	t.Parallel()
	// A non-not-found repo error must NOT be masked as invalid credentials —
	// the handler needs to return 500, not a misleading 401.
	boom := errors.New("db down")
	a := NewAuthenticator(&fakeRepo{err: boom})
	_, err := a.Verify(context.Background(), "alice@example.com", "s3cret")
	if errors.Is(err, ErrInvalidCredentials) || !errors.Is(err, boom) {
		t.Errorf("err=%v, want propagated infra error (not ErrInvalidCredentials)", err)
	}
}
