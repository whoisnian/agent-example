// Contract-level tests for add-artifacts-api. The domain read service takes the
// sqlc.Querier interface, so we drive the real handler → application → domain
// stack against a fake Querier + fake presigner — no DB, full wire contract
// (envelopes, no oss_key leak, 404 codes, 400-before-404, presign metric).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus/testutil"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

var (
	artTenant = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	artUser   = uuid.MustParse("00000000-0000-0000-0000-000000000002")
)

func artPg(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

// fakeArtQuerier implements the four Querier methods the artifact service uses.
type fakeArtQuerier struct {
	sqlc.Querier
	version    sqlc.TaskVersion
	versionErr error
	task       sqlc.Task
	artifacts  []sqlc.Artifact
	artifact   sqlc.GetArtifactWithOwnerRow
	artifErr   error
}

func (f *fakeArtQuerier) GetTaskVersionByID(context.Context, pgtype.UUID) (sqlc.TaskVersion, error) {
	return f.version, f.versionErr
}
func (f *fakeArtQuerier) GetTaskByID(context.Context, pgtype.UUID) (sqlc.Task, error) {
	return f.task, nil
}
func (f *fakeArtQuerier) ListArtifactsByVersion(context.Context, pgtype.UUID) ([]sqlc.Artifact, error) {
	return f.artifacts, nil
}
func (f *fakeArtQuerier) GetArtifactWithOwner(context.Context, pgtype.UUID) (sqlc.GetArtifactWithOwnerRow, error) {
	return f.artifact, f.artifErr
}

type fakeArtPresigner struct {
	called bool
	err    error
}

func (p *fakeArtPresigner) PresignGet(context.Context, string) (string, time.Time, error) {
	p.called = true
	if p.err != nil {
		return "", time.Time{}, p.err
	}
	return "https://oss.example/signed?X-Amz-Signature=abc", time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC), nil
}

func newArtEngine(t *testing.T, q sqlc.Querier, pre taskdomain.ArtifactPresigner, m *observability.Metrics) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(gin.Recovery())
	e.Use(injectPrincipal(artTenant, artUser))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &ArtifactHandlers{
		App:     apptask.NewArtifactReadService(taskdomain.NewArtifactReadService(q, pre)),
		Logger:  logger,
		Metrics: m,
	}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e
}

// rawDo returns the raw response body so we can assert on absent fields.
func rawDo(e *gin.Engine, path string) (status int, body string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	e.ServeHTTP(w, req)
	return w.Result().StatusCode, w.Body.String()
}

func ownedTask() sqlc.Task { return sqlc.Task{TenantID: artPg(artTenant), UserID: artPg(artUser)} }

func TestHTTP_ListArtifacts_200NoOssKey(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{
		version: sqlc.TaskVersion{ID: artPg(vid), TaskID: artPg(uuid.New())},
		task:    ownedTask(),
		artifacts: []sqlc.Artifact{
			{ID: artPg(uuid.New()), Kind: "file", OssKey: "t/v/file/index.md", Mime: ptr("text/markdown"), Bytes: i64(1024), Sha256: ptr("dead")},
		},
	}
	e := newArtEngine(t, q, &fakeArtPresigner{}, observability.NewMetrics())

	status, body := rawDo(e, "/api/v1/versions/"+vid.String()+"/artifacts")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if strings.Contains(body, "oss_key") || strings.Contains(body, "t/v/file/index.md") {
		t.Errorf("response leaked oss_key / storage layout: %s", body)
	}
	// shape sanity
	var env map[string]any
	_ = json.Unmarshal([]byte(body), &env)
	data, _ := env["data"].(map[string]any)
	arts, _ := data["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact, body=%s", body)
	}
}

