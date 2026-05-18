## ADDED Requirements

### Requirement: Build Toolchain

The web project SHALL be built with Vite 5+ targeting evergreen browsers (last 2 major versions of Chrome, Firefox, Safari, Edge). TypeScript SHALL be configured in `strict` mode with `noUncheckedIndexedAccess`, `noImplicitOverride`, and `exactOptionalPropertyTypes` enabled.

Production build (`pnpm build`) MUST produce a static asset bundle under `dist/` that can be served by any static file host. The HTML entry MUST include hashed asset URLs and a `Content-Security-Policy` meta tag that disallows inline scripts.

#### Scenario: TypeScript strict mode is enforced
- **WHEN** a contributor introduces code that violates `strict` mode (e.g. implicit `any` or unused parameters in strict mode)
- **THEN** `pnpm typecheck` MUST exit non-zero

#### Scenario: Production build emits hashed assets
- **WHEN** `pnpm build` completes
- **THEN** `dist/index.html` MUST reference at least one CSS and one JS asset with a content hash in the filename (e.g. `assets/index-<hash>.js`)

### Requirement: Application Shell

The application SHALL render a persistent shell at the React root containing: a top bar (logo on left, user-area placeholder on right), a left navigation rail with entries `Tasks`, `Cost`, `Settings`, and a main content slot where route components mount. The shell MUST render before route data has loaded (no blocking on first paint).

Shell components MUST be implemented in `src/components/layout/` and used by every authenticated route via a `<RootLayout>` wrapper.

#### Scenario: Shell renders on first paint
- **WHEN** the user navigates to any route under the authenticated tree
- **THEN** the shell (top bar + nav rail) MUST be visible before any route-specific content fetches complete

#### Scenario: Nav highlights active route
- **WHEN** the user is on `/tasks` or any subroute under `/tasks/...`
- **THEN** the `Tasks` nav item MUST render with the active style

### Requirement: Route Skeleton

The router SHALL declare the following routes (each rendering a placeholder component that displays the route name and a "not implemented" notice):

| Path | Component | Notes |
|---|---|---|
| `/` | redirect → `/tasks` | |
| `/tasks` | `TaskListPlaceholder` | |
| `/tasks/:id` | `TaskDetailPlaceholder` | reads `:id` from params |
| `/cost` | `CostDashboardPlaceholder` | |
| `/settings` | `SettingsPlaceholder` | |
| `/login` | `LoginPlaceholder` | rendered outside `RootLayout` |
| `*` | `NotFoundPlaceholder` | |

Unauthenticated access to any route except `/login` MUST redirect to `/login`. Authentication state is read from the auth store (token presence in `localStorage`); no token validation against the server is performed in this scaffold.

#### Scenario: Unauthenticated redirect
- **WHEN** the user navigates to `/tasks` without a token in `localStorage`
- **THEN** the router MUST redirect to `/login`

#### Scenario: Route parameter parsed
- **WHEN** the user navigates to `/tasks/abc-123`
- **THEN** `TaskDetailPlaceholder` MUST render and display `abc-123` as the task id

### Requirement: Global Error Boundary

A React error boundary SHALL wrap the entire application below the router. On a render-phase exception it MUST display a generic fallback UI (with a "Reload" button) and report the error to a centralized handler that logs to `console.error` with `{message, stack, route}` and, when configured, forwards to an error sink (placeholder hook only in scaffold).

#### Scenario: Render-phase exception is contained
- **WHEN** a child component throws during render
- **THEN** the error boundary fallback MUST replace the affected subtree without unmounting the shell, AND the error MUST be reported via the centralized handler

### Requirement: Global Notification (Toast) System

The application SHALL provide a global toast system accessible via a `useToast()` hook returning `{success, error, info, warning}` functions. Toasts MUST be stacked in the bottom-right corner, auto-dismiss after 5 seconds (configurable per toast), and be keyboard-dismissable.

The HTTP client (defined in `web-data-access`) MUST emit an error toast for any non-401 error not explicitly handled by the caller.

#### Scenario: Error toast appears for unhandled HTTP failure
- **WHEN** a fetch call resolves to a non-401 business error and the caller does not pass `{toastOnError: false}`
- **THEN** an error toast with the server-provided `message` MUST appear

### Requirement: Design Tokens and Styling

Tailwind CSS SHALL be the sole styling solution. A design token layer in `tailwind.config.js` SHALL define the project palette (semantic names: `bg`, `surface`, `border`, `text`, `text-muted`, `accent`, `success`, `warning`, `danger`), spacing scale, font sizes, and shadows. Arbitrary Tailwind colors / sizes (e.g., `bg-[#abcdef]`, `mt-[13px]`) SHALL be flagged by lint.

#### Scenario: Lint rejects arbitrary color
- **WHEN** a developer commits a class like `bg-[#abcdef]`
- **THEN** `pnpm lint` MUST exit non-zero with a rule violation

### Requirement: Dev Tooling Scripts

The project SHALL expose the following pnpm scripts:
- `dev`: launches Vite dev server with HMR on `:5173`
- `build`: production build into `dist/`
- `preview`: serves the production build locally
- `lint`: ESLint over `src/` with the configured rule set
- `lint:fix`: ESLint with `--fix`
- `format`: Prettier in write mode
- `typecheck`: `tsc --noEmit`
- `test`: Vitest single-run
- `test:watch`: Vitest watch mode
- `test:coverage`: Vitest with coverage report

#### Scenario: Test script runs vitest
- **WHEN** `pnpm test` is invoked
- **THEN** Vitest MUST execute all test files and exit non-zero on any failure

### Requirement: MSW-based Mock for Scaffold Tests

The project SHALL include `msw` as a test dependency and a request handler set under `src/test/mocks/` that simulates: `GET /healthz` (returns `{status:"ok"}`), `POST /api/v1/__scaffold/echo` (returns the request payload inside the standard envelope), AND a WS endpoint mock for the realtime tests.

#### Scenario: MSW intercepts a test request
- **WHEN** a unit test fires a request to `/api/v1/__scaffold/echo` with body `{ping:1}`
- **THEN** the test MUST receive `{code:0, message:"ok", data:{ping:1}, trace_id:<string>}` without any real network call
