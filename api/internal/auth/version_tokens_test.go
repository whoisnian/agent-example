package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArchiveAndPreviewRoundTrip(t *testing.T) {
	vid := uuid.New()
	iss := NewDownloadIssuer(testSecret, 5*time.Minute)
	v := NewDownloadVerifier(testSecret)

	archTok, _, err := iss.IssueArchive(vid)
	if err != nil {
		t.Fatalf("issue archive: %v", err)
	}
	if err := v.ParseArchive(archTok, vid); err != nil {
		t.Errorf("ParseArchive round-trip: %v", err)
	}

	prevTok, _, err := iss.IssuePreview(vid)
	if err != nil {
		t.Fatalf("issue preview: %v", err)
	}
	if err := v.ParsePreview(prevTok, vid); err != nil {
		t.Errorf("ParsePreview round-trip: %v", err)
	}
}

// Each verifier path requires exactly its own audience: a token of one kind is
// rejected by every other kind's verifier (download / archive / preview), and
// the access-token verifier rejects all of them (they carry an aud).
func TestVersionTokenAudienceIsolation(t *testing.T) {
	vid := uuid.New()
	iss := NewDownloadIssuer(testSecret, 5*time.Minute)
	v := NewDownloadVerifier(testSecret)
	access := NewVerifier(testSecret)

	dl, _, _ := iss.Issue(vid)
	arch, _, _ := iss.IssueArchive(vid)
	prev, _, _ := iss.IssuePreview(vid)

	// Archive verifier accepts only archive tokens.
	if err := v.ParseArchive(dl, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParseArchive accepted a download token")
	}
	if err := v.ParseArchive(prev, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParseArchive accepted a preview token")
	}
	// Preview verifier accepts only preview tokens.
	if err := v.ParsePreview(dl, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParsePreview accepted a download token")
	}
	if err := v.ParsePreview(arch, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParsePreview accepted an archive token")
	}
	// Download verifier rejects the version-scoped kinds.
	if err := v.Parse(arch, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("download Parse accepted an archive token")
	}
	if err := v.Parse(prev, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("download Parse accepted a preview token")
	}
	// Access-token verifier rejects every artifact token (all carry an aud).
	for _, tok := range []string{dl, arch, prev} {
		if _, err := access.Parse(tok); !errors.Is(err, ErrInvalidToken) {
			t.Error("access verifier accepted an artifact token")
		}
	}
}

func TestVersionTokenRejectsExpiredAndCrossVersion(t *testing.T) {
	vid := uuid.New()
	v := NewDownloadVerifier(testSecret)

	expired, _, _ := NewDownloadIssuer(testSecret, -time.Hour).IssueArchive(vid)
	if err := v.ParseArchive(expired, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParseArchive accepted an expired token")
	}
	otherVid, _, _ := NewDownloadIssuer(testSecret, 5*time.Minute).IssuePreview(uuid.New())
	if err := v.ParsePreview(otherVid, vid); !errors.Is(err, ErrInvalidToken) {
		t.Error("ParsePreview accepted a token minted for a different version")
	}
}
