## Context

`add-api-auth-jwt` (archived) made the API enforce HS256 JWT: every `/api/v1/*` business route returns `401 unauthenticated` without a valid Bearer token, and `GET /api/v1/ws` closes `4001`. The web client still ships the bootstrap-era `LoginPlaceholder` (paste any non-empty string → store as token). That token is not a real JWT, so the app is now end-to-end broken against the real backend.

Relevant current state:
- `useAuthStore` (`features/auth/store.ts`) holds `{ token, setToken }`, persisted to `localStorage["auth.token"]`. Read synchronously by `apiFetch` (Bearer injection) and the realtime client (`?token=`).
- `apiFetch` (`services/http.ts`) **intercepts every HTTP 401**: clears the token, calls the injected navigator → `/login`, and rejects with a synthetic `ApiError(code:"unauthenticated")` — discarding the response envelope.
- `RequireAuth` redirects unauthenticated routes to `/login` with `state:{ from: location.pathname }`.
- The API login contract (from `api-auth`): `POST /api/v1/auth/login {email,password}` → `200 {token, expires_at, user:{id,tenant_id,email}}`; `401 invalid_credentials` (email/password indistinguishable); `400 invalid_input`.

Feature-slice conventions (from `tasks`/`costs`/`artifacts`): `types.ts` + thin `api.ts` `apiFetch` wrappers + React Query `mutations.ts` with `meta:{silent:true}` for inline-error pages, paired with transport `toastOnError:false`.

## Goals / Non-Goals

**Goals:**
- A real email/password login that obtains and stores a backend-issued JWT, unblocking every authenticated page.
- Surface `invalid_credentials` / `invalid_input` inline on the login page (no toast, no redirect loop).
- Persist the authenticated identity (`user`) so the shell can show who is logged in and logout has something to clear.
- A minimal logout that clears the session and returns to `/login`.

**Non-Goals:**
- Token refresh / silent re-auth / expiry countdown (the API issues a fixed-TTL token with no refresh endpoint).
- "Remember me", multi-account switching UI, SSO, registration, password reset — all gated on a real user store (`add-api-user-store`).
- Re-validating a persisted token on app boot (the first authenticated `apiFetch` already self-heals via the 401 → logout path).
- Changing the realtime client; it already reads `token` from the same store.

## Decisions

### D1 — New `features/auth/` data layer; `LoginPage` replaces `LoginPlaceholder`
Add `features/auth/types.ts` (`LoginRequest`, `LoginResponse`, `AuthUser`), `api.ts` (`login()` wrapper), `mutations.ts` (`useLoginMutation`). A new `routes/LoginPage.tsx` renders the form and owns the submit flow; `placeholders/LoginPlaceholder.tsx` is deleted and `router.tsx` points `/login` at `LoginPage`. Mirrors the existing slice layout rather than inventing a new shape.

### D2 — Store grows to `{ token, user }`; single clear path = `logout()`
`useAuthStore` becomes `{ token, user, setSession(token, user), logout() }`, persisting **both** `token` and `user` under the unchanged key `auth.token`. `setSession` is the only writer; `logout()` is the only clearer (nulls both). The legacy `setToken` is removed; it has **two** non-test production callers — `apiFetch`'s 401 handler (`http.ts:101`) and the realtime client's `4001` auth-expiry path (`ws.ts:294`) — both of which now call `logout()` (clearing the whole session is a strict superset of "clear the token"). The test callers (`http.test.ts:45,73`) move to `setSession`/`setState`, and `store.test.ts` is updated. (See B1/B2.)
- *Alternative — token-only store, discard `user`*: rejected per product decision (we want identity display + a meaningful logout), and the `user` is returned for free.

**Persist migration.** The old store persisted `{ token }` only (no `version`). Bump the persist `version` to `1` with a `migrate` that maps any legacy (version 0 / unversioned) blob to **no session** (`{ token: null, user: null }`). Pre-`add-api-auth-jwt` tokens are placeholder garbage that the API will reject anyway, so forcing those returning users to re-login is correct and avoids a half-rehydrated `{ token, user: undefined }` state. (See S4.)

