## ADDED Requirements

### Requirement: HTTP Envelope Client

The project SHALL expose a typed `apiFetch<T>(path, init?)` function in `src/services/http.ts` that wraps `fetch` and:
- prefixes `path` with the configured API base URL (`VITE_API_BASE_URL`),
- injects `Authorization: Bearer <token>` when a token is present in the auth store,
- injects a generated `X-Request-Id` header (UUIDv4),
- propagates `AbortSignal` from `init`,
- parses the JSON body as `{code, message, data, trace_id}` (matching API's `api-bootstrap` "Unified Response Envelope"),
- resolves to `data` typed as `T` when `code === 0`,
- throws a typed `ApiError(code, message, traceId, status)` otherwise.

Default timeout is 30 seconds; callers may override via `init.timeoutMs`. Timeout MUST be implemented via `AbortController`, not a wall-clock race that leaks the underlying request.

#### Scenario: Success envelope unwraps to data
- **WHEN** `apiFetch<{ok:boolean}>("/api/v1/__scaffold/echo", {method:"POST", body:'{"ok":true}'})` is called and the response is `{code:0, message:"ok", data:{ok:true}, trace_id:"t-1"}`
- **THEN** the promise MUST resolve to `{ok: true}` typed as `{ok:boolean}`

#### Scenario: Business error throws ApiError
- **WHEN** the server returns `{code:"active_version_exists", message:"...", trace_id:"t-2"}` with HTTP 409
- **THEN** the promise MUST reject with `ApiError` whose `code === "active_version_exists"`, `status === 409`, and `traceId === "t-2"`

#### Scenario: Timeout aborts request
- **WHEN** `apiFetch` is called with `timeoutMs: 100` against a slow endpoint
- **THEN** the underlying `fetch` MUST be aborted via `AbortController`, and the promise MUST reject with `ApiError(code:"timeout", status:0)`

#### Scenario: Auth header injected when token present
- **WHEN** the auth store holds a non-empty token and `apiFetch` is called
- **THEN** the outbound request MUST carry `Authorization: Bearer <token>`

### Requirement: 401 Handling

The HTTP client SHALL intercept HTTP 401 responses, clear the auth token from the store, and trigger a navigation to `/login`. The original promise MUST still reject with `ApiError(code:"unauthenticated", status:401)` so callers can choose to suppress their own UI.

#### Scenario: 401 clears token and redirects
- **WHEN** any `apiFetch` call receives HTTP 401
- **THEN** the token in the auth store MUST be cleared, the router MUST navigate to `/login`, AND the promise MUST reject with `code:"unauthenticated"`

### Requirement: React Query Configuration

A single `QueryClient` SHALL be created at app startup with defaults: `staleTime: 30_000`, `gcTime: 5 * 60_000`, `retry: (failureCount, error) => error.code !== "unauthenticated" && error.status !== 409 && failureCount < 2`, `refetchOnWindowFocus: false`. A global `onError` MUST forward errors to the toast system unless the calling hook opts out via `meta: { silent: true }`.

The `QueryClient` MUST be exposed via `<QueryClientProvider>` at the application root, and `<ReactQueryDevtools>` MUST mount only when `import.meta.env.DEV` is true.

#### Scenario: 409 is not retried
- **WHEN** a query throws `ApiError(status:409)`
- **THEN** React Query MUST NOT retry, AND the rejection MUST propagate to the caller

#### Scenario: Silent mode skips toast
- **WHEN** a query is registered with `meta: { silent: true }` and fails
- **THEN** no toast MUST be emitted by the global error handler

### Requirement: Zustand Store Pattern

The project SHALL standardize on per-feature Zustand stores under `src/features/<name>/store.ts`. Each store file MUST:
- export a `useXxxStore` hook,
- type its state and actions explicitly,
- avoid storing server-fetched entities (those belong in React Query cache).

A scaffold-level `useAuthStore` MUST exist with shape `{ token: string | null; setToken(t: string | null): void }` and MUST persist `token` to `localStorage` under key `auth.token`.

A scaffold-level `useUiStore` MUST exist with shape `{ toasts: Toast[]; pushToast(t: Omit<Toast,"id">): void; dismissToast(id: string): void }`.

#### Scenario: Auth token persists across reloads
- **WHEN** `setToken("abc")` is called and the page reloads
- **THEN** on the next mount `useAuthStore.getState().token` MUST equal `"abc"`

#### Scenario: Server entity not stored in Zustand
- **WHEN** code review encounters a Zustand store holding server-fetched task data
- **THEN** the change MUST be rejected (enforced by review for the scaffold; documented as a convention)

### Requirement: Typed Error Envelope

The `ApiError` class SHALL be the only error type thrown by `apiFetch`. Its `code` field SHALL be typed as a string-literal union expandable per feature, with the scaffold-included subcodes: `"timeout" | "network_error" | "unauthenticated" | "internal_error" | string` (open-ended). Application code SHALL narrow via `instanceof ApiError`.

#### Scenario: Network failure surfaces ApiError
- **WHEN** `fetch` rejects with a `TypeError: Failed to fetch`
- **THEN** `apiFetch` MUST reject with `ApiError(code:"network_error", status:0)`, not the underlying `TypeError`
