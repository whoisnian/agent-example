//go:build integration

// HTTP-level integration test for add-artifacts-api, reworked by
// add-artifact-download-proxy. Stands up a real PostgreSQL container (for the
// artifact/version/task rows) AND a real MinIO container (the worker-proven S3
// double; no SeaweedFS testcontainers module exists — design D2a). It seeds an
// artifact + uploads its bytes, presigns via the endpoint, then GETs the
// returned API-relative download URL (unauthenticated — the token in the URL
// is the grant) to prove the bytes round-trip THROUGH the download proxy —
// the S3-protocol-drift guard for the GetObject streaming path.
//
// Run with: make test-integration
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/oss"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
	httpapi "github.com/whoisnian/agent-example/api/internal/interfaces/http"
)

const artBucket = "artifacts"

// startPGForArtifacts boots PG, migrates, and returns a pool.
func startPGForArtifacts(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, pgImage,
		tcpostgres.WithDatabase(pgDatabase),
		tcpostgres.WithUsername(pgUser),
		tcpostgres.WithPassword(pgPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	mig, err := persistence.NewMigrator(migrationsDir(t), dsn)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	if upErr := mig.Up(); upErr != nil {
		_ = mig.Close()
		t.Fatalf("migrate up: %v", upErr)
	}
	_ = mig.Close()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// startMinIO boots MinIO and returns the S3 endpoint URL + credentials.
func startMinIO(t *testing.T) (endpoint, user, pass string) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcminio.Run(ctx, "minio/minio:RELEASE.2024-01-16T16-07-38Z")
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	conn, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("minio conn: %v", err)
	}
	return "http://" + conn, ctr.Username, ctr.Password
}

// s3ClientFor builds a path-style S3 client pointed at the given endpoint.
func s3ClientFor(endpoint, user, pass string) *awss3.Client {
	return awss3.New(awss3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(user, pass, ""),
	})
}

// seedArtifact inserts a task → version → artifact owned by (tenant,user) and
// returns the version id, artifact id, and oss key.
func seedArtifact(t *testing.T, pool *pgxpool.Pool, tenant, user uuid.UUID) (versionID, artifactID uuid.UUID, key string) {
	t.Helper()
	ctx := context.Background()
	taskID := uuid.New()
	versionID = uuid.New()
	artifactID = uuid.New()
	key = taskID.String() + "/" + versionID.String() + "/file/report.md"

	if _, err := pool.Exec(ctx,
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status) VALUES ($1,$2,$3,$4,$5,$6)`,
		taskID, tenant, user, "build report", "research", "succeeded"); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status) VALUES ($1,$2,$3,$4,$5)`,
		versionID, taskID, 1, "investigate", "succeeded"); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	mime := "text/markdown"
	bytesLen := int64(0) // filled by caller via separate update if needed
	if _, err := pool.Exec(ctx,
		`INSERT INTO artifacts (id, version_id, kind, oss_key, mime, bytes, sha256) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		artifactID, versionID, "file", key, mime, bytesLen, "deadbeef"); err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	return versionID, artifactID, key
}

// newArtifactEngine stands up the artifact read server authenticating as
// (tenant, user); it returns the server plus the Bearer header for that
// principal so callers can authenticate their requests.
func newArtifactEngine(t *testing.T, pool *pgxpool.Pool, ossClient *oss.Client, tenant, user uuid.UUID) (*httptest.Server, string) {
	t.Helper()
	queries := sqlc.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	downloadSigner := auth.DownloadURLSigner{
		Issuer: auth.NewDownloadIssuer(intgJWTSecret, 5*time.Minute),
	}
	engine := httpapi.NewEngine(&httpapi.ServerDeps{
		Logger:   logger,
		Metrics:  observability.NewMetrics(),
		Probes:   httpapi.NewProbeRegistry(time.Second),
		Verifier: auth.NewVerifier(intgJWTSecret),
		ArtifactHandlers: &httpapi.ArtifactHandlers{
			App:     apptask.NewArtifactReadService(taskdomain.NewArtifactReadService(queries, downloadSigner, ossClient)),
			Logger:  logger,
			Metrics: observability.NewMetrics(),
			Tokens:  auth.NewDownloadVerifier(intgJWTSecret),
		},
	})
	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)
	return ts, intgAuthHeaderFor(tenant, user)
}

// authGet issues a GET carrying the Bearer header.
func authGet(t *testing.T, url, authHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func TestArtifactPresignRoundTrip(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	user := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	pool := startPGForArtifacts(t)
	endpoint, akid, secret := startMinIO(t)

	// Bucket + object.
	s3c := s3ClientFor(endpoint, akid, secret)
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(artBucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	versionID, artifactID, key := seedArtifact(t, pool, tenant, user)
	content := []byte("# Research Report\n\nfindings...\n")
	if _, err := s3c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(artBucket), Key: aws.String(key), Body: bytes.NewReader(content),
	}); err != nil {
		t.Fatalf("put object: %v", err)
	}

	ossClient := oss.New(&oss.Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: artBucket,
		AccessKeyID: akid, AccessKeySecret: secret, UsePathStyle: true,
	})
	ts, hdr := newArtifactEngine(t, pool, ossClient, tenant, user)

	// 1) list endpoint — 200, one artifact, no oss_key in the body.
	listResp := authGet(t, ts.URL+"/api/v1/versions/"+versionID.String()+"/artifacts", hdr)
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResp.StatusCode, listBody)
	}
	if bytes.Contains(listBody, []byte("oss_key")) || bytes.Contains(listBody, []byte(key)) {
		t.Errorf("list leaked oss_key: %s", listBody)
	}

	// 2) presign endpoint — 200 with an API-relative download URL, then GET it
	// through the proxy (NO auth header: the token in the URL is the grant) and
	// round-trip the bytes.
	presignResp := authGet(t, ts.URL+"/api/v1/artifacts/"+artifactID.String()+"/presign", hdr)
	var env struct {
		Data struct {
			URL       string `json:"url"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	pBody, _ := io.ReadAll(presignResp.Body)
	presignResp.Body.Close()
	if presignResp.StatusCode != http.StatusOK {
		t.Fatalf("presign status=%d body=%s", presignResp.StatusCode, pBody)
	}
	if err := json.Unmarshal(pBody, &env); err != nil {
		t.Fatalf("decode presign: %v (%s)", err, pBody)
	}
	if env.Data.URL == "" || env.Data.ExpiresAt == "" {
		t.Fatalf("presign missing url/expires_at: %s", pBody)
	}
	if !strings.HasPrefix(env.Data.URL, "/api/v1/artifacts/"+artifactID.String()+"/download?token=") {
		t.Fatalf("url is not the API-relative download route: %s", env.Data.URL)
	}

	dl, err := http.Get(ts.URL + env.Data.URL)
	if err != nil {
		t.Fatalf("GET download url: %v", err)
	}
	got, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download status=%d body=%s", dl.StatusCode, got)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("round-trip mismatch: got %q want %q", got, content)
	}
	wantHeaders := map[string]string{
		"Content-Type":            "text/markdown",
		"Content-Security-Policy": "sandbox allow-scripts",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
	}
	for k, want := range wantHeaders {
		if gotH := dl.Header.Get(k); gotH != want {
			t.Errorf("download header %s=%q, want %q", k, gotH, want)
		}
	}
}

