## 1. OSS client infrastructure

- [x] 1.1 Add `github.com/aws/aws-sdk-go-v2` modules (`config`, `credentials`, `service/s3`) to `api/go.mod`; run `go mod tidy`
- [x] 1.2 Add `OSS` struct to `infrastructure/config/config.go` with the **exact worker env names** for the four shared keys — `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET`, all `required:"true"` — plus `OSS_REGION` (default `us-east-1`), `OSS_USE_PATH_STYLE bool` (default `true`), `OSS_PRESIGN_TTL time.Duration` (default `5m`); add `oss:` YAML; ensure the struct is excluded from any config-dump log line (D7, no-credential-leak)
- [x] 1.3 Create `infrastructure/oss/client.go`: build an S3 client with a custom endpoint resolver + `UsePathStyle` + static credentials; expose `PresignGet(ctx, key) (url string, expiresAt time.Time, err error)` using `s3.NewPresignClient(...).PresignGetObject` with `s3.WithPresignExpires(cfg.PresignTTL)`; capture one `now()` and return `expiresAt = now + TTL` (advisory) (D2/D3)
- [x] 1.4 Confirm missing OSS config fails startup via `required:"true"` (same path as `DATABASE_URL`) — fail-fast, no per-request `oss_unconfigured` branch (D7); document the new required keys in the API README / compose

## 2. Persistence query

- [x] 2.1 Add `GetArtifactWithOwner :one` to `api/queries/artifacts.sql` — join `artifacts → task_versions → tasks`, selecting exactly `oss_key, bytes, mime, sha256, tenant_id, user_id` for the given `artifact_id` (no `id`/`kind`/`created_at` — the presign response needs none; keeps unused columns out of DTO assembly, D4)
- [x] 2.2 Run `make sqlc`; confirm generated `artifacts.sql.go` + `querier.go` compile

## 3. Domain read service + DTOs

- [x] 3.1 Add `artifact_read_dtos.go`: `ArtifactMeta{ID, Kind, Mime *string, Bytes *int64, Sha256 *string, CreatedAt}` (no `oss_key`) and `PresignResult{URL, ExpiresAt, Bytes *int64, Mime, Sha256 *string}` (D6)
- [x] 3.2 Add `ListVersionArtifacts(ctx, owner, versionID)` to the read service: ownership-probe the version (reuse the `ownedVersion` guard pattern) → `version_not_found` on miss/unowned; then `ListArtifactsByVersion`; map rows to `[]ArtifactMeta` (never `null`, empty slice for none)
- [x] 3.3 Add `PresignArtifact(ctx, owner, artifactID)` to the read service: `GetArtifactWithOwner` → compare `(tenant_id, user_id)`; map no-row OR wrong-owner to a new `ErrArtifactNotFound` sentinel; on success call `oss.PresignGet(oss_key)` and assemble `PresignResult` (D3/D4)

## 4. Application + HTTP layer

- [x] 4.1 Add `application/task/artifact_queries.go` orchestrating the two read-service calls (mirror `cost_queries.go`)
- [x] 4.2 Add error code `artifact_not_found` to `interfaces/http/errors.go`; map `ErrArtifactNotFound → 404`, sentinel version errors → existing 404s, presign SDK failure → `500 internal_error`
- [x] 4.3 Add `interfaces/http/artifact_reads.go` with `GET /versions/:version_id/artifacts` and `GET /artifacts/:artifact_id/presign`; validate UUID path params via the shared `parseUUIDParam` helper (emits `invalid_input` — NOT a domain error through `MapError`, which would yield `invalid_argument`); emit slog with `trace_id` + `version_id`/`artifact_id` (never `oss_key`/creds)
- [x] 4.4 Wire `ArtifactHandlers` into `ServerDeps` (`interfaces/http/server.go`) and register routes in the v1 group; construct the OSS client + handlers in `cmd/api/main.go`
- [x] 4.5 Add `OSSPresignTotal *prometheus.CounterVec` (by `outcome`) to the `Metrics` struct (mirror `MQPublishFailures`); increment per presign attempt (success/error) (D8, AGENTS.md §7)

## 5. Tests

- [x] 5.1 Domain unit tests: owned-version list (ordering `created_at ASC, id ASC`, empty slice not null, nullable `mime/bytes/sha256`→`null`, `kind` echoed verbatim e.g. `"file"`), unowned/unknown version → `version_not_found`; presign owned (calls OSS with the row's `oss_key`, assembles `expires_at = now+TTL`), unowned/unknown artifact → `artifact_not_found`, presigner error → `internal_error` (fake OSS presigner records the key it was asked to sign / can be told to fail)
- [x] 5.2 HTTP contract tests: 200 envelopes for both endpoints; assert response body has **no** `oss_key` field (D6 contract test); 404 codes (`version_not_found`, `artifact_not_found`); 400 `invalid_input` on malformed UUID and assert 400-precedes-404 (bad UUID on a non-existent resource still 400)
- [x] 5.3 Integration test using the Go testcontainers **MinIO** module (D2a — no SeaweedFS fixture exists; MinIO is the worker-proven S3 double): pre-create the bucket, insert an artifact row + upload bytes, presign via the endpoint, HTTP GET the returned URL and assert the bytes round-trip (S3-protocol-drift guard). *If the MinIO fixture proves heavy, split per D5a: land mocked-presigner unit/contract coverage now, real round-trip as fast follow.*
- [x] 5.4 Assert credentials never appear in logs/response (no-leak test); assert `OSSPresignTotal` increments with the right `outcome` label

## 6. Gates + docs

- [x] 6.1 `go vet ./...`, `go test ./...`, `golangci-lint run`, `make sqlc` (no diff) all clean
- [x] 6.2 `make test-integration` (testcontainers: PG + MinIO) green
- [x] 6.3 `openspec validate add-artifacts-api --strict` valid
- [x] 6.4 Update `docs/ARCHITECTURE.md` if the OSS config block or presign contract needs reflecting; note `/uploads/sts` remains deferred (D5)
