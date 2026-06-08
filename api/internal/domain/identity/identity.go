// Package identity is the API's user-identity domain: the User entity, a
// Repository port over the persistent users table, and the Authenticator that
// verifies login credentials.
//
// The Authenticator replaces the former in-memory dev-principal comparison
// (add-api-user-store): it looks a user up by normalized email and checks the
// posted password against the stored bcrypt hash. It upholds the api-auth
// contract — an unknown email and a wrong password are INDISTINGUISHABLE
// (both collapse to ErrInvalidCredentials) and TIMING-SAFE: the unknown-email
// branch still performs a bcrypt comparison against a fixed dummy hash so it is
// not measurably faster than the wrong-password branch.
package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ErrUserNotFound is returned by a Repository when no user matches the email.
// It never escapes the Authenticator — Verify collapses it to
// ErrInvalidCredentials so callers cannot distinguish it from a bad password.
var ErrUserNotFound = errors.New("user not found")

// ErrInvalidCredentials is the single sentinel for a failed login (unknown
// email OR wrong password). The handler maps it to 401 invalid_credentials.
var ErrInvalidCredentials = errors.New("invalid credentials")

// User is an authenticated identity. PasswordHash is a bcrypt digest and MUST
// never be logged or serialized to a response.
type User struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Email        string
	PasswordHash string
}

// Repository resolves users by their normalized (lowercased) email. A miss MUST
// be reported as ErrUserNotFound, not a zero User with nil error.
type Repository interface {
	FindByEmail(ctx context.Context, normalizedEmail string) (User, error)
}

// dummyHash is a fixed bcrypt digest (cost = bcrypt.DefaultCost) compared
// against on the unknown-email path so that "no such user" costs the same as
// "wrong password". It is a constant (not generated at init) so package load
// stays free of a hashing cost while preserving the cost-10 timing.
const dummyHash = "$2a$10$o0J4PAzYu2ecAaulauugd.OO1F1vbu1jTXRUudfUKT86BTpiD.Kiu"

// Authenticator verifies credentials against the user Repository.
type Authenticator struct {
	repo Repository
}

// NewAuthenticator builds an Authenticator over the given Repository.
func NewAuthenticator(repo Repository) *Authenticator {
	return &Authenticator{repo: repo}
}

// Verify normalizes the email, looks up the user, and checks the password
// against the stored bcrypt hash. It returns the user on success, or
// ErrInvalidCredentials for an unknown email OR a wrong password (the two are
// indistinguishable). A non-not-found repository error (e.g. a DB outage) is
// propagated unchanged so the caller can return a 500 rather than a misleading
// 401. NOTE: bcrypt silently truncates the password at 72 bytes — acceptable
// for MVP dev credentials.
func (a *Authenticator) Verify(ctx context.Context, email, password string) (User, error) {
	normalized := NormalizeEmail(email)
	user, err := a.repo.FindByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Equalize timing with the wrong-password path: run a comparison
			// against the dummy hash and discard the result.
			_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// NormalizeEmail lowercases and trims the email so the lookup key agrees with
// the users_email_lower_key index and the seeded row. It is the single
// normalization point shared by Verify and the dev-user seed.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
