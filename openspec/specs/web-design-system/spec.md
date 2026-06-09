# web-design-system Specification

## Purpose
shadcn/ui 主题与组件基座：CSS 变量主题 token、`cn()` 合并工具、`components/ui/` primitives 目录约定、明暗主题契约，以及替代受限调色板后的色彩使用规则。

## Requirements
### Requirement: shadcn/ui Component Foundation

The web client SHALL adopt shadcn/ui as its component foundation. Vendored shadcn primitives MUST live under `src/components/ui/` (source-in-repo, not an npm dependency), and a `cn()` helper combining `clsx` + `tailwind-merge` MUST be provided at `src/lib/cn.ts` and used by primitives to merge class names. Primitives MUST be added on demand (only those actually consumed by a surface), never bulk-imported. The legacy hand-written `src/components/primitives/Button.tsx` MUST be retired in favor of the shadcn `Button`.

#### Scenario: Vendored primitives live under components/ui

- **WHEN** a contributor inspects the repository after this change
- **THEN** the shadcn primitives in use (at minimum `Button`) MUST exist as source files under `src/components/ui/`, and `src/lib/cn.ts` MUST export a `cn()` that merges Tailwind classes with `tailwind-merge`

#### Scenario: Legacy Button primitive is retired

- **WHEN** the migration is complete
- **THEN** `src/components/primitives/Button.tsx` MUST no longer be imported by any surface, and all buttons MUST render via the shadcn `Button`

### Requirement: CSS-Variable Theme Tokens

The web client SHALL theme via shadcn-standard CSS variables rather than a hard-coded Tailwind color palette. `src/styles/globals.css` MUST define a `:root` (and a `.dark`) token set including at least `--background`, `--foreground`, `--card`, `--card-foreground`, `--primary`, `--primary-foreground`, `--muted`, `--muted-foreground`, `--accent`, `--accent-foreground`, `--border`, `--input`, `--ring`, `--destructive`, and `--radius`. `tailwind.config.js` MUST set `darkMode: ["class"]`, map `theme.extend.colors` to those variables (e.g. `hsl(var(--background))`), and MUST NOT override Tailwind's default `spacing`/`fontSize` scales (they are restored so shadcn primitives render correctly). The retired semantic class names (`bg`, `surface`, `accent`, `text`, `text-muted`, `danger`, …) MUST be replaced by their token equivalents (`bg-background`, `bg-card`, `text-foreground`, `text-muted-foreground`, `bg-destructive`, …) across the codebase.

#### Scenario: Theme variables drive Tailwind colors

- **WHEN** a component uses `bg-background`, `text-foreground`, or `bg-card`
- **THEN** the rendered color MUST resolve from the corresponding CSS variable defined in `globals.css`, and changing the variable MUST change every consuming surface without editing component code

#### Scenario: Tailwind default scales are restored

- **WHEN** the build runs after this change
- **THEN** `tailwind.config.js` MUST NOT define a top-level `theme.colors`/`theme.spacing`/`theme.fontSize` that replaces Tailwind defaults; the default spacing and font-size scales MUST be available to shadcn primitives

#### Scenario: No retired semantic class names remain

- **WHEN** the migration is complete
- **THEN** no file in the web project — including every source file under `src/`, `web/index.html` (its `<body class>`), and `src/styles/globals.css` (`@apply` rules) — MUST reference the retired palette classes (`bg-bg`, `bg-surface`, `text-text`, `text-text-muted`, `bg-accent`, `bg-danger`, the old `border-border` token); every color usage MUST go through the new CSS-variable tokens (a `grep` for the retired classes MUST return zero matches)

#### Scenario: Default theme resolves without a class toggle

- **WHEN** the app renders without any theme-toggle UI (MVP)
- **THEN** the MVP default appearance MUST be carried by the `:root` token set so the app renders correctly with no `.dark` class applied to `<html>`; the `.dark` set MUST be defined for future use but MUST NOT be required for the default render

### Requirement: Relaxed Color Lint Posture

The frontend lint configuration SHALL be updated to permit CSS-variable-backed color usage while still forbidding raw hex literals in component code. Arbitrary Tailwind values MUST be permitted only when they reference theme variables (e.g. `hsl(var(--ring))`); bare hex (e.g. `bg-[#abcdef]`) MUST remain disallowed. The relaxed posture MUST be documented in `web/AGENTS.md` (or the project AGENTS.md §4.3 reference) so contributors know the new color rule.

#### Scenario: Bare hex is still rejected

- **WHEN** a contributor introduces `class="bg-[#123456]"` or an inline hex color in component code
- **THEN** lint MUST flag it

#### Scenario: Variable-backed arbitrary value is allowed

- **WHEN** a primitive uses an arbitrary value that references a theme variable (e.g. `ring-[hsl(var(--ring))]`)
- **THEN** lint MUST NOT reject it
