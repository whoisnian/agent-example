## Why

`add-api-auth-jwt` made the API enforce real JWT auth: every business route now rejects tokenless requests with `401`, and `GET /api/v1/ws` closes `4001`. The web client still ships the `LoginPlaceholder` scaffold ("paste any non-empty token"), which can no longer produce a token the backend accepts. The app is therefore unusable against the real API until the web side performs a genuine `POST /api/v1/auth/login` and stores the issued JWT. This change is the web counterpart to the now-archived API auth work.

## What Changes

- **Replace** `LoginPlaceholder` with a real `LoginPage`: email + password form that calls `POST /api/v1/auth/login`, stores the returned JWT, and enters the app.
- New `features/auth/` data layer: `types.ts` (login request/response mirroring the API contract), `api.ts` (`login()` wrapper), `mutations.ts` (`useLoginMutation`).
- **Extend `useAuthStore`** from `{token}` to `{token, user}` — persist the authenticated `{id, tenant_id, email}` so the shell can show "logged in as …" and logout has something to clear. Add `setSession(token, user)` and `logout()` actions; remove `setToken` (its two callers — `http.ts` 401 and `ws.ts` 4001 — switch to `logout()`). Bump the persist `version` with a `migrate` that clears legacy `{token}`-only blobs.
- **Redirect-back**: on successful login, navigate to the `from` path that `RequireAuth` stashes in router state (fallback `/tasks`).
- **Minimal logout**: a logout control in the app shell (`RootLayout` header) that clears the session and redirects to `/login`.
- **Transport: distinguish credential-401 from session-401.** `apiFetch` currently intercepts *every* HTTP 401 — clears the token, redirects to `/login`, and forces `code:"unauthenticated"`, masking the envelope. Add an opt-out so the login call surfaces the real `invalid_credentials` (401) / `invalid_input` (400) inline **without** clearing state or redirecting. Default behavior for all other calls is unchanged.

## Capabilities

### New Capabilities
- `web-auth`: the web client's authentication flow — the login page and its credential-error UX, session storage (token + user identity) and its persistence, redirect-back after login, and logout.

### Modified Capabilities
- `web-data-access`: the `401 Handling` requirement gains a documented opt-out so a caller (login) can receive a 401 envelope inline instead of triggering the global clear-session + redirect; the `Zustand Store Pattern` requirement's `useAuthStore` shape grows from `{token}` to `{token, user}` with `setSession`/`logout` actions, persisting both under `auth.token` with a `version`/`migrate` that clears legacy token-only blobs.
- `web-bootstrap`: the `Route Skeleton` requirement's `/login` row changes from the `LoginPlaceholder` scaffold to the real `LoginPage`, the "no token validation against the server" caveat is removed (login now validates against the API), and the table is corrected to the routes actually rendered today (`TaskList`/`TaskCreate`/`TaskDetail`/`CostDashboard`, which drifted from the frozen scaffold names).

## Impact

- **Code**: `web/src/features/auth/` (new `types.ts`/`api.ts`/`mutations.ts`, extended `store.ts` with persist `migrate`); `web/src/routes/LoginPage.tsx` (replaces `placeholders/LoginPlaceholder.tsx`); `web/src/router.tsx` (route element swap); `web/src/services/http.ts` (401 opt-out flag + `logout()`); `web/src/services/ws.ts` (`setToken`→`logout`); `web/src/components/layout/TopBar.tsx` (`user-area` slot → identity + logout); MSW handler/fixtures for `/auth/login`; test updates to `http.test.ts`, `router.test.tsx`, `store.test.ts`.
- **Contract consumed**: `api-auth` `POST /api/v1/auth/login` → `{token, expires_at, user{id,tenant_id,email}}`; `401 invalid_credentials`, `400 invalid_input`. No API change.
- **Behavioral**: enforcement is already live, so this unblocks all authenticated pages end-to-end. No data-model, MQ, or worker impact.
