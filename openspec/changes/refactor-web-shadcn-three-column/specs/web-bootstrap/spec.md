## MODIFIED Requirements

### Requirement: Application Shell

The application SHALL render a persistent **three-column** shell at the React root, used by every authenticated route via a `<RootLayout>` wrapper. The three columns are: a **collapsible left navigation column** (logo, primary nav entries `Tasks`, `Cost`, `Settings`, the authenticated user area with a logout control, and a recent-tasks affordance), a **center main content column** where route components mount (the Task Detail surface being the primary inhabitant), and a **collapsible right Artifact Preview column** (see `web-artifact-preview`). The shell MUST render before route data has loaded (no blocking on first paint).

The left-nav and right-preview collapse states MUST be held in the global UI store (Zustand), not in route/query state. The shell MUST be responsive: at wide viewports all three columns render side-by-side; at narrower viewports the right preview column MUST collapse into a button-triggered drawer/overlay and the left nav MUST be collapsible to an icon rail or top drawer, so the center content remains usable on small screens.

The `<RootLayout>` wrapper MAY remain at its current location (`src/routes/root-layout.tsx`); its three-column child components (navigation column, center content region, right preview region) MUST live under `src/components/layout/` and MUST be built on the shadcn/ui foundation and CSS-variable theme tokens (see `web-design-system`).

#### Scenario: Shell renders on first paint

- **WHEN** the user navigates to any route under the authenticated tree
- **THEN** the shell (left nav + center content region + right preview region) MUST be visible before any route-specific content fetches complete

#### Scenario: Nav highlights active route

- **WHEN** the user is on `/tasks` or any subroute under `/tasks/...`
- **THEN** the `Tasks` nav item MUST render with the active style

#### Scenario: Columns collapse via the UI store

- **WHEN** the user toggles the left-nav or right-preview collapse control
- **THEN** the corresponding collapse flag in the global UI store MUST flip and the column MUST collapse/expand accordingly, with the state surviving route changes within the session

#### Scenario: Narrow viewport degrades gracefully

- **WHEN** the viewport is below the wide breakpoint
- **THEN** the right Artifact Preview column MUST become a button-triggered drawer/overlay (not a permanently visible third column) and the center content MUST remain fully usable

### Requirement: Design Tokens and Styling

Tailwind CSS SHALL be the sole styling solution. The design token layer SHALL be expressed as **shadcn-standard CSS variables** defined in `src/styles/globals.css` (`:root` and `.dark`) and mapped in `tailwind.config.js` via `theme.extend.colors` to `hsl(var(--token))` (see `web-design-system`), replacing the previous hard-coded semantic palette (`bg`, `surface`, `border`, `text`, `text-muted`, `accent`, `success`, `warning`, `danger`). `tailwind.config.js` MUST set `darkMode: ["class"]` and MUST NOT override Tailwind's default `spacing`/`fontSize` scales. Arbitrary Tailwind color literals — bare hex such as `bg-[#abcdef]` and raw pixel sizes such as `mt-[13px]` — SHALL still be flagged by lint; arbitrary values that reference a theme variable (e.g. `ring-[hsl(var(--ring))]`) SHALL be permitted.

#### Scenario: Lint rejects arbitrary hex color

- **WHEN** a developer commits a class like `bg-[#abcdef]`
- **THEN** `npm run lint` MUST exit non-zero with a rule violation

#### Scenario: Variable-backed arbitrary value is allowed

- **WHEN** a primitive uses an arbitrary value that references a theme variable (e.g. `ring-[hsl(var(--ring))]`)
- **THEN** `npm run lint` MUST NOT reject it
