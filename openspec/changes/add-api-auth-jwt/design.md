## Context

API auth is a documented stub. `authMiddleware()` (`internal/interfaces/http/middleware.go`) is `c.Next()` with a comment reserving the slot for "the `add-api-auth` proposal". Identity is a process-global constant: `main.go` parses `DEV_TENANT_ID`/`DEV_USER_ID` into `devTenant`/`devUser` and stamps them onto 5 handler structs (`TaskHandlers`, `TaskReadHandlers`, `TaskCostHandlers`, `TaskControlHandlers`, `ArtifactHandlers`) and the WS gateway; each handler uses `h.DevTenantID`/`h.DevUserID` as the caller. The middleware chain (`server.go`) is global via `e.Use(...)` ending in `authMiddleware()`; business routes hang off one `/api/v1` group. The WS gateway (`ws/gateway.go`) gates `?token=` presence only (close `4001`), and `realtime-gateway`'s spec explicitly says real per-user isolation is a future change. Envelope errors go through `httpapi.Error(c, status, code, message)` (`envelope.go`). There is **no `users`/`tenants` table** (migration `0002` defers them) and **no JWT/bcrypt dependency** in `go.mod`.

This change makes the token real and unblocks `add-web-auth-login`. The web side already sends `Bearer <token>` and handles `401 → clear+redirect`, so no web work is in scope.

## Goals / Non-Goals

**Goals:**
- A signed JWT issued at `POST /api/v1/auth/login` and validated on every protected request, with identity flowing from claims into request context.
- Replace the `Dev*` handler-field identity with `PrincipalFromContext`, preserving all owner-scoping behavior verbatim.
- Validate (not just presence-check) the WS `?token=`.
- Fail-fast, redacted JWT secret config.

**Non-Goals (deliberate MVP deferral — record per AGENTS.md §1):**
- **No `users`/`tenants` table, no password hashing store, no user CRUD.** Credentials are a single config-seeded dev principal. A real store is a later `add-api-user-store`.
- **No refresh tokens, no SSO/OIDC** (ARCHITECTURE §9 lists them; Post-MVP). The single short-lived access token is the whole story this round; the web re-logs-in on `401`.
- **No RBAC/roles** beyond the existing ownership checks.
- **No gateway (APISIX) integration** — auth runs in-process; the target architecture's edge auth is later.
- No web changes.

## Decisions

