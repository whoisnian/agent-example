// Contract-level tests for the version-scoped artifact routes added by
// improve-artifact-conversation-ux: the `path` field on the list DTO, the zip
// archive presign + download, the directory-aware preview mint + serve, the
// token-audience isolation between the three artifact-token kinds, and the
// guarantee that the preview token (a path segment) never reaches the access
// log. Same fake-Querier stack as artifact_reads_test.go.
package httpapi

import (
	"archive/zip"
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

func mintArchiveToken(t *testing.T, versionID uuid.UUID, ttl time.Duration) string {
	t.Helper()
	tok, _, err := auth.NewDownloadIssuer(artTestSecret, ttl).IssueArchive(versionID)
	if err != nil {
		t.Fatalf("mint archive token: %v", err)
	}
	return tok
}

func mintPreviewToken(t *testing.T, versionID uuid.UUID, ttl time.Duration) string {
	t.Helper()
	tok, _, err := auth.NewDownloadIssuer(artTestSecret, ttl).IssuePreview(versionID)
	if err != nil {
		t.Fatalf("mint preview token: %v", err)
	}
	return tok
}

// --- 3.1: list DTO carries path, present-and-null ---------------------------

func TestHTTP_ListArtifacts_PathPresentAndNullable(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{
		version: sqlc.TaskVersion{ID: artPg(vid), TaskID: artPg(uuid.New())},
		task:    ownedTask(),
		artifacts: []sqlc.Artifact{
			{ID: artPg(uuid.New()), Kind: "file", OssKey: "t/v/index.html", Path: ptr("index.html"), Mime: ptr("text/html")},
			{ID: artPg(uuid.New()), Kind: "file", OssKey: "t/v/legacy", Path: nil, Mime: nil},
		},
	}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/versions/"+vid.String()+"/artifacts")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(body, `"path":"index.html"`) {
		t.Errorf("expected path=index.html in body: %s", body)
	}
	if !strings.Contains(body, `"path":null`) {
		t.Errorf("expected a present-and-null path for the legacy row: %s", body)
	}
}

// --- archive presign + download ---------------------------------------------

func TestHTTP_ArchivePresign_200VersionScoped(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{version: sqlc.TaskVersion{ID: artPg(vid), TaskID: artPg(uuid.New())}, task: ownedTask()}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/versions/"+vid.String()+"/artifacts/archive/presign")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(body, "/artifacts/archive?token=") || !strings.Contains(body, "expires_at") {
		t.Errorf("archive presign body missing url/expires_at: %s", body)
	}
}

func TestHTTP_ArchivePresign_404Unowned(t *testing.T) {
	t.Parallel()
	q := &fakeArtQuerier{versionErr: pgx.ErrNoRows}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/versions/"+uuid.New().String()+"/artifacts/archive/presign")
	if status != http.StatusNotFound || !strings.Contains(body, "version_not_found") {
		t.Errorf("status=%d body=%s, want 404 version_not_found", status, body)
	}
}

func TestHTTP_ArchiveDownload_200ZipWithPathEntries(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	vid := uuid.New()
	q := &fakeArtQuerier{objects: []sqlc.ListArtifactObjectsByVersionRow{
		{ID: artPg(uuid.New()), OssKey: "t/v/index.html", Path: ptr("index.html")},
		{ID: artPg(uuid.New()), OssKey: "t/v/css/style.css", Path: ptr("css/style.css")},
	}}
	store := &fakeArtObjectStore{body: "BYTES"}
	e := newArtEngine(t, q, &fakeArtPresigner{}, store, m)

	tok := mintArchiveToken(t, vid, 5*time.Minute)
	w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/artifacts/archive?token="+tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type=%q, want application/zip", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "artifacts-"+vid.String()+".zip") {
		t.Errorf("Content-Disposition=%q missing filename", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "index.html,css/style.css" {
		t.Errorf("zip entry names=%v, want [index.html css/style.css]", names)
	}
}

func TestHTTP_ArchiveDownload_EmptyVersionValidEmptyZip(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{objects: nil}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	tok := mintArchiveToken(t, vid, 5*time.Minute)
	w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/artifacts/archive?token="+tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("empty zip not valid: %v", err)
	}
	if len(zr.File) != 0 {
		t.Errorf("want 0 entries, got %d", len(zr.File))
	}
}

func TestHTTP_ArchiveDownload_403TokenFailures(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	e := newArtEngine(t, &fakeArtQuerier{}, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	cases := map[string]string{
		"missing":           "",
		"expired":           "?token=" + mintArchiveToken(t, vid, -time.Hour),
		"cross-version":     "?token=" + mintArchiveToken(t, uuid.New(), 5*time.Minute),
		"download-not-arch": "?token=" + mintDownloadToken(t, vid, 5*time.Minute),
		"preview-not-arch":  "?token=" + mintPreviewToken(t, vid, 5*time.Minute),
	}
	for name, qs := range cases {
		w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/artifacts/archive"+qs)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invalid_download_token") {
			t.Errorf("%s: status=%d body=%s, want 403 invalid_download_token", name, w.Code, w.Body.String())
		}
	}
}

