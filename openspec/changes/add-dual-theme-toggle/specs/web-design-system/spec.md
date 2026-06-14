<!-- C MUST be applied/archived strictly after A (and B). This MODIFIED block restates
     `CSS-Variable Theme Tokens` as it stands after A's migration, and changes ONLY:
       1. the `:root`/`.dark` convention → standard shadcn (`:root`=light, `.dark`=dark);
       2. the "Default theme resolves without a class toggle" scenario → preference-driven boot.
     This is the single place the default-theme behavior flips; B does not touch this block. -->

## MODIFIED Requirements

### Requirement: CSS-Variable Theme Tokens

The web client SHALL theme via shadcn-standard CSS variables rather than a hard-coded Tailwind color palette. `src/styles/globals.css` MUST define a `:root` (and a `.dark`) token set including at least `--background`, `--foreground`, `--card`, `--card-foreground`, `--popover`, `--popover-foreground`, `--primary`, `--primary-foreground`, `--secondary`, `--secondary-foreground`, `--muted`, `--muted-foreground`, `--accent`, `--accent-foreground`, `--border`, `--input`, `--ring`, `--destructive`, `--success`, `--warning`, and `--radius`. Under Tailwind v4 these color variables MUST be defined as **complete color values in the OKLCH color space** (e.g. `--background: oklch(...)`), NOT as bare HSL channel triplets consumed through an `hsl(var(--token))` wrapper. The theme MUST be expressed CSS-first via an `@theme` (or `@theme inline`) block that exposes the tokens as Tailwind color utilities (so `bg-background`, `text-foreground`, `bg-card`, etc. resolve from the variables); the project MUST NOT rely on a `tailwind.config.js` `theme.extend.colors` mapping for this. With dual-theme switching now wired, the token sets MUST adopt the **standard shadcn convention**: `:root` carries the **light** value set and the `.dark` selector carries the **dark** value set (the prior MVP arrangement of carrying the default in `:root`-as-dark is replaced; the `.light` block authored-inactive by the neutral-minimal redesign MUST be promoted into `:root`). Class-based dark mode MUST remain in effect (`.dark` selector strategy). The default Tailwind `spacing`/`fontSize` scales MUST remain available to shadcn primitives (the migration MUST NOT replace or shrink them). The retired semantic class names (`bg`, `surface`, `accent`, `text`, `text-muted`, `danger`, …) MUST remain replaced by their token equivalents (`bg-background`, `bg-card`, `text-foreground`, `text-muted-foreground`, `bg-destructive`, …) across the codebase.

#### Scenario: Theme variables drive Tailwind colors

- **WHEN** a component uses `bg-background`, `text-foreground`, or `bg-card`
- **THEN** the rendered color MUST resolve from the corresponding CSS variable defined in `globals.css`, and changing the variable MUST change every consuming surface without editing component code

#### Scenario: Tokens are OKLCH complete-color values

- **WHEN** a contributor inspects the token definitions in `globals.css` after this change
- **THEN** each color token MUST be a complete `oklch(...)` color value, and NO **runtime style usage** (component `className`/`style` values, or the active `@theme`/`:root`/`.dark` blocks of `globals.css`) MUST consume a token through an `hsl(var(--token))` wrapper

#### Scenario: Theme is defined CSS-first

- **WHEN** the build runs after this change
- **THEN** the theme MUST be defined via a CSS `@theme` block in `globals.css` (Tailwind v4 CSS-first), and the color theme MUST NOT depend on a `tailwind.config.js` `theme.extend.colors` mapping

#### Scenario: Default spacing/font scales remain available

- **WHEN** the build runs after this change
- **THEN** the default Tailwind `spacing` and `fontSize` scales MUST remain available to shadcn primitives (the migration MUST NOT replace them with a reduced custom scale)

#### Scenario: No retired semantic class names remain

- **WHEN** the migration is complete
- **THEN** no file in the web project — including every source file under `src/`, `web/index.html` (its `<body class>`), and `src/styles/globals.css` — MUST reference the retired palette classes (`bg-bg`, `bg-surface`, `text-text`, `text-text-muted`, the old detached `bg-accent`/`bg-danger` palette); every color usage MUST go through the CSS-variable tokens (a `grep` for the retired classes MUST return zero matches)

#### Scenario: Default theme is preference-driven via FOUC-safe boot

- **WHEN** the app renders with theme switching wired (this change)
- **THEN** the applied theme MUST be determined by the persisted user preference (or `system` when none is stored), applied to `<html>` before first paint by the FOUC-safe boot mechanism (see the `web-theme-switching` capability); the `.dark` class on `<html>` MUST be present/absent according to the resolved theme, and the app MUST NOT depend on a hard-coded default that ignores the stored preference. The `:root` (light) set MUST render correctly when `.dark` is absent, and the `.dark` set when it is present.
