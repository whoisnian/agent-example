// Contract-level tests for add-artifacts-api / add-artifact-download-proxy.
// The domain read service takes the sqlc.Querier interface, so we drive the
// real handler → application → domain stack against a fake Querier + fake
// signer + fake object store — no DB, full wire contract (envelopes, no
// oss_key leak, 404 codes, 400-before-404, download token gate, response
// headers, metrics). The download tests use REAL download tokens (issuer +
// verifier share a test secret) so the token claims are exercised end-to-end.
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
	"github.com/whoisnian/agent-example/api/internal/auth"
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
	// improve-artifact-conversation-ux: archive + preview queries.
	objects  []sqlc.ListArtifactObjectsByVersionRow
	pathRows map[string]sqlc.GetArtifactObjectByVersionPathRow
	pathErr  error
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
func (f *fakeArtQuerier) ListArtifactObjectsByVersion(context.Context, pgtype.UUID) ([]sqlc.ListArtifactObjectsByVersionRow, error) {
	return f.objects, nil
}
func (f *fakeArtQuerier) GetArtifactObjectByVersionPath(_ context.Context, arg sqlc.GetArtifactObjectByVersionPathParams) (sqlc.GetArtifactObjectByVersionPathRow, error) {
	if f.pathErr != nil {
		return sqlc.GetArtifactObjectByVersionPathRow{}, f.pathErr
	}
	if arg.Path == nil {
		return sqlc.GetArtifactObjectByVersionPathRow{}, pgx.ErrNoRows
	}
	row, ok := f.pathRows[*arg.Path]
	if !ok {
		return sqlc.GetArtifactObjectByVersionPathRow{}, pgx.ErrNoRows
	}
	return row, nil
}

type fakeArtPresigner struct {
	called bool
	err    error
}

func (p *fakeArtPresigner) SignDownload(artifactID uuid.UUID) (string, time.Time, error) {
	p.called = true
	if p.err != nil {
		return "", time.Time{}, p.err
	}
	return "/api/v1/artifacts/" + artifactID.String() + "/download?token=abc",
		time.Date(2026, 6, 2, 12, 5, 0, 0, time.UTC), nil
}

// fakeArtObjectStore serves a canned body for the download proxy tests.
type fakeArtObjectStore struct {
	body   string
	length *int64
	err    error
}

func (s *fakeArtObjectStore) GetObject(context.Context, string) (io.ReadCloser, *int64, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), s.length, nil
}

const artTestSecret = "artifact-test-secret"

func newArtEngine(t *testing.T, q sqlc.Querier, pre taskdomain.ArtifactPresigner, store taskdomain.ArtifactObjectStore, m *observability.Metrics) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(gin.Recovery())
	e.Use(injectPrincipal(artTenant, artUser))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	domainSvc := taskdomain.NewArtifactReadService(q, pre, store)
	// Wire the version-scoped presigners with the test secret so archive/preview
	// tokens round-trip end-to-end (improve-artifact-conversation-ux).
	issuer := auth.NewDownloadIssuer(artTestSecret, 5*time.Minute)
	domainSvc.ArchivePresigner = auth.ArchiveURLSigner{Issuer: issuer}
	domainSvc.PreviewPresigner = auth.PreviewURLSigner{Issuer: issuer}
	h := &ArtifactHandlers{
		App:     apptask.NewArtifactReadService(domainSvc),
		Logger:  logger,
		Metrics: m,
		Tokens:  auth.NewDownloadVerifier(artTestSecret),
	}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e
}

// mintDownloadToken issues a real download token for the artifact with the
// engine's test secret.
func mintDownloadToken(t *testing.T, artifactID uuid.UUID, ttl time.Duration) string {
	t.Helper()
	tok, _, err := auth.NewDownloadIssuer(artTestSecret, ttl).Issue(artifactID)
	if err != nil {
		t.Fatalf("mint download token: %v", err)
	}
	return tok
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
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())

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
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())

	status, body := rawDo(e, "/api/v1/versions/"+uuid.New().String()+"/artifacts")
	if status != http.StatusNotFound || !strings.Contains(body, "version_not_found") {
		t.Errorf("status=%d body=%s, want 404 version_not_found", status, body)
	}
}

func TestHTTP_ListArtifacts_400MalformedUUID(t *testing.T) {
	t.Parallel()
	e := newArtEngine(t, &fakeArtQuerier{}, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
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
	e := newArtEngine(t, q, pre, &fakeArtObjectStore{}, m)

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
	e := newArtEngine(t, q, pre, &fakeArtObjectStore{}, m)

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
	e := newArtEngine(t, q, pre, &fakeArtObjectStore{}, m)

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
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/artifacts/not-a-uuid/presign")
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid_input") {
		t.Errorf("status=%d body=%s, want 400 invalid_input", status, body)
	}
}

func ptr(s string) *string { return &s }
func i64(n int64) *int64   { return &n }

// --- download proxy route (add-artifact-download-proxy) ---

// downloadDo performs a GET and returns the recorder for header assertions.
func downloadDo(e *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	e.ServeHTTP(w, req)
	return w
}

func ownedArtifactRow() sqlc.GetArtifactWithOwnerRow {
	return sqlc.GetArtifactWithOwnerRow{
		OssKey: "t/v/file/index.html", Mime: ptr("text/html"),
		TenantID: artPg(artTenant), UserID: artPg(artUser),
	}
}

