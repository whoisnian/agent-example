package task

import (
	"time"

	"github.com/google/uuid"
)

// Clock returns "now". Service depends on the interface so unit tests can
// inject a frozen time and assert payload fields (`deadline_ts`, etc.).
type Clock interface {
	Now() time.Time
}

// IDGenerator mints UUIDv7 ids. The Service uses this for every server-side
// id (task / version / run / msg_id). Tests inject deterministic generators
// to assert the payload builder output byte-for-byte.
type IDGenerator interface {
	NewV7() (uuid.UUID, error)
}

// SystemClock is the default production implementation.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// UUIDv7Gen is the default production implementation. It wraps
// `uuid.NewV7()` from github.com/google/uuid.
type UUIDv7Gen struct{}

func (UUIDv7Gen) NewV7() (uuid.UUID, error) { return uuid.NewV7() }
