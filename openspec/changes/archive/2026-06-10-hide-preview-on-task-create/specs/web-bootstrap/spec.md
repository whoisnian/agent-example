## MODIFIED Requirements

### Requirement: Application Shell

The application SHALL render a persistent **three-column** shell at the React root, used by every authenticated route via a `<RootLayout>` wrapper. **Exception**: on the Task Create route (`/tasks/new`) the right Artifact Preview column (including its collapsed-state re-open affordance) MUST NOT render at all — the chat composer is the full-focus surface and there is no version to preview; the suppression is route-driven and MUST NOT mutate the user's preview-collapse preference in the UI store. The three columns are: a **fixed-width left navigation column** (not collapsible; no collapse toggle is rendered), a **center main content column** where route components mount (the Task Detail surface being the primary inhabitant), and a **collapsible right Artifact Preview column** (see `web-artifact-preview`). The shell MUST render before route data has loaded (no blocking on first paint).

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

#### Scenario: Task Create suppresses the preview column

- **WHEN** an authenticated user is on `/tasks/new`
- **THEN** the right preview column and its re-open affordance MUST NOT render, and navigating to another route MUST restore the column per the unchanged store collapse flag

#### Scenario: Narrow viewport degrades gracefully

- **WHEN** the viewport is below the wide breakpoint
- **THEN** the right Artifact Preview column MUST become a button-triggered drawer/overlay (not a permanently visible third column) and the center content MUST remain fully usable
