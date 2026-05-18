## Context

`docs/ARCHITECTURE.md §3.1` fixes the frontend shape (React + TS + Vite + Tailwind + Zustand + React Query, plus a WebSocket-first realtime channel with REST fallback). What is left open: project layout for an unbuilt UI, the exact contracts between HTTP client and envelope, how the WS client handles topic coalescing / reconnect / gap-fill, and how the auth store interacts with both clients.

Current state: `web/` contains only `README.md`. The frontend does not depend on `init-api-scaffold` to merge first — it ships with `msw` mocks for every endpoint it touches.

Constraints inherited from architecture:
- Unified response envelope `{code, message, data, trace_id}` for all business endpoints.
- WS subscription protocol with `op:"subscribe"|"unsubscribe"|"ping"` and server frames `{topic, kind, seq, ts, payload}`.
- Client must recover from disconnects without losing event ordering — REST `events?after_id=` fills gaps.
- React Query for server state, Zustand for local UI state. Strict separation.

## Goals / Non-Goals

**Goals:**
- A `npm dev` skeleton that renders an app shell, routes, and verifies HTTP+WS contracts via `msw`.
- Lock in toolchain (Vite + TS strict + Tailwind + ESLint + Vitest) and patterns (Zustand-per-feature, React Query with sensible defaults) so feature proposals don't re-litigate.
- Provide a typed `apiFetch` and `realtimeClient` so the first business page can ship without infra work.
- Be backend-agnostic in CI: tests run with mocked endpoints; no need for a running API/Worker.

**Non-Goals:**
- Business pages (TaskCreate / TaskList / TaskDetail / VersionTree / CostDashboard).
- Real authentication flows. The scaffold accepts a token via a placeholder `/login` form that just stores it; no server validation.
- SSR / Next.js. Pure SPA.
- Internationalization, dark mode, accessibility audit — all noted as later proposals.
- Cross-tab session sync. One token per tab is fine for MVP.
- OpenAPI-generated client. We hand-write a thin `apiFetch` because there is no API contract to generate from yet.

## Decisions

### D1. Bundler: Vite 8

Fast dev server with native ESM, simple config, mature React 19 + TS templates. Alternatives considered:
- **Next.js**: SSR / file-based routing buys nothing for a logged-in dashboard app and adds RSC complexity.
- **Webpack**: slow dev rebuilds; we get nothing from its richer ecosystem at MVP scale.
- **Rsbuild / Turbopack**: Rsbuild is promising but the ecosystem (Vite plugins, Vitest) is heavier on Vite side.

Vite config (`vite.config.ts`) imports the React plugin (`@vitejs/plugin-react` v6) and Tailwind PostCSS — nothing exotic.

### D2. TypeScript strict mode + extra checks

`strict: true` plus `noUncheckedIndexedAccess` (catches a class of runtime undefined bugs), `noImplicitOverride`, `exactOptionalPropertyTypes`. Path alias `@/* → src/*` via `tsconfig.compilerOptions.paths` plus Vite alias config.

### D3. Routing: react-router-dom v7, data router mode

The data router (`createBrowserRouter`) supports route-level loaders and actions which we will exploit in future proposals for prefetch. `react-router-dom` v7 keeps the v6 hook + component API stable; the `react-router-dom` import path remains a thin re-export of the new `react-router` package. The scaffold doesn't ship loaders — placeholder components render directly — but the architecture is in place.

### D4. State separation: React Query vs Zustand

Hard rule encoded in `web-data-access`: server-fetched entities go in React Query; local UI ephemera (modal open/closed, toast queue, draft form state) go in Zustand. Auth token is the lone exception — kept in Zustand+`localStorage` because it's read synchronously by both `apiFetch` and `realtimeClient` at request time and is not really "server state."

`<ReactQueryDevtools>` mounts only in `import.meta.env.DEV` — never in prod bundles.

### D5. HTTP client: hand-written fetch wrapper

We considered axios; rejected because:
- adds 12 KB gzipped for features we don't use (interceptors, transforms, XHR shim),
- doesn't natively integrate with `AbortSignal` as cleanly,
- has its own error class that we'd wrap anyway.