// --- preview mint + serve ---------------------------------------------------

func TestHTTP_PreviewMint_200BaseURL(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{version: sqlc.TaskVersion{ID: artPg(vid), TaskID: artPg(uuid.New())}, task: ownedTask()}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{}, observability.NewMetrics())
	status, body := rawDo(e, "/api/v1/versions/"+vid.String()+"/preview")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(body, "/preview/") || !strings.Contains(body, "base_url") {
		t.Errorf("preview mint body missing base_url: %s", body)
	}
}

func TestHTTP_PreviewServe_200RelativeAssetResolves(t *testing.T) {
	t.Parallel()
	m := observability.NewMetrics()
	vid := uuid.New()
	q := &fakeArtQuerier{pathRows: map[string]sqlc.GetArtifactObjectByVersionPathRow{
		"index.html":    {OssKey: "t/v/index.html", Mime: ptr("text/html")},
		"css/style.css": {OssKey: "t/v/css/style.css", Mime: ptr("text/css")},
	}}
	store := &fakeArtObjectStore{body: "body { color: red }", length: i64(19)}
	e := newArtEngine(t, q, &fakeArtPresigner{}, store, m)
	tok := mintPreviewToken(t, vid, 5*time.Minute)

	// The sibling asset request (what the rendered index.html triggers).
	w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/preview/"+tok+"/css/style.css")
	if w.Code != http.StatusOK || w.Body.String() != "body { color: red }" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	wantHeaders := map[string]string{
		"Content-Type":            "text/css",
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
}

func TestHTTP_PreviewServe_404TraversalAndEmptyAndUnknown(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{pathRows: map[string]sqlc.GetArtifactObjectByVersionPathRow{
		"index.html": {OssKey: "t/v/index.html", Mime: ptr("text/html")},
	}}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{body: "x"}, observability.NewMetrics())
	tok := mintPreviewToken(t, vid, 5*time.Minute)
	for _, sub := range []string{
		"/../secret.txt", // traversal
		"/",              // empty
		"/missing.js",    // unknown path
	} {
		w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/preview/"+tok+sub)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "artifact_not_found") {
			t.Errorf("%q: status=%d body=%s, want 404 artifact_not_found", sub, w.Code, w.Body.String())
		}
	}
}

func TestHTTP_PreviewServe_403WrongAudience(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{pathRows: map[string]sqlc.GetArtifactObjectByVersionPathRow{
		"index.html": {OssKey: "t/v/index.html", Mime: ptr("text/html")},
	}}
	e := newArtEngine(t, q, &fakeArtPresigner{}, &fakeArtObjectStore{body: "x"}, observability.NewMetrics())
	cases := map[string]string{
		"archive-not-preview":  mintArchiveToken(t, vid, 5*time.Minute),
		"download-not-preview": mintDownloadToken(t, vid, 5*time.Minute),
		"cross-version":        mintPreviewToken(t, uuid.New(), 5*time.Minute),
		"expired":              mintPreviewToken(t, vid, -time.Hour),
	}
	for name, tok := range cases {
		w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/preview/"+tok+"/index.html")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invalid_download_token") {
			t.Errorf("%s: status=%d body=%s, want 403", name, w.Code, w.Body.String())
		}
	}
}

// --- access log never carries the preview token (path segment) --------------

func TestHTTP_PreviewServe_TokenNeverInAccessLog(t *testing.T) {
	t.Parallel()
	vid := uuid.New()
	q := &fakeArtQuerier{pathRows: map[string]sqlc.GetArtifactObjectByVersionPathRow{
		"index.html": {OssKey: "t/v/index.html", Mime: ptr("text/html")},
	}}
	domainSvc := taskdomain.NewArtifactReadService(q, &fakeArtPresigner{}, &fakeArtObjectStore{body: "<html></html>"})
	issuer := auth.NewDownloadIssuer(artTestSecret, 5*time.Minute)
	domainSvc.ArchivePresigner = auth.ArchiveURLSigner{Issuer: issuer}
	domainSvc.PreviewPresigner = auth.PreviewURLSigner{Issuer: issuer}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(accessLogMiddleware(logger)) // exercises accessLogPath redaction
	e.Use(injectPrincipal(artTenant, artUser))
	h := &ArtifactHandlers{App: apptask.NewArtifactReadService(domainSvc), Logger: logger, Metrics: observability.NewMetrics(), Tokens: auth.NewDownloadVerifier(artTestSecret)}
	h.Register(e.Group("/api/v1"))

	tok := mintPreviewToken(t, vid, 5*time.Minute)
	w := downloadDo(e, "/api/v1/versions/"+vid.String()+"/preview/"+tok+"/index.html")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	logged := logBuf.String()
	if strings.Contains(logged, tok) {
		t.Errorf("access log leaked the preview token: %s", logged)
	}
	if !strings.Contains(logged, "/preview/[redacted]/index.html") {
		t.Errorf("access log missing redacted preview path: %s", logged)
	}
}