// TestArtifactDownloadMissingObjectReturns502 exercises the real-OSS failure
// path: the artifact row exists and the token is valid, but the object was
// never uploaded (lifecycle-expired analogue) — the proxy must answer 502
// oss_unavailable without leaking the key.
func TestArtifactDownloadMissingObjectReturns502(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	user := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	pool := startPGForArtifacts(t)
	endpoint, akid, secret := startMinIO(t)
	s3c := s3ClientFor(endpoint, akid, secret)
	if _, err := s3c.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(artBucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	_, artifactID, key := seedArtifact(t, pool, tenant, user) // row only — no PutObject

	ossClient := oss.New(&oss.Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: artBucket,
		AccessKeyID: akid, AccessKeySecret: secret, UsePathStyle: true,
	})
	ts, hdr := newArtifactEngine(t, pool, ossClient, tenant, user)

	presignResp := authGet(t, ts.URL+"/api/v1/artifacts/"+artifactID.String()+"/presign", hdr)
	var env struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	pBody, _ := io.ReadAll(presignResp.Body)
	presignResp.Body.Close()
	if presignResp.StatusCode != http.StatusOK {
		t.Fatalf("presign status=%d body=%s (mint must not probe the object)", presignResp.StatusCode, pBody)
	}
	if err := json.Unmarshal(pBody, &env); err != nil {
		t.Fatalf("decode presign: %v (%s)", err, pBody)
	}

	dl, err := http.Get(ts.URL + env.Data.URL)
	if err != nil {
		t.Fatalf("GET download url: %v", err)
	}
	body, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != http.StatusBadGateway || !bytes.Contains(body, []byte("oss_unavailable")) {
		t.Errorf("status=%d body=%s, want 502 oss_unavailable", dl.StatusCode, body)
	}
	if bytes.Contains(body, []byte(key)) {
		t.Errorf("response leaked oss_key: %s", body)
	}
}

func TestArtifactPresignUnownedReturns404(t *testing.T) {
	tenant := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	owner := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	other := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")

	pool := startPGForArtifacts(t)
	endpoint, akid, secret := startMinIO(t)
	_, artifactID, _ := seedArtifact(t, pool, tenant, owner)

	ossClient := oss.New(&oss.Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: artBucket,
		AccessKeyID: akid, AccessKeySecret: secret, UsePathStyle: true,
	})
	// Engine runs as a DIFFERENT user → the seeded artifact is unowned.
	ts, hdr := newArtifactEngine(t, pool, ossClient, tenant, other)

	resp := authGet(t, ts.URL+"/api/v1/artifacts/"+artifactID.String()+"/presign", hdr)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !bytes.Contains(body, []byte("artifact_not_found")) {
		t.Errorf("status=%d body=%s, want 404 artifact_not_found", resp.StatusCode, body)
	}
}