The hand-written `apiFetch` is ~120 lines; it owns request id generation, auth header injection, envelope unwrap, timeout via `AbortController`, and `ApiError` mapping. Critical: timeout is implemented by aborting the underlying request (not racing a Promise) so we don't leak in-flight connections.

### D6. 401 handling at HTTP-client level

When any `apiFetch` sees a 401, we clear the auth token and navigate to `/login`. This is centralized in the client (not duplicated in every hook) and uses a navigator passed in at module init to avoid a hard dep on `react-router` inside `services/http.ts` (kept framework-agnostic for unit-testability).

### D7. WebSocket client design

A singleton `realtimeClient`. Why singleton: the architecture stipulates one connection per user (per browser tab) carrying all subscriptions; multiple connections would defeat server-side fanout.

Key sub-decisions:
- **Coalesced subscriptions**: multiple components subscribing to the same topic share one server-side subscription, refcounted client-side. Unsubscribe only sends `op:"unsubscribe"` when the last handler detaches.
- **Re-subscribe on reconnect**: after each reconnect the client replays the full current subscription set in a single frame.
- **Gap detection via `seq`**: track per-topic `last_delivered_seq`. Drops on `<=`, callbacks `onGap` on `>+1`. Gap-fill is delegated to an injected callback (not done by `realtimeClient`) so the client stays independent of REST routes — the consuming feature wires it up.
- **Reconnect backoff**: exponential with jitter, base 1s cap 30s. Matches conventional client behavior.
- **Heartbeat**: 25s ping, 60s timeout. Conservative; revisited if we see false positives over flaky networks.
- **Background idle close**: 5min hidden + no subs → close. Saves resources, reconnect on return.

Rejected alternatives:
- **Socket.IO**: protocol mismatch with the backend's plain WS contract from ARCHITECTURE §5.2.
- **EventSource (SSE)**: server protocol is WS; SSE is the documented fallback for later.

### D8. Styling: Tailwind only, design-token-enforced

A small `tailwind.config.js` defines semantic color tokens (`bg`, `surface`, `border`, `text`, `accent`, `success`, `warning`, `danger`). We avoid raw hex literals; ESLint rule blocks Tailwind's `arbitrary value` syntax (e.g. `bg-[#abc]`) so the token layer remains authoritative.

We deliberately do NOT ship a component library (shadcn/ui, MUI, Radix-based kit). The scaffold's components are the bare minimum: layout primitives, error fallback, toast. Higher-level primitives come with business proposals that need them.

### D9. Test stack: Vitest + Testing Library + msw

- **Vitest**: fast, ESM-native, Vite-aligned.
- **@testing-library/react**: standard.
- **msw**: intercepts both HTTP and WebSocket. MSW's WS support is sufficient for our scaffold tests (mock handshake, push frames, simulate disconnects).

Coverage threshold: not enforced at MVP. Will be set in a follow-up proposal once we have non-trivial code paths.

### D10. Directory layout

```
web/
├── index.html
├── vite.config.ts
├── tsconfig.json
├── tailwind.config.js
├── postcss.config.js
├── eslint.config.js                # flat config (ESLint 9)
├── .prettierrc
├── package.json                    # `engines.node>=24`, `packageManager:"npm@11.x"`
├── package-lock.json
├── README.md
└── src/
    ├── main.tsx                    # React root, QueryClientProvider, RouterProvider
    ├── App.tsx                     # routes config
    ├── routes/
    │   ├── root-layout.tsx         # the shell
    │   └── placeholders/           # TaskListPlaceholder, etc.
    ├── components/
    │   ├── layout/                 # top bar, side nav, content wrapper
    │   ├── feedback/               # ErrorBoundary, Toast renderer
    │   └── primitives/             # Button, Link, Card (scaffold-minimal)
    ├── features/
    │   ├── auth/
    │   │   └── store.ts            # token state
    │   ├── ui/
    │   │   └── store.ts            # toast queue, modals
    │   └── (other feature folders are created by their own proposals)
    ├── services/
    │   ├── http.ts                 # apiFetch + ApiError
    │   ├── ws.ts                   # realtimeClient
    │   └── query-client.ts         # QueryClient singleton
    ├── hooks/
    │   ├── use-toast.ts
    │   └── use-realtime.ts         # React hook wrapper for `subscribe`
    ├── types/
    │   └── envelope.ts             # ApiEnvelope<T>, RealtimeEvent
    ├── test/
    │   ├── mocks/
    │   │   ├── handlers.ts         # msw HTTP handlers
    │   │   ├── ws.ts               # msw WS handlers
    │   │   └── server.ts
    │   └── setup.ts                # vitest setup
    └── styles/
        └── globals.css             # Tailwind layers
```

