package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// fakeArtifactQuerier implements only the four sqlc.Querier methods the
// artifact read service calls. Embedding sqlc.Querier means any OTHER method is
// a nil-interface call that panics — a loud signal if the service starts
// touching the DB in an unexpected way.
type fakeArtifactQuerier struct {
	sqlc.Querier

	version    sqlc.TaskVersion
	versionErr error
	task       sqlc.Task
	taskErr    error
	artifacts  []sqlc.Artifact
	artifsErr  error
	artifact   sqlc.GetArtifactWithOwnerRow
	artifErr   error
}

func (f *fakeArtifactQuerier) GetTaskVersionByID(context.Context, pgtype.UUID) (sqlc.TaskVersion, error) {
	return f.version, f.versionErr
}
func (f *fakeArtifactQuerier) GetTaskByID(context.Context, pgtype.UUID) (sqlc.Task, error) {
	return f.task, f.taskErr
}
func (f *fakeArtifactQuerier) ListArtifactsByVersion(context.Context, pgtype.UUID) ([]sqlc.Artifact, error) {
	return f.artifacts, f.artifsErr
}
func (f *fakeArtifactQuerier) GetArtifactWithOwner(context.Context, pgtype.UUID) (sqlc.GetArtifactWithOwnerRow, error) {
	return f.artifact, f.artifErr
}

// fakePresigner records the key it was asked to sign and returns canned values.
type fakePresigner struct {
	called  bool
	gotKey  string
	url     string
	expires time.Time
	err     error
}