### D3 — Transport opt-out: `apiFetch(path, { interceptUnauthorized: false })`
The login endpoint's `401 invalid_credentials` is a **domain** error, not a session expiry, yet `apiFetch` currently force-converts every 401 into `unauthenticated` + clear + redirect. Add an `ApiFetchInit` flag `interceptUnauthorized` (default `true`). When `false`, a 401 is parsed like any other error envelope — the promise rejects with an `ApiError` carrying the envelope's real `code`/`message`/`data` and `status:401`, and the store/navigator are left untouched. Login is the only caller that opts out.
- *Alternative — raw `fetch` inside `login()`*: rejected; loses base-URL prefixing, `X-Request-Id`, timeout/abort, and envelope parsing.
- *Alternative — branch on `envelope.code` inside `apiFetch`*: rejected; the transport layer must not enumerate domain error codes.

### D4 — Login mutation silences both toast layers; relies on mutation `retry:false`
`useLoginMutation` uses `meta:{ silent:true }` (React Query cache `onError`) and its `api.login()` passes `toastOnError:false` (transport) — the two independent toast surfaces both must be muted so the login page is the single error surface. Consistent with the create/iterate/control precedent. Note the cache's retry rule (`query-client.ts`) only exempts `code === "unauthenticated"` / `status === 409`; a credential `401 invalid_credentials` would *not* be exempt — but it is safe because **mutations default to `retry:false`**, and login is a mutation. We set `retry:false` explicitly on the mutation to pin this (so a future move to a query can't silently start retrying credential failures). (See S1.)

### D5 — Indistinguishable credential error
On `ApiError(code:"invalid_credentials")` the page renders one fixed message (e.g. "Incorrect email or password") regardless of which field was wrong, mirroring the API's deliberate email/password indistinguishability. `invalid_input` (400) renders a generic "check your input" message. Any other error (network/timeout/5xx) falls back to its `ApiError.message`. No `ApiErrorCode` change is needed: the union is open-ended (`envelope.ts`), so comparing `err.code === "invalid_credentials"` type-checks without narrowing the frozen union. (See S2.)

### D6 — Redirect-back, guarded
On success, navigate to `location.state.from` when it is a safe **internal absolute path**, else `/tasks`. Safe = begins with `/` AND its second character is neither `/` nor `\` (rejecting `//evil.example` and the protocol-relative-backslash trick `/\evil.example`). `RequireAuth` only ever stashes `location.pathname`, so this guard is defense-in-depth against a hand-crafted `<Link state>` / `navigate(_, {state})`, not the normal flow. (See S5.)

### D7 — Logout + identity in the shell
The shell's existing `TopBar` already exposes a right-aligned `user-area` slot (currently the literal `"user"` placeholder, `TopBar.tsx`). Replace that slot's content with the logged-in `user.email` plus a logout button → `useAuthStore.logout()` then navigate `/login` (replace). `RootLayout` only composes `<TopBar/>` (no injection slot), so the control lives in `TopBar`, not `root-layout.tsx`. The display MUST tolerate `user == null` (render nothing for the identity text) so a half-rehydrated or post-logout render never dereferences a null `user`. Kept minimal: no dropdown/menu, just text + button using existing primitives and design tokens. (See N1/S4.)

### D8 — Stale Bearer on the login request is harmless
`apiFetch` still attaches `Authorization` if a stale token sits in the store when login is submitted. The login route is on the API's public allowlist (exempt from the Bearer middleware), so it is ignored. No special-casing needed.

## Risks / Trade-offs

- **Open redirect via `from`** → mitigated by D6's internal-path guard; covered by a test.
- **Persisting `user` (email) in `localStorage`** → email is non-secret and the JWT already lives there; acceptable for MVP, no new exposure class.
- **Returning user with a legacy `{token}`-only persisted blob** → handled by the persist `version:1` + `migrate` (D2): the legacy session is dropped, the user re-logs in. The shell's null-`user` tolerance (D7) is the belt-and-suspenders guard.
- **Opt-out misused on other calls** → default stays `true`; only `login()` sets it `false`, asserted by a transport test that the default 401 still clears+redirects.
- **No boot-time token validation** → a stale/expired persisted token lets `RequireAuth` admit the user for one render; the first `apiFetch` 401 immediately runs `logout()` + redirect. Acceptable; avoids a blocking validation request on every load.

## Migration Plan

Pure web change; the API contract is already live. Order: ship anytime after `add-api-auth-jwt` (done). The only client-side migration is the zustand persist `version:1` bump (D2) that clears legacy `{token}`-only sessions — returning users simply re-login. Rollback = revert the web commit(s); the API keeps enforcing regardless, and the old placeholder could not mint a valid token anyway.

## Open Questions

None — the two product forks (logout scope, store `user`) were resolved with the user before drafting.
