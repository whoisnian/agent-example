## ADDED Requirements

### Requirement: Theme Preference State and Persistence

The web client SHALL maintain a user theme preference of `light`, `dark`, or `system`, held as local UI state (Zustand) and persisted to `localStorage`. Setting the preference MUST update `localStorage` and apply the resolved theme to `<html>` (adding the `.dark` class for dark, removing it for light). When the preference is `system`, the resolved theme MUST follow `prefers-color-scheme` and MUST update live when the OS setting changes. Both `localStorage` access AND `window.matchMedia` access MUST be guarded: if either is unavailable or throws (privacy mode, or a non-browser/jsdom test environment where `matchMedia` is undefined), the client MUST fall back gracefully (resolve to a safe default without throwing) so that **existing contract tests remain green** and the app does not crash on import.

#### Scenario: Preference persists across reloads

- **WHEN** a user selects the `dark` (or `light`) theme and reloads the app
- **THEN** the previously selected theme MUST be applied, read from `localStorage`

#### Scenario: System preference follows the OS live

- **WHEN** the preference is `system` and the OS color scheme changes from light to dark
- **THEN** the applied theme MUST update to dark without a reload (the `.dark` class on `<html>` MUST update)

#### Scenario: localStorage unavailable degrades gracefully

- **WHEN** `localStorage` access throws (e.g. privacy mode)
- **THEN** the client MUST NOT crash, MUST resolve to a safe default for the session, and switching MUST still work within the session

#### Scenario: Missing matchMedia (jsdom) is guarded

- **WHEN** the theme store initializes in an environment where `window.matchMedia` is `undefined` (the vitest + jsdom test environment)
- **THEN** the store MUST NOT throw on import or init, the `system` subscription MUST no-op safely, and the existing contract test suite MUST remain green; the test setup (`src/test/setup`) MUST provide a `matchMedia` stub so DropdownMenu-bearing component tests still render

### Requirement: FOUC-Safe Theme Boot Under Strict CSP

The web client SHALL apply the resolved theme to `<html>` **before first paint**, with no perceptible flash of the wrong theme. Because `index.html` enforces a strict `script-src 'self'` CSP, the boot mechanism MUST remain CSP-compliant with the **minimum** necessary allowance: if an inline `<head>` script is used, its exact bytes MUST be allow-listed via a `'sha256-...'` hash in `script-src`, and `unsafe-inline` MUST NOT be introduced. The boot script and the runtime store MUST read the same `localStorage` key and use the same resolution rules so the boot decision and the post-hydration decision agree (no second flash). The `object-src 'none'` and other CSP red-line directives MUST remain unchanged.

#### Scenario: No flash on load

- **WHEN** the app loads with a persisted `dark` preference (or `system` resolving to dark)
- **THEN** `<html>` MUST carry `.dark` before first paint — there MUST be no visible light-then-dark flash

#### Scenario: CSP allows the boot script without unsafe-inline

- **WHEN** the inline boot script runs under the page CSP
- **THEN** it MUST be permitted via a `'sha256-...'` hash in `script-src`, `unsafe-inline` MUST NOT be present, and `object-src 'none'` MUST remain

#### Scenario: Boot hash matches the built output and stays in sync

- **WHEN** the production build emits `dist/index.html` (Vite may transform/whitespace the inline script)
- **THEN** the `'sha256-...'` in the CSP `script-src` MUST match the hash of the inline script bytes **as emitted in `dist/index.html`** (not merely the source), and a check run as part of `npm run build` acceptance MUST fail if the emitted-script hash and the declared CSP hash diverge — preventing a boot script that works in dev but is silently blocked in production

#### Scenario: Boot and store agree

- **WHEN** the runtime theme store initializes after the boot script has already set the class
- **THEN** the store MUST resolve the same theme (same `localStorage` key and rules) and MUST NOT re-flip the class, avoiding a second flash

### Requirement: Theme Toggle in SideNav

The web client SHALL expose a theme switch within the SideNav user-area DropdownMenu (alongside the existing Tasks / Cost / Settings / Logout items), letting the user choose `light`, `dark`, or `system`. The control MUST carry stable `data-testid`s (e.g. `theme-option-{light,dark,system}`) and MUST reflect the stored **preference** (`light`/`dark`/`system`) as the selected state — NOT the resolved theme — so that `system` reads as selected even when it resolves to dark (the control MAY additionally indicate the currently-resolved theme). The interaction MUST account for Radix `DropdownMenuItem` `onSelect` closing the menu by default: either present three explicit `DropdownMenuItem`s that call `e.preventDefault()` to keep the menu open on choose, or use a `DropdownMenuRadioGroup` — and if a primitive not currently vendored is needed, it MUST be vendored on-demand into `components/ui/` and noted in Impact. The control MUST NOT alter the locked SideNav structure (brand → New task → RecentTasks → user-area menu) beyond adding the theme control, nor introduce a separate column or floating button.

#### Scenario: User switches theme from the menu

- **WHEN** a user opens the SideNav user-area menu and selects a different theme option
- **THEN** the applied theme MUST change immediately and the choice MUST persist

#### Scenario: Control reflects the stored preference, not the resolved theme

- **WHEN** the preference is `system` and it currently resolves to dark
- **THEN** the `system` option MUST read as the selected one (the selected-state `data-testid` is on the preference option), not the `dark` option

#### Scenario: Toggle control has stable testids

- **WHEN** the contract tests query the theme control
- **THEN** it MUST be reachable via stable `data-testid`s, and the rest of the SideNav structure and its existing testids MUST be unchanged
