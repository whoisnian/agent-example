## ADDED Requirements

<!-- This change is value-only. It ADDS a new requirement constraining token VALUES;
     it does NOT modify `CSS-Variable Theme Tokens` (whose structure/format/list A owns)
     nor `Relaxed Color Lint Posture`. This decoupling keeps B independent of A's
     MODIFIED block and makes archive order A-then-B idempotent on the structural spec. -->

### Requirement: Neutral-Minimal Visual Identity

The web client's theme token **values** SHALL express a neutral-minimal (Vercel/Geist-style) visual identity rather than the prior Linear-style dark-indigo palette, changing only **values** (and `--radius` / typography rhythm) on top of the token structure, OKLCH format, and required-token list established by `CSS-Variable Theme Tokens`. The `--primary` token MUST be a near-monochrome high-contrast neutral (near-black in the light value set, near-white in the dark value set) rather than a saturated brand hue; saturated accent color MUST be confined to links, focus rings, and semantic states. `globals.css` MUST define **both** a complete light value set and a complete dark value set (each covering the full required token list as OKLCH complete-color values), adopting the **standard shadcn convention**: the **light** set on `:root` (the active default — the app renders light with no `.dark` class on `<html>`) and the **dark** set under the `.dark` selector. Surface layering (`card`/`popover`/`muted`/`accent`/`secondary`) MUST read through subtle neutral lightness steps and low-contrast borders. The `--ring` token MUST remain a visible focus indicator with adequate contrast even though `--primary` is now monochrome (it MUST NOT silently inherit the monochrome primary if that would fail focus-visibility). This change MUST NOT alter token structure/format/mechanism, the `darkMode:["class"]` strategy, vendored primitives, class names, any `data-testid`, or the three-column shell / conversation-turn structure.

#### Scenario: Primary is near-monochrome, not a brand hue

- **WHEN** a contributor inspects the `--primary` token in both the light and dark value sets
- **THEN** it MUST be a near-monochrome high-contrast neutral (near-black for light, near-white for dark), NOT a saturated indigo/brand hue

#### Scenario: Both light and dark palettes are authored complete

- **WHEN** a contributor inspects `globals.css` after this change
- **THEN** both a complete light value set (on `:root`) and a complete dark value set (on `.dark`) MUST exist (full required OKLCH token list each), so the theme-switching change can later switch between two complete, self-consistent palettes by mounting/removing `.dark`

#### Scenario: Focus ring stays visible under monochrome primary

- **WHEN** a keyboard user focuses an interactive element in either palette
- **THEN** the `--ring` focus indicator MUST be clearly visible (adequate contrast against the focused surface), even though `--primary` is monochrome

#### Scenario: Visual-only — structure and contracts unchanged

- **WHEN** the redesign is complete
- **THEN** no `data-testid`, three-column shell structure, conversation-turn model, token structure/format, or required-token list MUST have changed, and the contract tests MUST pass unchanged; only token values, `--radius`, and typography rhythm MUST differ from the base migration

#### Scenario: Default appearance is light with no toggle

- **WHEN** the app renders after this change (toggle UI not yet introduced)
- **THEN** the MVP default appearance MUST be light, carried by the `:root` set with no `.dark` class on `<html>` and no `index.html` structural change, and MUST render correctly with no theme-toggle UI present; introducing the toggle + persistence + FOUC-safe boot is out of scope for this change (the `:root`=light / `.dark`=dark convention is already standard here, so no convention flip remains for the toggle change)

#### Scenario: Both palettes meet text-contrast accessibility

- **WHEN** text and semantic-state colors are rendered under either the light or the dark value set
- **THEN** body text and status colors MUST meet WCAG AA contrast against their backgrounds in both palettes
