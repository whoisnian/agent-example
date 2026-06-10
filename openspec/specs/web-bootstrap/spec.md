# web-bootstrap Specification

## Purpose
TBD - created by archiving change init-web-scaffold. Update Purpose after archive.
## Requirements
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

The application SHALL render a persistent **three-column** shell at the React root, used by every authenticated route via a `<RootLayout>` wrapper. The three columns are: a **fixed-width left navigation column** (not collapsible; no collapse toggle is rendered), a **center main content column** where route components mount (the Task Detail surface being the primary inhabitant), and a **collapsible right Artifact Preview column** (see `web-artifact-preview`). The shell MUST render before route data has loaded (no blocking on first paint).

**Column proportions** (wide viewports, all three columns side-by-side): the right Artifact Preview column SHALL be the visually dominant column. Its width is measured against the **width remaining beside the navigation column**: at the wide breakpoint it MUST occupy 40–55% of that remaining width, and at extra-wide viewports it MUST occupy approximately half, bounded by a maximum width so ultra-wide screens do not degenerate. The center column SHALL constrain route content to a reading-width container, centered within the remaining space; the left navigation column SHALL be a narrow fixed-width column. The previous fixed narrow preview column (320px sidebar) is superseded.

**Left navigation content** (top to bottom): the brand/logo row; a primary **"New task" action button** navigating to `/tasks/new`; a **Recents section** listing the most recent tasks (reusing the existing task-list data access, no new transport), where each row shows the task title and navigates to that task's detail page, and the row for the currently open task renders highlighted; and at the bottom the authenticated **user area rendered avatar-style** (a circular initial-of-email avatar and the user email). The user area SHALL act as the trigger of a **popup menu** containing the primary navigation entries `Tasks`, `Cost`, `Settings` and the **logout control**; these entries MUST NOT render as a flat always-visible nav list. The menu entry for the currently active route MUST render with an active/selected style when the menu is open. The Recents list MUST render quiet loading and error states (no toast) and MUST NOT re-sort tasks client-side (server order — `created_at` descending per `task-read-api` — is the recency order; "recently created", not "recently active").

Only the right-preview collapse state remains in the global UI store (Zustand); the previous left-nav collapse state is retired and MUST NOT be reintroduced. The shell MUST be responsive: at wide viewports all three columns render side-by-side; at narrower viewports the right preview column MUST collapse into a button-triggered drawer/overlay so the center content remains usable; the left navigation keeps its fixed width.

The `<RootLayout>` wrapper MAY remain at its current location (`src/routes/root-layout.tsx`); its three-column child components (navigation column, center content region, right preview region) MUST live under `src/components/layout/` and MUST be built on the shadcn/ui foundation and CSS-variable theme tokens (see `web-design-system`).

#### Scenario: Shell renders on first paint

- **WHEN** the user navigates to any route under the authenticated tree
- **THEN** the shell (left nav + center content region + right preview region) MUST be visible before any route-specific content fetches complete

#### Scenario: Preview column dominates at wide viewports

- **WHEN** the viewport is at or above the wide breakpoint and the preview column is expanded
- **THEN** the right Artifact Preview column MUST occupy 40–55% of the width remaining beside the navigation column (approximately half at extra-wide viewports, subject to its maximum-width bound), and the center column content MUST be constrained to a centered reading-width container

#### Scenario: No nav collapse affordance

- **WHEN** the left navigation renders
- **THEN** it MUST NOT render a collapse/expand toggle, and the navigation column MUST keep its fixed width across routes

#### Scenario: User-area menu hosts the primary navigation

- **WHEN** the user activates the bottom user area
- **THEN** a popup menu MUST open containing `Tasks`, `Cost`, `Settings` entries and a logout control, and activating an entry MUST navigate to its route and close the menu

#### Scenario: Menu marks the active route

- **WHEN** the user is on `/cost` and opens the user-area menu
- **THEN** the `Cost` menu entry MUST render with the active/selected style

#### Scenario: New task action navigates to creation

- **WHEN** the user activates the "New task" action in the left navigation
- **THEN** the router MUST navigate to `/tasks/new`

#### Scenario: Recents lists recent tasks and navigates

- **WHEN** the task-list read returns tasks for the first page
- **THEN** the Recents section MUST list them in server order with their titles, and activating a row MUST navigate to that task's `/tasks/:id` detail page

