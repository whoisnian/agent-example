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

The web client SHALL theme via shadcn-standard CSS variables rather than a hard-coded Tailwind color palette. `src/styles/globals.css` MUST define a `:root` (and a `.dark`) token set including at least `--background`, `--foreground`, `--card`, `--card-foreground`, `--popover`, `--popover-foreground`, `--primary`, `--primary-foreground`, `--secondary`, `--secondary-foreground`, `--muted`, `--muted-foreground`, `--accent`, `--accent-foreground`, `--border`, `--input`, `--ring`, `--destructive`, `--success`, `--warning`, and `--radius`. Under Tailwind v4 these color variables MUST be defined as **complete color values in the OKLCH color space** (e.g. `--background: oklch(...)`), NOT as bare HSL channel triplets consumed through an `hsl(var(--token))` wrapper. The theme MUST be expressed CSS-first via an `@theme` (or `@theme inline`) block that exposes the tokens as Tailwind color utilities (so `bg-background`, `text-foreground`, `bg-card`, etc. resolve from the variables); the project MUST NOT rely on a `tailwind.config.js` `theme.extend.colors` mapping for this. Class-based dark mode MUST remain in effect (`.dark` selector strategy). The default Tailwind `spacing`/`fontSize` scales MUST remain available to shadcn primitives (the migration MUST NOT replace or shrink them). The retired semantic class names (`bg`, `surface`, `accent`, `text`, `text-muted`, `danger`, …) MUST remain replaced by their token equivalents (`bg-background`, `bg-card`, `text-foreground`, `text-muted-foreground`, `bg-destructive`, …) across the codebase.

#### Scenario: Theme variables drive Tailwind colors

- **WHEN** a component uses `bg-background`, `text-foreground`, or `bg-card`
- **THEN** the rendered color MUST resolve from the corresponding CSS variable defined in `globals.css`, and changing the variable MUST change every consuming surface without editing component code

#### Scenario: Tokens are OKLCH complete-color values

- **WHEN** a contributor inspects the token definitions in `globals.css` after this change
- **THEN** each color token MUST be a complete `oklch(...)` color value, and NO **runtime style usage** (component `className`/`style` values, or the active `@theme`/`:root`/`.dark` blocks of `globals.css`) MUST consume a token through an `hsl(var(--token))` wrapper
- **AND** the acceptance gate for this MUST scope to runtime usage — a search for `hsl(var(` across `src/**/*.{ts,tsx,css}` excluding comments and `*.test.*` fixtures MUST return zero matches (the `cn.test.ts` `tailwind-merge` fixture is migrated to the v4 `var(--color-*)` form as part of this change; documentation comments are not gated)

#### Scenario: Theme is defined CSS-first

- **WHEN** the build runs after this change
- **THEN** the theme MUST be defined via a CSS `@theme` block in `globals.css` (Tailwind v4 CSS-first), and the color theme MUST NOT depend on a `tailwind.config.js` `theme.extend.colors` mapping

#### Scenario: Default spacing/font scales remain available

- **WHEN** the build runs after this change
- **THEN** the default Tailwind `spacing` and `fontSize` scales MUST remain available to shadcn primitives (the migration MUST NOT replace them with a reduced custom scale)

#### Scenario: No retired semantic class names remain

- **WHEN** the migration is complete
- **THEN** no file in the web project — including every source file under `src/`, `web/index.html` (its `<body class>`), and `src/styles/globals.css` — MUST reference the retired palette classes (`bg-bg`, `bg-surface`, `text-text`, `text-text-muted`, the old detached `bg-accent`/`bg-danger` palette); every color usage MUST go through the CSS-variable tokens (a `grep` for the retired classes MUST return zero matches)

#### Scenario: Default theme resolves without a class toggle

- **WHEN** the app renders without any theme-toggle UI (MVP)
- **THEN** the MVP default appearance MUST be carried by the `:root` token set so the app renders correctly with no `.dark` class applied to `<html>`; the `.dark` set MUST be defined for future use but MUST NOT be required for the default render

### Requirement: Relaxed Color Lint Posture

The frontend lint configuration SHALL forbid raw color literals in component code while permitting CSS-variable-backed token usage, and this guardrail MUST remain in force under Tailwind v4 (it MUST NOT be silently dropped during the migration). Bare color literals in JSX class names or inline styles (e.g. `bg-[#abcdef]`, a raw `oklch(...)`/`rgb(...)`/`hsl(...)` literal) MUST be rejected. Arbitrary Tailwind values MUST be permitted only when they reference a theme variable (e.g. a `var(--color-ring)` / `var(--ring)`-backed value). Because `eslint-plugin-tailwindcss@3` does not support Tailwind v4, the guardrail MAY be delivered either by (a) a Tailwind-v4-capable lint plugin, or (b) the plugin-independent `no-restricted-syntax` hex/color-literal rule combined with a grep-based CI check that asserts zero raw color literals and zero retired-palette class names; either way the effective protection MUST be equivalent. The posture MUST be documented in `web/AGENTS.md` so contributors know the color rule.

#### Scenario: Bare hex is still rejected

- **WHEN** a contributor introduces `class="bg-[#123456]"` or an inline hex color in component code
- **THEN** the lint/CI guardrail MUST flag it

#### Scenario: Raw OKLCH/RGB literal in component code is rejected

- **WHEN** a contributor introduces a bare `oklch(...)` or `rgb(...)` color literal in a JSX class name or inline style instead of a token
- **THEN** the lint/CI guardrail MUST flag it

#### Scenario: Variable-backed arbitrary value is allowed

- **WHEN** a primitive uses an arbitrary value that references a theme variable (e.g. a `var(--color-ring)` / `var(--ring)`-backed value)
- **THEN** the guardrail MUST NOT reject it

#### Scenario: Guardrail survives the v4 migration

- **WHEN** the migration to Tailwind v4 is complete
- **THEN** the raw-color-literal protection MUST still be enforced by CI (whether via a v4-capable plugin or the `no-restricted-syntax` + grep fallback), and `web/AGENTS.md` MUST document the color rule