### D11. CI scope

`.github/workflows/web-ci.yml`:
- `npm ci`
- `npm lint`
- `npm typecheck`
- `npm test`
- `npm build`
- Caches: npm store, node_modules.
- Trigger: `web/**` or workflow file changes; Node 24.

### D12. Auth model in scaffold

`/login` is a placeholder form with a single "Token" textarea. Submit → `useAuthStore.setToken(...)` → redirect to `/tasks`. No server interaction. This unblocks scaffold testing (you can paste any string to render the authenticated tree) and explicitly defers real auth to `add-web-auth` proposal.

The HTTP and WS clients consume the token via `useAuthStore.getState().token` at request time so token changes apply on the next request without store-subscription glue inside the clients.

## Risks / Trade-offs

- **[Risk] Hand-rolled `apiFetch` re-implements features (retry, dedup) that React Query already provides** → Mitigation: keep `apiFetch` pure transport. Retry, dedupe, refetch logic stays in React Query layer. `apiFetch` only handles transport-level concerns (envelope, timeout, 401, headers).
- **[Risk] Singleton WS client makes unit tests stateful** → Mitigation: expose `__resetRealtimeForTests()` in dev/test builds; gate on `import.meta.env.MODE !== "production"`.
- **[Risk] MSW WS support is younger than its HTTP support** → Mitigation: encapsulate WS mocks behind a small custom helper; if MSW WS proves flaky we swap to a small in-process WS server (e.g., `ws` package) without changing test signatures.
- **[Risk] Strict TS + `noUncheckedIndexedAccess` slows down array-heavy code** → Mitigation: the discipline is worth it for the realtime path where `events[seq]` reasoning is error-prone. Add helper utilities (e.g., `at(arr, i)` returning `T | undefined`) to keep call sites tidy.
- **[Risk] Tailwind arbitrary-value lint rule may fight legitimate one-off needs** → Mitigation: rule is warn-on-PR with `// eslint-disable-next-line` escape hatch; pattern requires writing a brief justification comment.
- **[Risk] Coalesced WS subscriptions miss edge cases when handlers attach during reconnect** → Mitigation: subscriber refcount is updated immediately, but the actual server `op:"subscribe"` frame is queued through the connection state machine; tests cover attach-during-reconnect.
- **[Risk] Background idle close (5min) confuses developers in dev tools** → Mitigation: log clearly at each transition; documented in `web/README.md` once written.

## Migration Plan

First code-bearing change for `web/`. No existing UI to migrate. Rollback is `git revert`.

The scaffold is publishable as a standalone static bundle (e.g. via `npm build && npm preview`) before any business endpoints exist, useful for design reviews. We do not deploy this scaffold to any environment.

## Open Questions

1. **CSP policy detail** — meta-tag CSP suffices for static hosts; do we add `Strict-Transport-Security` and other headers via a reverse proxy or a `_headers` file (Netlify-style)? Tentative: out of scope, handle in deployment proposal.
2. **Where does `VITE_API_BASE_URL` default in dev?** — Tentative: `http://localhost:8080` (matches `init-api-scaffold` default port). Documented in `web/README.md`.
3. **Pin React 18 or move to 19 at scaffold time?** — Tentative: React 18 for MVP (ecosystem maturity around 19 still settling for some libs we'll add later). Re-evaluate at v1.
4. **Storybook / Ladle / Cosmos?** — Not in scaffold; revisit when we have ≥3 design-system primitives worth documenting.
5. **e2e tests (Playwright)?** — Out of scope for MVP scaffold. The first business proposal that creates a real flow gets a Playwright proposal alongside it.