### D1 — Credential source: config-seeded dev principal, not a user table
Login verifies `{email, password}` against `AUTH_DEV_EMAIL`/`AUTH_DEV_PASSWORD` (config/env) and, on match, issues a token whose claims are the existing `DEV_TENANT_ID`/`DEV_USER_ID`. **Why:** a real user store (table + migration + repo + hashing + seeding) is a large, separable concern that would balloon this change well past the size budget and mix two capabilities. Config-seeded credentials keep the change focused on *the token boundary* — which is the thing the web is blocked on — while remaining honest (it's clearly a dev credential). The claims still carry real `(tenant_id, user_id)` UUIDs, so when a user store lands later, only the verification source changes; the middleware, context plumbing, and handlers are untouched.

**Alternative considered:** create `tenants`/`users` now with bcrypt hashes. Rejected for scope; tracked as `add-api-user-store`. Password compare is constant-time (`subtle.ConstantTimeCompare`) against the configured value; bcrypt is deferred with the store.

### D2 — JWT: HS256 with a single configured secret
Symmetric HS256 (one `AUTH_JWT_SECRET`) over asymmetric RS/ES. **Why:** one in-process issuer+verifier, no key distribution, simplest correct thing for MVP; matches `ARCHITECTURE`'s "JWT (短期)". Claims: `sub`=user_id, a `tid`=tenant_id, `iat`, `exp`, `iss`. Library: add `github.com/golang-jwt/jwt/v5` (the de-facto Go JWT lib) to `go.mod`. The signer lives in a small `pkg/auth` (or `internal/auth`) package: `Issuer.Issue(tenantID, userID uuid.UUID) (token string, expiresAt time.Time, err error)` and `Verifier.Parse(token string) (Principal, error)`, sharing the secret + TTL. `Parse` MUST pin the accepted algorithm to `HS256` (reject `alg=none` and any asymmetric-key confusion) and MUST verify `iss` equals the configured issuer (we sign `iss`, so we validate it rather than leave it decorative). It maps every failure (bad signature, expired, malformed, wrong alg, wrong issuer) to a single sentinel so callers can't leak the reason.

**Alternative considered:** PASETO. Rejected — `golang-jwt` matches the `?token=<jwt>` contract already named in `realtime-gateway` and ARCHITECTURE; no benefit to diverging for MVP.

### D3 — Principal in context, not handler fields
Introduce `type Principal struct { TenantID, UserID uuid.UUID }`, a private context key, `WithPrincipal(ctx, p)`, and `PrincipalFromContext(ctx) (Principal, bool)`. The auth middleware sets it; handlers replace `h.DevTenantID`/`h.DevUserID` with `p, _ := PrincipalFromContext(c.Request.Context())`. The `Dev*` struct fields and their `main.go` wiring are deleted. **Why context, not gin keys:** the domain/app layer already takes `context.Context`; a typed context accessor keeps identity flowing without gin leaking inward, and `PrincipalFromContext` is unit-testable without a gin request. A handler that somehow runs without a principal (programming error — the middleware guarantees one on protected routes) MUST fail closed (treat as `500 internal_error`, never as a zero-UUID owner).

### D4 — Public-route allowlist inside the global middleware
Keep `authMiddleware()` global in the `e.Use(...)` chain (preserves the documented chain order) but skip a fixed allowlist keyed on `(method, c.FullPath())`: `GET /healthz`, `GET /readyz`, `GET /metrics`, and `POST /api/v1/auth/login`. **Why `(method, path)` not path-only:** only the `POST` verb of `/auth/login` is public; keying on the route template (`c.FullPath()`) rather than the raw URL also prevents trailing-slash/query spoofing. **Why an allowlist over a separate protected group:** the chain order comment in `server.go` is load-bearing and the health/metrics routes are registered outside the `/api/v1` group; an allowlist is a 4-entry check that avoids restructuring route registration. The login route registers on the same `/api/v1` group as a new `AuthHandlers.Register(v1)` but is matched by the allowlist before the token check.

### D5 — WS validation reuses the same Verifier
`gateway.go` swaps its `c.Query("token") == ""` presence gate for `verifier.Parse(c.Query("token"))`; on error → existing `closeAuthMissing` (`4001`); on success the resolved `Principal` becomes the connection identity (replacing the injected `DevTenantID`/`DevUserID`). Origin allowlist still runs first. The subscribe-time ownership check is unchanged — it now reads the connection's real principal.

### D6 — Tests migrate from `Dev*` fields to issued tokens
Handler/contract tests currently build handlers with `DevTenantID`/`DevUserID` and assert owner-scoping. They migrate to either (a) seating a `Principal` directly in the request context via a tiny test middleware, or (b) issuing a real token from a test `Issuer` and exercising the full middleware. Read/cost/control/artifact tests use (a) for focus; a dedicated `auth_middleware_test.go` + `auth_login_test.go` use (b) for the end-to-end token path. New tests: login happy/invalid/malformed; middleware valid/missing/expired/bad-sig/public-allowlist; WS valid/invalid/expired; config fail-fast on missing secret + redaction.

## Risks / Trade-offs

- **[Change size > 500-line budget (AGENTS.md §7)]** Touching 5 handlers + WS + all their tests is wide → Mitigation: the *net* handler edit is mechanical (one identity-read line each); the genuinely new code (jwt pkg, middleware, login, config) is small. Tasks are ordered so the change can be split at a clean seam if review prefers: **(P1)** jwt pkg + config + `Principal`/context + middleware + login + their tests (no handler churn yet, middleware still allowlists but handlers keep reading a context principal seeded by a shim), **(P2)** migrate the 5 handlers + WS off `Dev*` and delete the fields. If kept as one change, call out the split option to the reviewer.
- **[Config-seeded credentials look like "real auth" but aren't a user store]** → Mitigation: documented as a Non-Goal + D1; the dev credential is clearly named; claims carry real UUIDs so the upgrade path is clean.
- **[Secret leakage]** A logged or dumped `AUTH_JWT_SECRET` / `AUTH_DEV_PASSWORD` is catastrophic → Mitigation: both `required:"true"`, never echoed. NOTE the repo has no config-dump line today (`config.go:76`), and the `OSS_*` keys are likewise never actually printed — so this is a standing invariant, not reuse of an existing redaction path; if any config print is ever added it must exclude these. Tests assert the secret does not appear in any login/error response body.
- **[Rollout locks out every existing client until the web login ships]** Flipping the stub to real validation means: the instant the new API deploys, every in-flight or cached **REST** call without a valid token gets `401`, and every existing **WS** long-connection gets `4001` on its next (re)connect — and tokens can ONLY come from the new `/auth/login`. So the window "API deployed, `add-web-auth-login` not yet shipped" is a site-wide outage. → Mitigation: this change and `add-web-auth-login` MUST be coordinated on rollout order (ship the web login alongside or before enabling enforcement), OR operators use the seeded dev credential + `/auth/login` to mint a working token for local/dev. Existing WS connections receiving `4001` on reconnect is expected (the client already treats 4001 as auth-expired → redirect to login). Document the dev login in the api README/`.env`.
- **[`401` shape must match the web's expectation]** The web triggers clear+redirect on a `401` envelope with `code:"unauthenticated"` → Mitigation: middleware uses `httpapi.Error(c, 401, "unauthenticated", ...)`; a contract test pins the body shape.
- **[Clock skew on `exp`]** Tight TTL + skew could reject fresh tokens → Mitigation: a bounded default TTL (a day) dwarfs skew; optionally a small leeway in `Parse`.

## Migration Plan

1. Land jwt pkg + config + `Principal`/context + middleware + login (+tests). 2. Migrate handlers + WS off `Dev*`; delete fields and their `main.go` wiring (+test migration). 3. Update the api README / `.env` example with `AUTH_JWT_SECRET`/`AUTH_DEV_PASSWORD` and the dev login flow, AND add the `POST /api/v1/auth/login` row to the `docs/ARCHITECTURE.md` API route table (capability `api-auth`) — the table has no such row and §9 names no endpoint, so per AGENTS.md §1 the doc is synced here. Coordinate the enabling rollout with `add-web-auth-login` (see Risks). Rollback = revert the change; the stub returns. No DB migration, so rollback is code-only.

## Open Questions

- None blocking. The user-store, refresh tokens, and SSO are explicitly deferred (Non-Goals); raise `add-api-user-store` when multi-user is needed.