#### Scenario: Recents highlights the open task

- **WHEN** the user is on `/tasks/:id` and that task appears in the Recents list
- **THEN** that Recents row MUST render with the active/highlighted style

#### Scenario: Recents reflects task lifecycle changes

- **WHEN** an iterate, rollback, or control mutation settles, or a realtime task status frame arrives
- **THEN** the task-list query caches MUST be invalidated so the Recents section (and the Task List page) re-render the task's new status on refetch

#### Scenario: Recents stays quiet on failure

- **WHEN** the Recents task-list read fails
- **THEN** the section MUST render an inline quiet error placeholder without emitting any toast

#### Scenario: Avatar-style user area with logout in the menu

- **WHEN** an authenticated user views the left navigation
- **THEN** the bottom user area MUST render a circular avatar derived from the user's email initial and the user email, and the logout control MUST be reachable inside the user-area popup menu and MUST work

#### Scenario: Preview column collapses via the UI store

- **WHEN** the user toggles the right-preview collapse control
- **THEN** the preview collapse flag in the global UI store MUST flip and the column MUST collapse/expand accordingly, with the state surviving route changes within the session

#### Scenario: Narrow viewport degrades gracefully

- **WHEN** the viewport is below the wide breakpoint
- **THEN** the right Artifact Preview column MUST become a button-triggered drawer/overlay (not a permanently visible third column) and the center content MUST remain fully usable

### Requirement: Route Skeleton

The router SHALL declare the following routes (placeholder rows display the route name and a "not implemented" notice; real rows render their implemented component):

| Path | Component | Notes |
|---|---|---|
| `/` | redirect → `/tasks/new` | chat-style creation is the landing surface |
| `/tasks` | `TaskList` | |
| `/tasks/new` | `TaskCreate` | |
| `/tasks/:id` | `TaskDetail` | reads `:id` from params |
| `/cost` | `CostDashboard` | |
| `/settings` | `SettingsPlaceholder` | |
| `/login` | `LoginPage` | rendered outside `RootLayout`; real email/password login (see `web-auth`) |
| `*` | `NotFoundPlaceholder` | |

Unauthenticated access to any route except `/login` MUST redirect to `/login`. Authentication state is read from the auth store (token presence in `localStorage`). Token validity is established by `LoginPage` against the API and re-checked lazily: an invalid or expired token surfaces as a `401` on the first authenticated request, which clears the session and redirects to `/login` (see `web-data-access` 401 Handling).

#### Scenario: Authenticated root lands on the composer
- **WHEN** an authenticated user navigates to `/`
- **THEN** the router MUST redirect (replace) to `/tasks/new` and render the Task Create composer

#### Scenario: Unauthenticated redirect
- **WHEN** the user navigates to `/tasks` without a token in `localStorage`
- **THEN** the router MUST redirect to `/login`

#### Scenario: Route parameter parsed
- **WHEN** the user navigates to `/tasks/abc-123`
- **THEN** `TaskDetail` MUST render and read `abc-123` as the task id from the route params

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

Tailwind CSS SHALL be the sole styling solution. The design token layer SHALL be expressed as **shadcn-standard CSS variables** defined in `src/styles/globals.css` (`:root` and `.dark`) and mapped in `tailwind.config.js` via `theme.extend.colors` to `hsl(var(--token))` (see `web-design-system`), replacing the previous hard-coded semantic palette (`bg`, `surface`, `border`, `text`, `text-muted`, `accent`, `success`, `warning`, `danger`). `tailwind.config.js` MUST set `darkMode: ["class"]` and MUST NOT override Tailwind's default `spacing`/`fontSize` scales. Arbitrary Tailwind color literals — bare hex such as `bg-[#abcdef]` and raw pixel sizes such as `mt-[13px]` — SHALL still be flagged by lint; arbitrary values that reference a theme variable (e.g. `ring-[hsl(var(--ring))]`) SHALL be permitted.

#### Scenario: Lint rejects arbitrary hex color

- **WHEN** a developer commits a class like `bg-[#abcdef]`
- **THEN** `npm run lint` MUST exit non-zero with a rule violation

#### Scenario: Variable-backed arbitrary value is allowed

- **WHEN** a primitive uses an arbitrary value that references a theme variable (e.g. `ring-[hsl(var(--ring))]`)
- **THEN** `npm run lint` MUST NOT reject it

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