func (p *fakePresigner) PresignGet(_ context.Context, key string) (string, time.Time, error) {
	p.called = true
	p.gotKey = key
	return p.url, p.expires, p.err
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
func strptr(s string) *string       { return &s }
func i64ptr(n int64) *int64         { return &n }

var (
	testTenant = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	testUser   = uuid.MustParse("00000000-0000-0000-0000-0000000000a2")
	testOwner  = Owner{TenantID: testTenant, UserID: testUser}
)

// ownedTaskRow builds a tasks row owned by testOwner.
func ownedTaskRow() sqlc.Task {
	return sqlc.Task{TenantID: pgUUID(testTenant), UserID: pgUUID(testUser)}
}

func TestListVersionArtifacts_OwnedOrderingAndNullables(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	a1, a2 := uuid.New(), uuid.New()
	q := &fakeArtifactQuerier{
		version: sqlc.TaskVersion{ID: pgUUID(versionID), TaskID: pgUUID(uuid.New())},
		task:    ownedTaskRow(),
		// The SQL orders created_at ASC, id ASC; the fake returns them already
		// ordered, and we assert the service preserves that order.
		artifacts: []sqlc.Artifact{
			{ID: pgUUID(a1), Kind: "file", Mime: strptr("text/markdown"), Bytes: i64ptr(1024), Sha256: strptr("deadbeef")},
			{ID: pgUUID(a2), Kind: "file", Mime: nil, Bytes: nil, Sha256: nil},
		},
	}
	svc := NewArtifactReadService(q, &fakePresigner{})

	out, err := svc.ListVersionArtifacts(context.Background(), testOwner, versionID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.VersionID != versionID {
		t.Errorf("VersionID=%v, want %v", out.VersionID, versionID)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("len=%d, want 2", len(out.Artifacts))
	}
	if out.Artifacts[0].ID != a1 || out.Artifacts[1].ID != a2 {
		t.Errorf("ordering not preserved: %v then %v", out.Artifacts[0].ID, out.Artifacts[1].ID)
	}
	if out.Artifacts[0].Kind != "file" {
		t.Errorf("Kind=%q, want file (worker writes only this)", out.Artifacts[0].Kind)
	}
	if out.Artifacts[0].Mime == nil || *out.Artifacts[0].Mime != "text/markdown" {
		t.Errorf("Mime=%v, want text/markdown", out.Artifacts[0].Mime)
	}
	if out.Artifacts[1].Mime != nil || out.Artifacts[1].Bytes != nil || out.Artifacts[1].Sha256 != nil {
		t.Errorf("nullable fields should stay nil, got mime=%v bytes=%v sha=%v",
			out.Artifacts[1].Mime, out.Artifacts[1].Bytes, out.Artifacts[1].Sha256)
	}
}

func TestListVersionArtifacts_EmptyIsNonNilSlice(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	q := &fakeArtifactQuerier{
		version:   sqlc.TaskVersion{ID: pgUUID(versionID), TaskID: pgUUID(uuid.New())},
		task:      ownedTaskRow(),
		artifacts: nil, // no rows
	}
	svc := NewArtifactReadService(q, &fakePresigner{})

	out, err := svc.ListVersionArtifacts(context.Background(), testOwner, versionID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Artifacts == nil {
		t.Fatal("Artifacts is nil; must be an initialised empty slice for JSON []")
	}
	if len(out.Artifacts) != 0 {
		t.Errorf("len=%d, want 0", len(out.Artifacts))
	}
}

func TestListVersionArtifacts_UnknownVersion404(t *testing.T) {
	t.Parallel()
	q := &fakeArtifactQuerier{versionErr: pgx.ErrNoRows}
	svc := NewArtifactReadService(q, &fakePresigner{})

	_, err := svc.ListVersionArtifacts(context.Background(), testOwner, uuid.New())
	if !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("err=%v, want ErrVersionNotFound", err)
	}
}

func TestListVersionArtifacts_UnownedVersion404(t *testing.T) {
	t.Parallel()
	versionID := uuid.New()
	q := &fakeArtifactQuerier{
		version: sqlc.TaskVersion{ID: pgUUID(versionID), TaskID: pgUUID(uuid.New())},
		// task owned by a DIFFERENT user
		task: sqlc.Task{TenantID: pgUUID(testTenant), UserID: pgUUID(uuid.New())},
	}
	svc := NewArtifactReadService(q, &fakePresigner{})

	_, err := svc.ListVersionArtifacts(context.Background(), testOwner, versionID)
	if !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("err=%v, want ErrVersionNotFound (never 403)", err)
	}
}

func TestPresignArtifact_OwnedCallsOSSWithRowKey(t *testing.T) {
	t.Parallel()
	exp := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	pre := &fakePresigner{url: "https://oss.example/t/v/file/index.md?X-Amz-Signature=abc", expires: exp}
	q := &fakeArtifactQuerier{
		artifact: sqlc.GetArtifactWithOwnerRow{
			OssKey:   "t/v/file/index.md",
			Bytes:    i64ptr(1024),
			Mime:     strptr("text/markdown"),
			Sha256:   strptr("deadbeef"),
			TenantID: pgUUID(testTenant),
			UserID:   pgUUID(testUser),
		},
	}
	svc := NewArtifactReadService(q, pre)

	out, err := svc.PresignArtifact(context.Background(), testOwner, uuid.New())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pre.called || pre.gotKey != "t/v/file/index.md" {
		t.Errorf("presigner called with key=%q (called=%v), want the row's oss_key", pre.gotKey, pre.called)
	}
	if out.URL != pre.url || !out.ExpiresAt.Equal(exp) {
		t.Errorf("URL/ExpiresAt not passed through: %q / %v", out.URL, out.ExpiresAt)
	}
	if out.Bytes == nil || *out.Bytes != 1024 || out.Mime == nil || *out.Mime != "text/markdown" {
		t.Errorf("row metadata not echoed: bytes=%v mime=%v", out.Bytes, out.Mime)
	}
}

func TestPresignArtifact_UnknownArtifact404(t *testing.T) {
	t.Parallel()
	pre := &fakePresigner{}
	q := &fakeArtifactQuerier{artifErr: pgx.ErrNoRows}
	svc := NewArtifactReadService(q, pre)

	_, err := svc.PresignArtifact(context.Background(), testOwner, uuid.New())
	if !errors.Is(err, ErrArtifactNotFound) {
		t.Errorf("err=%v, want ErrArtifactNotFound", err)
	}
	if pre.called {
		t.Error("presigner must NOT be called for a missing artifact")
	}
}

func TestPresignArtifact_UnownedArtifact404(t *testing.T) {
	t.Parallel()
	pre := &fakePresigner{}
	q := &fakeArtifactQuerier{
		artifact: sqlc.GetArtifactWithOwnerRow{
			OssKey:   "t/v/file/index.md",
			TenantID: pgUUID(testTenant),
			UserID:   pgUUID(uuid.New()), // different user
		},
	}
	svc := NewArtifactReadService(q, pre)

	_, err := svc.PresignArtifact(context.Background(), testOwner, uuid.New())
	if !errors.Is(err, ErrArtifactNotFound) {
		t.Errorf("err=%v, want ErrArtifactNotFound (never 403)", err)
	}
	if pre.called {
		t.Error("presigner must NOT be called for an unowned artifact (no existence leak via OSS)")
	}
}

func TestPresignArtifact_PresignerErrorPropagates(t *testing.T) {
	t.Parallel()
	pre := &fakePresigner{err: errors.New("oss down")}
	q := &fakeArtifactQuerier{
		artifact: sqlc.GetArtifactWithOwnerRow{
			OssKey:   "t/v/file/index.md",
			TenantID: pgUUID(testTenant),
			UserID:   pgUUID(testUser),
		},
	}
	svc := NewArtifactReadService(q, pre)

	_, err := svc.PresignArtifact(context.Background(), testOwner, uuid.New())
	if err == nil || errors.Is(err, ErrArtifactNotFound) {
		t.Errorf("err=%v, want a non-404 error that maps to 500", err)
	}
}
