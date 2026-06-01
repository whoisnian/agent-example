package oss

import (
	"context"
	"strings"
	"testing"
	"time"
)

// PresignGetObject signs locally (no network), so we can assert URL scoping and
// the advisory expiry against an injected clock.
func TestPresignGet_SingleObjectAndExpiry(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	c := New(&Config{
		Endpoint:        "https://oss.example.com",
		Region:          "us-east-1",
		Bucket:          "artifacts",
		AccessKeyID:     "key",
		AccessKeySecret: "secret",
		UsePathStyle:    true,
		PresignTTL:      5 * time.Minute,
	})
	c.now = func() time.Time { return fixed }

	url, expiresAt, err := c.PresignGet(context.Background(), "t/v/file/index.md")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	want := fixed.Add(5 * time.Minute)
	if !expiresAt.Equal(want) {
		t.Errorf("expiresAt=%v, want now+TTL=%v", expiresAt, want)
	}
	// Path-style, single object: /<bucket>/<key> with a bounded expiry.
	if !strings.Contains(url, "/artifacts/t/v/file/index.md") {
		t.Errorf("url does not target the single object key: %s", url)
	}
	if !strings.Contains(url, "X-Amz-Expires=300") {
		t.Errorf("url missing the 300s expiry: %s", url)
	}
	// The secret is used to sign but must never appear verbatim in the URL.
	if strings.Contains(url, "secret") {
		t.Errorf("secret leaked into url: %s", url)
	}
}

// A non-UTC injected clock must still yield a UTC expires_at.
func TestPresignGet_ExpiresAtIsUTC(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC+8", 8*3600)
	c := New(&Config{
		Endpoint: "https://oss.example.com", Region: "us-east-1", Bucket: "b",
		AccessKeyID: "k", AccessKeySecret: "s", UsePathStyle: true, PresignTTL: time.Minute,
	})
	c.now = func() time.Time { return time.Date(2026, 6, 2, 20, 0, 0, 0, loc) }

	_, expiresAt, err := c.PresignGet(context.Background(), "k")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if expiresAt.Location() != time.UTC {
		t.Errorf("expiresAt location=%v, want UTC", expiresAt.Location())
	}
}
