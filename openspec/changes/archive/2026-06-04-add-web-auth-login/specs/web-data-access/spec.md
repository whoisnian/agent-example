## MODIFIED Requirements

### Requirement: 401 Handling

The HTTP client SHALL, by default, intercept HTTP 401 responses, clear the session from the auth store (token and `user`), and trigger a navigation to `/login`. The original promise MUST still reject with `ApiError(code:"unauthenticated", status:401)` so callers can choose to suppress their own UI.

A caller MAY opt out of this interception by passing `apiFetch(path, { interceptUnauthorized: false })`. When opted out, a 401 MUST NOT clear the session and MUST NOT navigate; instead the promise MUST reject with an `ApiError` carrying the response envelope's own `code`, `message`, and `data` (e.g. `code:"invalid_credentials"`) with `status:401`. The opt-out exists so the login flow can surface credential errors inline; every other caller retains the default behavior.

#### Scenario: 401 clears session and redirects (default)
- **WHEN** any `apiFetch` call without an opt-out receives HTTP 401
- **THEN** the auth store's token and `user` MUST be cleared, the router MUST navigate to `/login`, AND the promise MUST reject with `code:"unauthenticated"`

#### Scenario: Opted-out 401 surfaces the envelope code
- **WHEN** an `apiFetch` call passes `{ interceptUnauthorized: false }` and receives HTTP 401 with `code:"invalid_credentials"`
- **THEN** the session MUST NOT be cleared, the router MUST NOT navigate, AND the promise MUST reject with an `ApiError` whose `code` is `"invalid_credentials"` and `status` is `401`

### Requirement: Zustand Store Pattern

The project SHALL standardize on per-feature Zustand stores under `src/features/<name>/store.ts`. Each store file MUST:
- export a `useXxxStore` hook,
- type its state and actions explicitly,
- avoid storing server-fetched entities (those belong in React Query cache).

A `useAuthStore` MUST exist with shape `{ token: string | null; user: AuthUser | null; setSession(token: string, user: AuthUser): void; logout(): void }`, where `AuthUser = { id: string; tenant_id: string; email: string }`. It MUST persist `token` and `user` to `localStorage` under key `auth.token`. `setSession` MUST be the only writer of a non-null session; `logout()` MUST clear both `token` and `user`. (The authenticated identity is the login-issued principal, not arbitrary server-fetched task data, so persisting it here does not violate the "no server entities in Zustand" convention.)

Because the persisted shape changed from the prior token-only blob, the store MUST set a persist `version` and provide a `migrate` that maps any legacy (pre-version) persisted state to **no session** (`token: null`, `user: null`). Legacy tokens predate server-side JWT enforcement and cannot be honored, so a returning user with a legacy blob MUST be treated as logged out (and re-login), never rehydrated into a half-populated `{ token, user: undefined }` state.

A scaffold-level `useUiStore` MUST exist with shape `{ toasts: Toast[]; pushToast(t: Omit<Toast,"id">): void; dismissToast(id: string): void }`.

#### Scenario: Session persists across reloads
- **WHEN** `setSession("abc", { id, tenant_id, email })` is called and the page reloads
- **THEN** on the next mount `useAuthStore.getState().token` MUST equal `"abc"` and `useAuthStore.getState().user` MUST equal the stored `AuthUser`

#### Scenario: Logout clears the persisted session
- **WHEN** a session is stored and `logout()` is called
- **THEN** both `token` and `user` MUST be `null` and the persisted `auth.token` entry MUST no longer hold a session

#### Scenario: Legacy token-only blob migrates to logged-out
- **WHEN** `localStorage["auth.token"]` holds a pre-version blob with a `token` but no `user`
- **THEN** after rehydration `useAuthStore.getState().token` and `.user` MUST both be `null` (the legacy session is dropped, forcing re-login)

#### Scenario: Server entity not stored in Zustand
- **WHEN** code review encounters a Zustand store holding server-fetched task data
- **THEN** the change MUST be rejected (enforced by review for the scaffold; documented as a convention)