func TestHTTP_Download_200StreamsWithHeadersAndMetrics(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	artifactID := uuid.New()
	store := &fakeArtObjectStore{body: "<html>hi</html>", length: i64(15)}
	e := newArtEngine(t, &fakeArtQuerier{artifact: ownedArtifactRow()}, &fakeArtPresigner{}, store, m)

	tok := mintDownloadToken(t, artifactID, 5*time.Minute)
	w := downloadDo(e, "/api/v1/artifacts/"+artifactID.String()+"/download?token="+tok)
	if w.Code != http.StatusOK || w.Body.String() != "<html>hi</html>" {
		t.Fatalf("status=%d body=%q, want 200 with object bytes", w.Code, w.Body.String())
	}
	wantHeaders := map[string]string{
		"Content-Type":            "text/html",
		"Content-Length":          "15",
		"Content-Security-Policy": "sandbox allow-scripts",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
	}
	for k, want := range wantHeaders {
		if got := w.Header().Get(k); got != want {
			t.Errorf("header %s=%q, want %q", k, got, want)
		}
	}
	if got := testutil.ToFloat64(m.OSSDownloadTotal.WithLabelValues("success")); got != 1 {
		t.Errorf("OSSDownloadTotal{success}=%v, want 1", got)
	}
	if got := testutil.ToFloat64(m.OSSDownloadBytes); got != 15 {
		t.Errorf("OSSDownloadBytes=%v, want 15", got)
	}
}

func TestHTTP_Download_NullMimeFallsBackToOctetStream(t *testing.T) {
	t.Parallel()
	artifactID := uuid.New()
	row := ownedArtifactRow()
	row.Mime = nil
	store := &fakeArtObjectStore{body: "", length: i64(0)}
	e := newArtEngine(t, &fakeArtQuerier{artifact: row}, &fakeArtPresigner{}, store, observability.NewMetrics())

	tok := mintDownloadToken(t, artifactID, 5*time.Minute)
	w := downloadDo(e, "/api/v1/artifacts/"+artifactID.String()+"/download?token="+tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type=%q, want application/octet-stream for null mime", got)
	}
	// A legitimate 0-byte object still gets an explicit Content-Length: 0.
	if got := w.Header().Get("Content-Length"); got != "0" {
		t.Errorf("Content-Length=%q, want \"0\" for an empty object", got)
	}
}

func TestHTTP_Download_403SingleCodeForAllTokenFailures(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	artifactID := uuid.New()
	e := newArtEngine(t, &fakeArtQuerier{artifact: ownedArtifactRow()}, &fakeArtPresigner{}, &fakeArtObjectStore{body: "x"}, m)

	// access token (no aud) minted with the same secret
	accessTok, _, err := auth.NewIssuer(artTestSecret, time.Hour).Issue(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	cases := map[string]string{
		"missing token":      "",
		"garbage token":      "?token=not.a.jwt",
		"expired token":      "?token=" + mintDownloadToken(t, artifactID, -time.Hour),
		"sub mismatch":       "?token=" + mintDownloadToken(t, uuid.New(), 5*time.Minute),
		"access token mixed": "?token=" + accessTok,
	}
	for name, qs := range cases {
		w := downloadDo(e, "/api/v1/artifacts/"+artifactID.String()+"/download"+qs)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invalid_download_token") {
			t.Errorf("%s: status=%d body=%s, want 403 invalid_download_token", name, w.Code, w.Body.String())
		}
	}
	if got := testutil.ToFloat64(m.OSSDownloadTotal.WithLabelValues("token_invalid")); got != float64(len(cases)) {
		t.Errorf("OSSDownloadTotal{token_invalid}=%v, want %d", got, len(cases))
	}
}

func TestHTTP_Download_400MalformedUUIDBeforeToken(t *testing.T) {
	t.Parallel()
	e := newArtEngine(t, &fakeArtQuerier{}, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	w := downloadDo(e, "/api/v1/artifacts/not-a-uuid/download")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_input") {
		t.Errorf("status=%d body=%s, want 400 invalid_input", w.Code, w.Body.String())
	}
}

func TestHTTP_Download_404ValidTokenRowGone(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	artifactID := uuid.New()
	e := newArtEngine(t, &fakeArtQuerier{artifErr: pgx.ErrNoRows}, &fakeArtPresigner{}, &fakeArtObjectStore{}, m)

	tok := mintDownloadToken(t, artifactID, 5*time.Minute)
	w := downloadDo(e, "/api/v1/artifacts/"+artifactID.String()+"/download?token="+tok)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "artifact_not_found") {
		t.Errorf("status=%d body=%s, want 404 artifact_not_found", w.Code, w.Body.String())
	}
	if got := testutil.ToFloat64(m.OSSDownloadTotal.WithLabelValues("not_found")); got != 1 {
		t.Errorf("OSSDownloadTotal{not_found}=%v, want 1", got)
	}
}

func TestHTTP_Download_502OSSFailureNoLeak(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	artifactID := uuid.New()
	store := &fakeArtObjectStore{err: errors.New("dial tcp 10.0.0.9:9000: connection refused")}
	e := newArtEngine(t, &fakeArtQuerier{artifact: ownedArtifactRow()}, &fakeArtPresigner{}, store, m)

	tok := mintDownloadToken(t, artifactID, 5*time.Minute)
	w := downloadDo(e, "/api/v1/artifacts/"+artifactID.String()+"/download?token="+tok)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "oss_unavailable") {
		t.Errorf("status=%d body=%s, want 502 oss_unavailable", w.Code, w.Body.String())
	}
	for _, leak := range []string{"t/v/file/index.html", "10.0.0.9", "connection refused"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("response leaked OSS internals (%q): %s", leak, w.Body.String())
		}
	}
	if got := testutil.ToFloat64(m.OSSDownloadTotal.WithLabelValues("oss_error")); got != 1 {
		t.Errorf("OSSDownloadTotal{oss_error}=%v, want 1", got)
	}
}
