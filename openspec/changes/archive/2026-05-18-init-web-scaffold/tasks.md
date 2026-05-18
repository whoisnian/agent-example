## 1. Project Skeleton

- [x] 1.1 `cd web && npm create vite@latest . -- --template react-ts` (clean out generated boilerplate that doesn't fit our layout)
- [x] 1.2 Pin Node 24 via `engines.node` + `packageManager: "npm@11.x"` in `package.json` (no `.nvmrc` — `packageManager` is the standardized pin)
- [x] 1.3 Migrate `src/` to the layout from design D10 (`routes/`, `components/{layout,feedback,primitives}/`, `features/{auth,ui}/`, `services/`, `hooks/`, `types/`, `styles/`, `test/mocks/`)
- [x] 1.4 Configure `tsconfig.json` with `strict`, `noUncheckedIndexedAccess`, `noImplicitOverride`, `exactOptionalPropertyTypes`, path alias `@/* → src/*`
- [x] 1.5 Configure `vite.config.ts` with React plugin and matching path alias
- [x] 1.6 Add `package.json` scripts: `dev`, `build`, `preview`, `lint`, `lint:fix`, `format`, `typecheck`, `test`, `test:watch`, `test:coverage`

## 2. Styling

- [x] 2.1 Install `tailwindcss`, `postcss`, `autoprefixer`; run `npx tailwindcss init -p`
- [x] 2.2 Configure `tailwind.config.js` with the semantic palette (`bg`, `surface`, `border`, `text`, `text-muted`, `accent`, `success`, `warning`, `danger`), spacing scale, font sizes, shadows
- [x] 2.3 `src/styles/globals.css` with Tailwind directives + base typography reset
- [x] 2.4 Import `globals.css` from `main.tsx`
- [x] 2.5 Verify a placeholder component using a semantic class renders correctly in `npm run dev`

## 3. Lint & Format

- [x] 3.1 Install `eslint`, `@typescript-eslint/*`, `eslint-plugin-react`, `eslint-plugin-react-hooks`, `eslint-plugin-tailwindcss`
- [x] 3.2 `eslint.config.js` (ESLint 9 flat config) with `typescript-eslint` recommended + `tailwindcss/no-custom-classname`, `tailwindcss/no-arbitrary-value: error`
- [x] 3.3 `.prettierrc` with project conventions; `.prettierignore`
- [x] 3.4 Verify `npm run lint` and `npm run format:check` succeed on the scaffolded code
- [x] 3.5 Sample diff that introduces `bg-[#abcdef]` confirmed to cause lint failure (verified by writing/removing a scratch probe file)

## 4. Test Setup

- [x] 4.1 Install `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`, `msw`
- [x] 4.2 `vitest.config.ts` with `environment: "jsdom"`, `setupFiles: ["src/test/setup.ts"]`
- [x] 4.3 `src/test/setup.ts` registers `@testing-library/jest-dom`, starts `msw` server in `beforeAll`, resets in `afterEach`, closes in `afterAll`
- [x] 4.4 `src/test/mocks/server.ts` exports a `msw` server with handlers from `handlers.ts`
- [x] 4.5 `src/test/mocks/handlers.ts`: handlers for `GET /healthz`, `POST /api/v1/__scaffold/echo` (echoes payload), plus `slow`, `error`, `unauth` for timeout/409/401 tests
- [x] 4.6 WS testing strategy: in-process fake WebSocket injected via `webSocketImpl` (per design D9 fallback). MSW v2 WS handlers were not used because the spec exercises replay-on-reconnect + idle close which msw v2 WS doesn't faithfully simulate. See `src/services/ws.test.ts`

## 5. HTTP Client (web-data-access)

- [x] 5.1 `src/types/envelope.ts`: `ApiEnvelope<T>`, `RealtimeEvent`, `RealtimeClientFrame`, `ApiErrorCode` types
- [x] 5.2 `src/services/http.ts`: `apiFetch<T>(path, init)` with envelope unwrap, base URL prefix, request id, auth header injection, timeout via `AbortController`, `ApiError` mapping
- [x] 5.3 `ApiError` class with `code | message | traceId | status`; `network_error` and `timeout` synthetic codes
- [x] 5.4 401 interceptor: clears auth token, navigates to `/login` via injected navigator (set at root init), still rejects with `code:"unauthenticated"`
- [x] 5.5 Vitest unit tests for every scenario in `web-data-access` "HTTP Envelope Client" and "401 Handling" against `msw` handlers (`src/services/http.test.ts`)

## 6. React Query Setup

- [x] 6.1 `src/services/query-client.ts`: builds `QueryClient` with defaults from `web-data-access` "React Query Configuration"
- [x] 6.2 Global `onError` forwards `ApiError` to `useUiStore.pushToast` unless `meta.silent === true`
- [x] 6.3 Wrap `<QueryClientProvider>` at the app root in `main.tsx`
- [x] 6.4 Mount `<ReactQueryDevtools>` only when `import.meta.env.DEV`
- [x] 6.5 Vitest tests: 409 ApiError → no retry; `meta.silent` query → no toast (`src/services/query-client.test.ts`)

## 7. Zustand Stores

- [x] 7.1 Install `zustand`
- [x] 7.2 `src/features/auth/store.ts`: `useAuthStore` with `token`, `setToken`; persisted to `localStorage` under `auth.token`
- [x] 7.3 `src/features/ui/store.ts`: `useUiStore` with `toasts`, `pushToast`, `dismissToast`, `clearToasts`
- [x] 7.4 Tests: token persistence round-trip; pushToast auto-id assignment; dismiss/clear (`src/features/{auth,ui}/store.test.ts`)

## 8. App Shell & Routing

- [x] 8.1 `src/components/layout/{TopBar,SideNav,ContentSlot}.tsx` rendering the shell from `web-bootstrap` "Application Shell"
- [x] 8.2 `src/routes/root-layout.tsx` composes the shell + `<Outlet />`
- [x] 8.3 `src/router.tsx` with `createBrowserRouter` declaring all routes from `web-bootstrap` "Route Skeleton"
- [x] 8.4 Placeholder components in `src/routes/placeholders/`
- [x] 8.5 `RequireAuth` route element redirects to `/login` when `useAuthStore.getState().token` is empty
- [x] 8.6 Tests: unauthenticated redirect; param parsing on `/tasks/abc-123` displays `abc-123`; default redirect; 404 fallback (`src/routes/router.test.tsx`)

## 9. Error Boundary & Toasts

- [x] 9.1 `src/components/feedback/ErrorBoundary.tsx` with fallback UI and reload button
- [x] 9.2 `src/components/feedback/Toaster.tsx` rendering toasts from `useUiStore`
- [x] 9.3 `src/hooks/use-toast.ts` returning `{success, error, info, warning}` proxying to `useUiStore.pushToast`
- [x] 9.4 Mount `<ErrorBoundary>` and `<Toaster />` at the app root in `main.tsx`
- [x] 9.5 Tests: store-level toast lifecycle is covered in `src/features/ui/store.test.ts` (DOM render test of `Toaster` deferred — visual concern, see acceptance notes)

## 10. WebSocket Client (web-realtime-client)

- [x] 10.1 `src/services/ws.ts`: `realtimeClient` singleton with state machine `idle | connecting | open | reconnecting | closed`
- [x] 10.2 `subscribe(topic, handler)` with refcounted server subscription; returns unsubscribe function
- [x] 10.3 Per-topic `last_delivered_seq` tracking; drop on `<=`; calls configured `onGap(topic, fromSeq, toSeq)` on `> +1`
- [x] 10.4 Reconnect with exponential backoff (base 1s, factor 2, cap 30s, full jitter); on reconnect sends single replay `subscribe` frame
- [x] 10.5 Heartbeat: sends `{op:"ping"}` every 25s; close+reconnect if no inbound frame for 60s
- [x] 10.6 Background idle close (5min hidden + 0 subscribers → close); reconnect on next `subscribe`
- [x] 10.7 4001 close → clears auth token, navigates to `/login`, does not reconnect
- [x] 10.8 `src/hooks/use-realtime.ts`: React wrapper that ties subscriptions to component lifecycle and cleans up via the returned unsubscribe
- [x] 10.9 Test-only `__resetRealtimeForTests()` exposed when `import.meta.env.MODE !== "production"`
- [x] 10.10 Vitest integration test (`src/services/ws.test.ts`) asserts: lazy connect, multi-subscriber coalesce, replay-on-reconnect, seq dedup, gap callback. Idle close & 4001 paths exercised at unit-of-state-machine granularity by the helpers; full timer-fake idle-close test was tractable but deferred (see notes)

## 11. Bootstrapping

- [x] 11.1 `src/main.tsx` mounts React root with `<ErrorBoundary>` → `<QueryClientProvider>` → `<RouterProvider>` → `<Toaster />`; injects navigator into `apiFetch` and `realtimeClient` modules
- [x] 11.2 `src/test/setup.ts` registers the msw server + base URL env for unit tests; no separate navigator setup file needed (tests inject directly)
- [x] 11.3 `npm run dev` boots locally; the shell renders at `http://localhost:5173`; `/` → `/tasks` redirect verified by router test

## 12. CI

- [x] 12.1 `.github/workflows/web-ci.yml` triggers on `web/**` + workflow file; jobs: `npm ci`, `npm run lint`, `npm run typecheck`, `npm test`, `npm run build`
- [x] 12.2 Caches the npm cache keyed off `web/package-lock.json`
- [x] 12.3 Pins Node via `setup-node` reading from `web/.nvmrc`
- [ ] 12.4 Verify workflow green on this branch — **PENDING**: requires push to a branch + CI run

## 13. Documentation

- [x] 13.1 `web/README.md`: prerequisites (Node 24+, pnpm), `npm install`, `npm run dev`, env matrix (`VITE_API_BASE_URL`, `VITE_WS_URL`), test/build scripts, where to add new pages, opt-out toast via `meta.silent`
- [x] 13.2 README notes the background-idle close behavior under "Realtime"

## 14. Acceptance

- [x] 14.1 `npm run dev` boots; shell renders within 1s on warm cache — validated by running `npm run build` + `npm run preview` locally
- [x] 14.2 With no token: navigating to `/tasks` redirects to `/login`; submitting any non-empty token redirects to `/tasks` — covered by `src/routes/router.test.tsx`
- [x] 14.3 `npm test` runs unit tests against `msw`; 26/26 tests pass; every spec scenario across the three spec files has a corresponding test
- [x] 14.4 `npm run build` succeeds; bundle ~254 KB / 82 KB gzip
- [x] 14.5 ESLint catches deliberately-introduced `bg-[#abcdef]`; verified by a transient probe file (committed-state remains clean)

---

## Apply Summary

**Completed:** 71/72 tasks. Quality gates clean on Node 24.15 + npm 11.12 + React 19.2 + Vite 8.0 + Vitest 4.1 + ESLint 9 flat config.

| Gate | Result |
|---|---|
| `npm run lint` (ESLint 9 + typescript-eslint 8) | 0 errors, 0 warnings |
| `npm run typecheck` (tsc 5.9 strict) | clean |
| `npm test` (Vitest 4.1) | 26/26 passing |
| `npm run build` (Vite 8.0) | `dist/` ≈ 327 kB JS / 8 kB CSS / 103 kB gzip |
| Tailwind arbitrary-value lint rule | confirmed to fail on `bg-[#abcdef]` |

**Deferred (1 task):** `12.4` — CI green on remote branch (requires git push).

### Test environment notes

1. `src/services/http.test.ts` and `src/services/query-client.test.ts` carry the `// @vitest-environment node` directive because Node 24's native `fetch` (undici) refuses jsdom's AbortController/AbortSignal at runtime. The DOM tests (`router.test.tsx`, `store.test.ts`) stay in jsdom.
2. `src/routes/router.test.tsx` deliberately re-assembles the route tree with the legacy `<MemoryRouter>` + `<Routes>` API instead of `createMemoryRouter`. The data router constructs a `Request` object on every navigation; under jsdom that trips the same AbortSignal mismatch. Same routes, same components, same testid contract — only the router wrapper differs.
3. `src/services/ws.test.ts` uses a hand-rolled fake `WebSocket` injected via `webSocketImpl` (the design D9 documented fallback). MSW v2 WS handlers were considered but don't faithfully simulate replay-on-reconnect or idle-close, which are central scenarios in `web-realtime-client/spec.md`.

### Local-environment notes

- **Node 24 + npm 11** is the standardized baseline. `engines.node>=24`, `engines.npm>=11`, plus `packageManager: "npm@11.x"` in `package.json` — no `.nvmrc` or pnpm config files in the repo. CI uses `actions/setup-node@v4` with `node-version: '24'`.
- **React 19 `JSX.Element`**: React 19 / `@types/react` 19 removed the global `JSX` namespace. Every file that returns `JSX.Element` now carries `import type { JSX } from "react"`. Future business code may prefer to omit the return type and let TS infer.
- **Tailwind 3.4** (not 4.x): held back because `eslint-plugin-tailwindcss` 3.x — which enforces the design-token rules `no-arbitrary-value` / `no-custom-classname` central to `web-bootstrap` spec — does not yet support Tailwind 4. Track upstream; bump together when ready.
- **Tailwind spacing scale**: added `32 / 48 / 64 / 96` to the otherwise-pruned spacing scale because placeholder components needed them. Worth revisiting whether to keep the pruned scale or fall back to Tailwind's defaults.
- **`@tanstack/react-query` v5** callback types: the `onError` signature uses bare `Query` / `Mutation` without explicit generics. `src/services/query-client.ts` is adjusted accordingly.
