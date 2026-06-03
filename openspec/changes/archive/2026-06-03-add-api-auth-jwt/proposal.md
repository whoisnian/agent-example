## Why

API authentication is a pass-through stub: `authMiddleware()` calls `c.Next()` unconditionally, the `Authorization` header is ignored, and every handler hardcodes `DevTenantID` / `DevUserID` as the caller identity (`main.go` wires these into 5 handler structs + the WS gateway). There is no login endpoint and no token validation, so the web client's `Bearer <token>` is discarded and a forged/empty token is indistinguishable from a real one. This blocks the next web change (`add-web-auth-login`) — a login form has nothing real to call — and is the standing MVP gap called out by the middleware's own comment ("Real auth arrives via the `add-api-auth` proposal"). This change makes the token real: the API issues a signed JWT at login and validates it on every request, deriving identity from its claims.

## What Changes

- **NEW `POST /api/v1/auth/login`** — verifies credentials and returns a signed JWT plus its expiry and the principal. Public (un-authenticated) route.
- **Real `authMiddleware`** — validates the `Authorization: Bearer <jwt>` (signature + expiry), injects an authenticated `Principal{tenant_id, user_id}` into the request context, and returns `401 unauthenticated` on a missing/malformed/invalid/expired token. A small allowlist (`/healthz`, `/readyz`, `/metrics`, `/api/v1/auth/login`) stays public.
- **Identity from claims, not config** — handlers read the caller from `PrincipalFromContext(ctx)` instead of struct `DevTenantID`/`DevUserID` fields; the `Dev*` handler fields are removed. Owner-scoping behavior (the `task-read-api` 404 rules etc.) is unchanged — only the *source* of `tenant_id`/`user_id` changes from a global constant to the per-request token.
- **WS token is validated, not just present** — `GET /api/v1/ws?token=<jwt>` now verifies the JWT and resolves the connection's principal from its claims; a missing/invalid/expired token still closes with `4001`. **BREAKING** at the spec level for `realtime-gateway` (presence-only → validated).
- **JWT config** — `AUTH_JWT_SECRET` (required, redacted, fail-fast like the OSS keys), `AUTH_JWT_TTL`, and a config-seeded dev principal (`AUTH_DEV_EMAIL` / `AUTH_DEV_PASSWORD` → the existing `DEV_TENANT_ID`/`DEV_USER_ID`) as the MVP credential source.

## Capabilities

### New Capabilities
- `api-auth`: JWT issuance via login, Bearer-token validation + principal injection, identity-from-claims for ownership, WS token validation, and JWT/credential configuration.

### Modified Capabilities
- `realtime-gateway`: the **WebSocket Endpoint and Connection Lifecycle** requirement changes from "gates token *presence* only; identity resolves through the `DevTenantID`/`DevUserID` stub" to "validates the JWT and resolves the connection's principal from its claims; invalid/expired closes `4001`". The subscribe-time ownership boundary is unchanged; it now resolves against a real per-connection principal.
- `task-cost-api`: the **Owner-Scoped Reads Hide Unowned Resources** requirement writes the identity source into its contract ("the MVP dev-mode middleware fills these from env", `spec.md:183`); it is reworded to "the authenticated `Principal{tenant_id, user_id}` from the token claims". Scoping behavior is unchanged — only the identity *source* moves from env to the per-request token. (`task-read-api`, `task-write-api`, `task-control-api`, and `artifacts-api` do NOT pin the identity source in their contracts, so they need no delta — verified by sweep.)

## Impact

- **Code (api only, Go)**: new JWT helper + `Principal` context (`pkg/auth` or `internal/.../auth`); new `auth_login.go` handler; rewritten `authMiddleware` + a public-route allowlist; `main.go` wiring (parse secret/dev principal, drop `Dev*` handler fields); identity migration across the 5 HTTP handler structs (`tasks`, `task_reads`, `task_cost_reads`, `task_control`, `artifact_reads`) and the WS gateway, plus their unit/contract tests (which currently set `Dev*` fields — they move to injecting a principal/issuing a test token).
- **Config**: new keys (`AUTH_JWT_SECRET` required, `AUTH_JWT_TTL`, `AUTH_DEV_EMAIL`, `AUTH_DEV_PASSWORD` required). The repo currently has **no** config-dump/log line (`config.go:76` says so, and the OSS keys are likewise never actually printed); the secret + dev password are a never-log invariant — if any config print is ever added it MUST exclude them (alongside the `OSS_*` credentials).
- **Docs**: `docs/ARCHITECTURE.md` has no `/auth/login` route-table row and §9 lists only "JWT/Refresh/SSO" with no endpoint; per AGENTS.md §1 this change adds the row (the new public route is the landing of §9 "JWT (短期)").
- **No DB change**: there is still **no `users`/`tenants` table** (migration `0002` defers them); the credential source is config-seeded and issues a token for the existing dev principal. A real user store / password hashing / refresh tokens / SSO are **out of scope** (see design — deliberate MVP deferral, `add-api-user-store` later).
- **Unblocks** `add-web-auth-login` (web): a real `/api/v1/auth/login` to call, and a token the API actually enforces.
- **No web change here**: `apiFetch` already sends `Bearer <token>` and handles `401 → clear token + redirect /login`; the web login UI is the separate follow-up.
