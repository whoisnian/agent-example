## 1. Transport: 401 opt-out (web-data-access)

- [ ] 1.1 Add `interceptUnauthorized?: boolean` (default `true`) to `ApiFetchInit` in `services/http.ts`; thread it through `apiFetch`.
- [ ] 1.2 In the 401 branch: when `interceptUnauthorized === false`, parse the envelope and reject with an `ApiError` carrying the envelope `code`/`message`/`data` + `status:401`, without clearing the session or navigating. Default path unchanged (clear + redirect + `unauthenticated`).
- [ ] 1.3 Repoint `handleUnauthorized` (`http.ts:101`) at the store's `logout()` (replacing the removed `setToken(null)`).
- [ ] 1.4 Repoint the realtime `4001` close path (`ws.ts:294`) at `logout()` (replacing `setToken(null)`). **(B1)**
- [ ] 1.5 Update `http.test.ts`: `setToken(...)` at lines 45/73 → `setSession(...)`/`setState`; add cases — default 401 still clears session + navigates; opted-out 401 surfaces `invalid_credentials` and does neither. **(B2)**

## 2. Auth store: token + user + migrate (web-data-access)

- [ ] 2.1 Extend `features/auth/store.ts` to `{ token, user, setSession(token,user), logout() }`; add `AuthUser = {id, tenant_id, email}`; persist `token` + `user` under `auth.token`. Remove `setToken`.
- [ ] 2.2 Add persist `version: 1` + a `migrate` that maps any legacy (version 0 / unversioned) blob to `{ token: null, user: null }`. **(S4)**
- [ ] 2.3 Update `features/auth/store.test.ts`: session persists across reload; `logout()` clears both `token` and `user` and the persisted entry; legacy token-only blob migrates to logged-out.

## 3. Auth feature data layer (web-auth)

- [ ] 3.1 `features/auth/types.ts`: `LoginRequest {email,password}`, `AuthUser`, `LoginResponse {token, expires_at, user}`.
- [ ] 3.2 `features/auth/api.ts`: `login(body)` → `apiFetch<LoginResponse>("/api/v1/auth/login", { method:"POST", body: JSON.stringify(body), toastOnError:false, interceptUnauthorized:false })`. **(B3 — `apiFetch` does NOT serialize; stringify at the call site like `tasks/api.ts`.)**
- [ ] 3.3 `features/auth/mutations.ts`: `useLoginMutation` with `meta:{silent:true}`, `retry:false` (defensive — see S1), `mutationFn: login`.

## 4. Login page (web-auth)

- [ ] 4.1 Create `routes/LoginPage.tsx`: email + password fields + submit (existing primitives + design tokens); submit disabled while pending.
- [ ] 4.2 On success: `setSession(token, user)` then navigate to the safe internal `from` path (begins with `/`, 2nd char neither `/` nor `\`) else `/tasks`. **(S5)**
- [ ] 4.3 Inline error UX: `invalid_credentials` → one fixed indistinct message; `invalid_input` → generic input message; otherwise `ApiError.message`.
- [ ] 4.4 Swap `router.tsx` `/login` element to `LoginPage`; delete `placeholders/LoginPlaceholder.tsx`.
- [ ] 4.5 Update `routes/router.test.tsx`: import/use `LoginPage` (drop the `LoginPlaceholder` import + `placeholder-login` testid at lines 15/30). **(B2)**
- [ ] 4.6 Add `LoginPage` tests: success stores session + redirects to `from`; open-redirect `from` (incl. `/\evil`) falls back to `/tasks`; `invalid_credentials` shows indistinct message with no redirect; pending disables submit; no global toast on error.

## 5. Shell: identity + logout (web-auth)

- [ ] 5.1 In `components/layout/TopBar.tsx`, replace the `user-area` slot's literal `"user"` with `user?.email` (tolerate null `user`) + a logout button. **(N1)**
- [ ] 5.2 Logout button → `useAuthStore.logout()` then navigate `/login` (replace).
- [ ] 5.3 Add tests: shell renders the logged-in email; null `user` renders no identity text without error; logout clears the session and gating sends `/tasks` → `/login`.

## 6. Mocks + gates

- [ ] 6.1 Add an MSW handler for `POST /api/v1/auth/login` in `test/mocks/handlers.ts`: default returns `200` for the configured dev credentials; the `invalid_credentials` (401) and `invalid_input` (400) cases are installed per-test via `server.use()` overrides (matching the established override pattern). **(S6)**
- [ ] 6.2 Run web gates from `web/`: `npm run typecheck`, `npm run lint` (0 warnings), `npm run test`; `npx prettier --write` only on touched files (do NOT run the repo `format` script — it globs all of `src/**`).
- [ ] 6.3 `openspec validate add-web-auth-login --strict` clean.
