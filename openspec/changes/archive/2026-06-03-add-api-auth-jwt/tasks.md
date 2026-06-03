## 1. JWT + Principal foundation

- [x] 1.1 Add `github.com/golang-jwt/jwt/v5` to `api/go.mod` (`go get`; commit `go.sum`).
- [x] 1.2 New `auth` package (`internal/auth/` or `pkg/auth/`): `Principal{TenantID, UserID uuid.UUID}`; `Issuer.Issue(tenantID, userID) (token string, expiresAt time.Time, err error)` (HS256, claims `sub`=user, `tid`=tenant, `iat`, `exp`, `iss`); `Verifier.Parse(token) (Principal, error)` that PINS the accepted alg to `HS256` (reject `alg=none`/asymmetric confusion), verifies `iss` == configured issuer, checks `exp`, and maps every failure (bad sig / expired / malformed / wrong alg / wrong iss) to ONE sentinel error so the reason can't leak. Small leeway on `exp` for clock skew.
- [x] 1.3 Context plumbing: private context key + `WithPrincipal(ctx, Principal) ctx` + `PrincipalFromContext(ctx) (Principal, bool)`.
- [x] 1.4 Unit tests: issue→parse round-trip; reject tampered/expired/wrong-secret/`alg=none`; `expires_at == iat + TTL`; context set/get.

## 2. Config

- [x] 2.1 `config.go`: add `AUTH_JWT_SECRET` (`required:"true"`), `AUTH_JWT_TTL` (default e.g. `24h`), `AUTH_DEV_PASSWORD` (`required:"true"` — no default weak login), `AUTH_DEV_EMAIL` (may default, e.g. `dev@example.com`), all env + yaml. Keep `DEV_TENANT_ID`/`DEV_USER_ID` (now the login's issued principal).
- [x] 2.2 No config-dump line exists today (`config.go:76`); do NOT add one. Record the never-log invariant in the config comment block: `AUTH_JWT_SECRET` + `AUTH_DEV_PASSWORD` (alongside `OSS_*`) MUST be excluded if any config print is ever added. Fail-fast validate names the missing key.
- [x] 2.3 Config tests: missing `AUTH_JWT_SECRET` OR `AUTH_DEV_PASSWORD` → load error naming the key.

## 3. Login endpoint

- [x] 3.1 `auth_login.go`: `AuthHandlers` with `Issuer`, dev-credential config, logger; `Register(v1)` mounts `POST /auth/login`. Verify `{email,password}` constant-time (`subtle.ConstantTimeCompare`); on match issue a token for `DEV_TENANT_ID`/`DEV_USER_ID` and return `{token, expires_at, user:{id, tenant_id, email}}`; unknown email AND wrong password both → `401 invalid_credentials`; malformed body → `400 invalid_input`. Never log the password.
- [x] 3.2 Wire `AuthHandlers` into `ServerDeps` + `NewEngine` (registers on the shared `/api/v1` group) and `main.go`.
- [x] 3.3 Contract tests: valid → 200 + verifiable token + principal (no password field); wrong password / unknown email → 401 `invalid_credentials` (indistinguishable); malformed → 400; password never in body/log.

## 4. Bearer middleware + public allowlist

- [x] 4.1 Rewrite `authMiddleware()` to take the `Verifier`: skip the allowlist matched on `(method, c.FullPath())` — `GET /healthz`, `GET /readyz`, `GET /metrics`, `POST /api/v1/auth/login` (login public for `POST` only); else require `Authorization: Bearer <jwt>`, `Parse`, and on success `WithPrincipal` into `c.Request.Context()`; on any failure `httpapi.Error(c, 401, "unauthenticated", ...)` and abort.
- [x] 4.2 Wire the verifier into the chain in `server.go`/`main.go` (auth middleware still last in `e.Use(...)`, preserving documented order).
- [x] 4.3 Middleware tests: valid token → principal in context + handler runs; missing / non-Bearer / bad-sig / expired → `401 unauthenticated`, handler NOT run; each public route reachable with no token; `401` body is the `{code:"unauthenticated", ...}` envelope (pins the web's clear+redirect contract).

## 5. Migrate handlers + WS off the dev identity

- [x] 5.1 Replace `h.DevTenantID`/`h.DevUserID` with `PrincipalFromContext` in all 5 handler structs (`tasks.go`, `task_reads.go`, `task_cost_reads.go`, `task_control.go`, `artifact_reads.go`); fail closed (`500 internal_error`) if no principal present. Delete the `Dev*` fields.
- [x] 5.2 `ws/gateway.go`: replace the `?token=` presence gate with `verifier.Parse`; invalid/expired → existing `4001` close; resolved `Principal` becomes the connection identity (drop injected `Dev*`). Origin check still first.
- [x] 5.3 `main.go`: drop the `devTenant`/`devUser` stamping of handler/gateway structs (identity now flows via context/token); keep parsing `DEV_*` only for the login principal.
- [x] 5.4 Migrate existing handler/contract tests: seat a `Principal` in request context via a tiny test middleware (read/cost/control/artifact), preserving every owner-scoping assertion (two principals → disjoint visibility; unowned → `404`). Note: owner-agnostic endpoints (`GET /pricing`, `task_cost_reads.go:114`) need no per-principal isolation assertion — same response for every caller. Update WS tests: valid token registers with principal; invalid/expired → `4001`.

## 6. Gates & wrap-up

- [x] 6.1 From `api/`: `go vet ./...` ✓, `go test -race ./...` ✓, `golangci-lint run` ✓.
- [x] 6.2 `make sqlc` shows no diff (no query/schema change this round); `gofmt`/`goimports` clean on touched files.
- [x] 6.3 `make test-integration` (testcontainers; needs Docker) ✓ — covers the WS handshake + an authed REST path end-to-end.
- [x] 6.4 Update `api/README` + `.env` example with `AUTH_JWT_SECRET`/`AUTH_DEV_PASSWORD` and the dev `POST /auth/login` flow; add the `POST /api/v1/auth/login` row to the `docs/ARCHITECTURE.md` API route table (capability `api-auth`) — it has no such row today (AGENTS.md §1 doc-sync).
- [x] 6.5 `openspec validate add-api-auth-jwt --strict` (from repo root) ✓.