func TestHTTP_ListArtifacts_404VersionNotFound(t *testing.T) {
	t.Parallel()
	q := &fakeArtQuerier{versionErr: pgx.ErrNoRows}
	e := newArtEngine(t, q, &fakeArtPresigner{}, observability.NewMetrics())

	status, body := rawDo(e, "/api/v1/versions/"+uuid.New().String()+"/artifacts")
	if status != http.StatusNotFound || !strings.Contains(body, "version_not_found") {
		t.Errorf("status=%d body=%s, want 404 version_not_found", status, body)
	}
}

func TestHTTP_ListArtifacts_400MalformedUUID(t *testing.T) {
	t.Parallel()
	e := newArtEngine(t, &fakeArtQuerier{}, &fakeArtPresigner{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/versions/not-a-uuid/artifacts")
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Errorf("status=%d body=%s, want 400 invalid_input", status, body)
	}
}

func TestHTTP_Presign_200AndMetric(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	q := &fakeArtQuerier{artifact: sqlc.GetArtifactWithOwnerRow{
		OssKey: "t/v/file/index.md", Bytes: i64(1024), Mime: ptr("text/markdown"),
		TenantID: artPg(artTenant), UserID: artPg(artUser),
	}}
	pre := &fakeArtPresigner{}
	e := newArtEngine(t, q, pre, m)

	status, body := rawDo(e, "/api/v1/artifacts/"+uuid.New().String()+"/presign")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if strings.Contains(body, "oss_key") {
		t.Errorf("presign response leaked oss_key: %s", body)
	}
	if !strings.Contains(body, "expires_at") || !strings.Contains(body, "\"url\"") {
		t.Errorf("presign body missing url/expires_at: %s", body)
	}
	if got := testutil.ToFloat64(m.OSSPresignTotal.WithLabelValues("success")); got != 1 {
		t.Errorf("OSSPresignTotal{success}=%v, want 1", got)
	}
}

func TestHTTP_Presign_404NoMetric(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	pre := &fakeArtPresigner{}
	q := &fakeArtQuerier{artifErr: pgx.ErrNoRows}
	e := newArtEngine(t, q, pre, m)

	status, body := rawDo(e, "/api/v1/artifacts/"+uuid.New().String()+"/presign")
	if status != http.StatusNotFound || !strings.Contains(body, "artifact_not_found") {
		t.Errorf("status=%d body=%s, want 404 artifact_not_found", status, body)
	}
	if pre.called {
		t.Error("presigner must not be called for a missing artifact")
	}
	if got := testutil.ToFloat64(m.OSSPresignTotal.WithLabelValues("error")); got != 0 {
		t.Errorf("OSSPresignTotal{error}=%v, want 0 (404 is not an OSS interaction)", got)
	}
}

func TestHTTP_Presign_500OnPresignerErrorAndMetric(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	pre := &fakeArtPresigner{err: errors.New("oss down")}
	q := &fakeArtQuerier{artifact: sqlc.GetArtifactWithOwnerRow{
		OssKey: "t/v/file/index.md", TenantID: artPg(artTenant), UserID: artPg(artUser),
	}}
	e := newArtEngine(t, q, pre, m)

	status, body := rawDo(e, "/api/v1/artifacts/"+uuid.New().String()+"/presign")
	if status != http.StatusInternalServerError || !strings.Contains(body, "internal_error") {
		t.Errorf("status=%d body=%s, want 500 internal_error", status, body)
	}
	if got := testutil.ToFloat64(m.OSSPresignTotal.WithLabelValues("error")); got != 1 {
		t.Errorf("OSSPresignTotal{error}=%v, want 1", got)
	}
}

func TestHTTP_Presign_400BeforeAnyLookup(t *testing.T) {
	t.Parallel()
	// A malformed UUID returns 400 even though no such artifact exists — path
	// validation runs before ownership resolution.
	q := &fakeArtQuerier{artifErr: pgx.ErrNoRows}
	e := newArtEngine(t, q, &fakeArtPresigner{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/artifacts/not-a-uuid/presign")
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Errorf("status=%d body=%s, want 400 invalid_input", status, body)
	}
}

func ptr(s string) *string { return &s }
func i64(n int64) *int64   { return &n }
