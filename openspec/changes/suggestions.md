# Review: Design-System Modernization Trilogy (A / B / C)

> **Resolution status (2026-06-14):** All 6 blocking + 14 improvements were vetted against the live code and **applied** to the proposals, with one deliberate divergence noted below. The three changes re-validate clean (`openspec validate`). See the Resolution log at the bottom for per-finding disposition.
>
> **Divergence from the review on X1:** the reviewer offered "B mounts a static `class="dark"` on `<html>`" as one option. I instead kept **B strictly value-only** — `:root` stays dark (default unchanged, no `index.html` touch), the light palette is authored-inactive in a `.light` block (reusing the repo's existing "defined for future use" convention), and the entire `:root`=light/`.dark`=dark **convention flip is concentrated in C** (the theme-behavior change). This honors B's non-goals better and means only C ever MODIFIES the default-theme scenario — no three-way contention on the shared spec block.

Consolidated improvement suggestions for the three dependency-ordered changes:

- **A** = `migrate-design-system-base` (Tailwind v3→v4 base migration, visually equivalent)
- **B** = `redesign-neutral-minimal` (repaint to Vercel/Geist neutral-minimal; light+dark palettes)
- **C** = `add-dual-theme-toggle` (theme store + persistence + FOUC-safe boot under strict CSP + SideNav toggle)

Reviewed against root `AGENTS.md`, `web/AGENTS.md`, the archived `web-design-system` spec, and the actual `web/` config.

---

## Summary table

| Change | Blocking | Improvements | Overall health |
|---|---|---|---|
| A (`migrate-design-system-base`) | 2 | 4 | Solid plan; one real spec-gate bug + one header omission. Technically accurate on v4. |
| B (`redesign-neutral-minimal`) | 2 | 3 | **Delta header is wrong** (won't match archived spec) + scope creep into A's spec. Otherwise sound. |
| C (`add-dual-theme-toggle`) | 1 | 4 | CSP/FOUC reasoning is correct. One real default-theme coherence gap with the test suite. |
| Cross-cutting | 1 | 3 | Default-theme transition A→B→C and the shared archived "at least" token list. |

Technical-accuracy verdict: the Tailwind v4 claims (`@import "tailwindcss"`, `@theme`/`@theme inline`, `@tailwindcss/vite` vs `@tailwindcss/postcss`, autoprefixer removal, browser baseline Safari 16.4+/Chrome 111+/Firefox 128+), the OKLCH "variable = full color, drop `hsl(var())` wrapper" claim, the `tailwindcss-animate`→`tw-animate-css` claim, and the CSP `script-src 'self'` + `sha256` FOUC reasoning are all **correct and current**. No factual corrections needed there. Findings below are about spec-delta mechanics, scope boundaries, and concrete gaps verified against the codebase.

---

## A — migrate-design-system-base

### [BLOCKING] A1 — D4 zero-`hsl(var(` grep gate has guaranteed false-positive hits it never accounts for
Evidence: design.md:51 and tasks.md:4.3 set the gate `grep -rE 'hsl\(var\(' web/src` → zero matches; the OKLCH scenario in `specs/.../spec.md:15` makes it normative: *"a search for `hsl(var(` across `src/` and `globals.css` MUST return zero matches."* But the live tree has matches the change never names:
- `src/lib/cn.test.ts:25-26` — `const cls = "ring-[hsl(var(--ring))]";` is a **deliberate test fixture** asserting `tailwind-merge` preserves variable-backed arbitraries. A literal `grep` gate fails here.
- `src/styles/globals.css:8` and `:96` — `hsl(var(...))` appears inside **comments** describing the old mechanism.

Why it matters: the acceptance gate as written can never pass without touching files A explicitly says it won't reason about, and the contract test at cn.test.ts:24-27 is a real regression-net assertion — blindly rewriting it to `var(--color-ring)` changes what `tailwind-merge` is being tested against. This is the single most likely thing to make A's "all green" claim false on first run.

Suggested fix: in D4/tasks 4.3, scope the gate to **runtime style usage**, not raw text. Either (a) restrict to non-comment, non-test code (`grep -rE 'hsl\(var\(' src --include='*.tsx' --include='*.ts' --include='*.css' | grep -v '\.test\.' | grep -v '^\s*\*'`), or (b) explicitly list cn.test.ts and the globals.css comments as known/expected, and add a task: "update `cn.test.ts` fixture to the v4 `var(--color-ring)` form and re-assert merge behavior." Spell out the test-file decision so it isn't silently broken.

### [BLOCKING] A2 — A drops the `shadcn/ui Component Foundation` requirement from the spec without a RENAMED/REMOVED marker
Evidence: archived spec has three requirements (`shadcn/ui Component Foundation`, `CSS-Variable Theme Tokens`, `Relaxed Color Lint Posture`; spec.md:7/21/45). A's delta (`migrate-design-system-base/specs/.../spec.md`) carries only `## MODIFIED Requirements` with `CSS-Variable Theme Tokens` and `Relaxed Color Lint Posture`. The proposal (proposal.md:24) explicitly says the component-foundation requirements "**保持不变**" (stay unchanged).

Why it matters: an OpenSpec delta with only MODIFIED blocks signals which requirements change. A requirement that is intentionally untouched is fine to omit — but A's *own* tasks (6.x) touch `components/ui/*` smoke validation and D7 discusses possible visual correction inside primitives. A reader can't tell whether "Foundation" is deliberately unchanged or accidentally dropped. This is borderline; flagging as blocking only because the archive step will re-materialize the full spec from deltas and a missing/extra requirement is exactly the kind of drift that corrupts the archived spec.

Suggested fix: add a one-line note in A's spec delta header comment, e.g. `<!-- shadcn/ui Component Foundation requirement intentionally unchanged by this migration -->`, so archive reconciliation is unambiguous. (No content change needed — just make the omission explicit.)

### [IMPROVEMENT] A3 — `tailwind-merge` v4-compat not verified, though it's the linchpin of the vendored primitives
Evidence: `package.json:41` pins `tailwind-merge@^3.6.0`; D7 (design.md:66-67) asserts `components/ui/*` "应能编译" under v4 but only smoke-tests rendering. `tailwind-merge` has class-group tables that must match the Tailwind major; a v3-era table can mis-merge v4 utilities (e.g. new `size-*`, changed `ring` defaults) silently — no compile error, just wrong last-wins behavior at runtime, which jsdom tests won't catch.

Why it matters: A's correctness rests on "primitives render the same," and the merge layer is exactly where a v3/v4 mismatch hides. Add an explicit check.

Suggested fix: add a task under §2 or §6 — "confirm `tailwind-merge` major aligns with Tailwind v4 (bump if the v4 class-group table requires it); re-run cn.test.ts merge assertions against v4 utilities." Note it in Impact's dependency list alongside the other dep bumps.

### [IMPROVEMENT] A4 — Lint fallback path (b) bans raw `hsl(...)` literals but A itself is built on OKLCH; ensure the rule targets *class/style* literals only
Evidence: design.md:61 path (b) extends `no-restricted-syntax` to "禁裸 `oklch(...)`/`rgb(...)` 字面量出现在 JSX className"; spec.md:46-49 scenario rejects "a bare `oklch(...)` or `rgb(...)` color literal in a JSX class name or inline style." But `globals.css` legitimately contains many raw `oklch(...)` values (the token definitions themselves), and the existing eslint rule only scans `.ts/.tsx`.

Why it matters: the wording "in component code / JSX className / inline style" is right, but the new grep-based CI check (D6 path b) could over-match if pointed at CSS. A reader implementing path (b) needs the boundary stated: token *definitions* in `globals.css` are the one allowed home for raw color literals.

Suggested fix: in D6 path (b) and tasks 5.1, state the grep check excludes the `@theme`/`:root`/`.dark` token-definition blocks in `globals.css` (or scope it to `src/**/*.{ts,tsx}` + className strings only), so token definitions aren't false-flagged.

### [IMPROVEMENT] A5 — `eslint.config.js` `tailwindcss` plugin removal/replacement under path (b) is under-specified for the flat-config `settings`
Evidence: `eslint.config.js:48-60` registers `tailwindcss` plugin + `settings.tailwindcss.callees/config`, and rules `no-custom-classname`/`no-contradicting-classname` (lines 76-77). D6 path (b) "退役 `eslint-plugin-tailwindcss` 的 classname 校验" but tasks 5.1 only mentions adding the grep check.

Why it matters: if the plugin is removed, the `plugins`, `settings.tailwindcss`, and two rule lines must all be deleted or ESLint flat config throws on an unknown plugin/rule reference. The cn.test.ts comment at line 16-18 also references `tailwindcss/no-contradicting-classname` — that test still passes (it's a comment), but the lint that *protected* that assertion goes away under path (b).

Suggested fix: add an explicit sub-task: "under path (b), remove the `tailwindcss` plugin registration, `settings.tailwindcss`, and its two rules from `eslint.config.js`; update the cn.test.ts comment if the referenced rule no longer exists." Note that `tailwindcss/no-contradicting-classname` coverage is lost and the grep check does not replace it.

### [IMPROVEMENT] A6 — `components.json` v4 update mentioned but the concrete shape isn't pinned
Evidence: tasks.md:2.4 "更新 `components.json` 的 Tailwind v4 相关字段"; actual file has `tailwind.config: "tailwind.config.js"` and `tailwind.css: "src/styles/globals.css"` (components.json:7-8). Under v4 CSS-first with `tailwind.config.js` deleted (D1), the `config` field should become `""`.

Why it matters: leaving `config` pointing at a deleted file makes future `shadcn add` invocations fail; minor but concrete.

Suggested fix: specify in tasks 2.4 that `tailwind.config` becomes `""` (empty) if the config file is deleted, matching the shadcn-v4 `components.json` shape.

---

## B — redesign-neutral-minimal

### [BLOCKING] B1 — MODIFIED requirement header does not match any archived requirement (delta will not reconcile)
Evidence: B's delta (`redesign-neutral-minimal/specs/.../spec.md:1,3`) is `## MODIFIED Requirements` → `### Requirement: Neutral-Minimal Visual Identity`. The archived spec (`openspec/specs/web-design-system/spec.md`) has no such requirement; its headers are `shadcn/ui Component Foundation`, `CSS-Variable Theme Tokens`, `Relaxed Color Lint Posture`. A `MODIFIED` block must reproduce an **existing** requirement header verbatim, then restate its full updated content.

Why it matters: this is a hard delta-mechanics bug. On archive, a MODIFIED block whose header matches nothing either creates an orphan or silently fails to update the intended requirement (`CSS-Variable Theme Tokens`, which is what B actually repaints). The values B specifies (light+dark palettes, near-monochrome primary) are exactly the content of `CSS-Variable Theme Tokens` and the OKLCH wording A added to it.

Suggested fix: rename B's block header to `### Requirement: CSS-Variable Theme Tokens` and fold the neutral-minimal value constraints into the *full* restated requirement text (carrying forward A's OKLCH/`@theme` wording, since B stacks on A). If the intent is genuinely a *new* requirement, move it under `## ADDED Requirements` with a name that doesn't collide — but given it only changes token **values**, MODIFIED-on-`CSS-Variable Theme Tokens` is the correct shape.

### [BLOCKING] B2 — B redefines the full token list and adds requirements (`--secondary`, `--success`, `--warning`) that the archived "at least" list omits; this belongs in A, not B
Evidence: B's requirement (spec.md:5) enumerates the *complete* required token list including `--secondary`, `--success`, `--warning`. The archived `CSS-Variable Theme Tokens` "at least" list (spec.md:23) does **not** include those three, yet the live `globals.css:22-33,49-60` already defines them and `tailwind.config.js:32-39` maps `success`/`warning`. So the spec's required-token list is already stale vs. reality, and B is silently widening it as a side effect of a values repaint.

Why it matters: widening the **required token contract** (adding success/warning/secondary as MUST-haves) is a structural/contract change, which B's own scope explicitly disclaims ("token 的结构/格式/机制…保持 A 落地后的形态不变", proposal.md:21). If the required-list widening lands in B it contradicts B's non-goals; it also means A's MODIFIED block (which also restates the "at least" list at spec.md:5 without success/warning/secondary) and B's list disagree about the contract.

Suggested fix: move the required-token-list correction into **A** (A already rewrites `CSS-Variable Theme Tokens` and should make the "at least" list match what already ships: add `--secondary(-foreground)`, `--success(-foreground)`, `--warning(-foreground)`, `--popover(-foreground)`). Then B changes only values, not the list. This also resolves the A/B list disagreement.

### [IMPROVEMENT] B3 — Light palette is brand-new surface area never visually exercised before; "visual-only, contracts unchanged" undersells the risk
Evidence: B introduces a *complete light token set* in `:root` (spec.md:13-16, D1) while the app has only ever shipped dark. design.md Risks covers WCAG AA, but no task validates that light mode actually *renders* end-to-end (it can't be reached in B — there's no toggle until C). tasks 3.1 walks surfaces but only in the default (dark) appearance.

Why it matters: B authors a palette that nothing in B or its tests can render. Bugs in the light set (e.g. `--foreground` near-white on near-white `--background` from a copy/paste) surface only in C, far from where they're introduced.

Suggested fix: add a B task to **temporarily** force `.dark` off (or a throwaway story/dev flag) and screenshot-walk the key surfaces in light, purely for review — explicitly noting it's a dev-only check, no toggle shipped. Or state that C's acceptance owns first real light-mode rendering and add a forward-reference risk.

### [IMPROVEMENT] B4 — `--ring` / focus-visibility coupling with near-monochrome primary needs an explicit token decision
Evidence: live `--ring` equals `--primary` (`globals.css:36` `--ring: 239 84% 67%` = `--primary`). B makes primary near-monochrome (D2). If `--ring` keeps tracking primary, the focus ring becomes near-black-on-dark / near-white-on-light — i.e. low-contrast focus on some surfaces. D2/D3 mention focus rings as where accent *may* live, but no token-level decision is recorded.

Why it matters: accessibility-relevant; focus visibility can regress precisely because primary went monochrome. It's the kind of thing that passes "visual review" on a desktop and fails keyboard-nav audit.

Suggested fix: add to tasks 1.2/3.3 an explicit `--ring` decision (keep an accent hue for ring even if primary is monochrome) and a focus-visibility check across light+dark.

### [IMPROVEMENT] B5 — Font/`--font-*` + CSP `font-src` is left as an open question that can leak into C's CSP work
Evidence: D4 (design.md:36-38) defers Geist/Inter; tasks 2.3 conditionally configures `--font-*` and "评估 CSP `font-src`". Current CSP is `font-src 'self'` (index.html:12). C is the change that touches CSP (for the boot-script hash).

Why it matters: if B decides to self-host a font, that's a `font-src 'self'` (already fine) — but if it ever reaches for an external font CDN, it collides with the locked CSP and with C's CSP edits. Two changes editing CSP is exactly the kind of cross-change coupling to avoid.

Suggested fix: state in B that any font is **self-hosted** (no CSP change; `font-src 'self'` already covers it), keeping all CSP edits inside C. If a CDN font is ever wanted, that's a separate change.

---

## C — add-dual-theme-toggle

### [BLOCKING] C1 — Default flips to `system`, but the existing contract tests/SideNav render assume a fixed default; the "boot vs store agree" invariant doesn't cover the test environment
Evidence: D4 (design.md:41-44) changes default from hard-coded dark to `system`. The boot script reads `localStorage` + `matchMedia` (D1). But (a) jsdom has no real `matchMedia` (`web/AGENTS.md:32` — tests are vitest+jsdom+MSW), and (b) `web/AGENTS.md` requires existing `data-testid` stability and green contract tests. tasks 5.x adds new theme tests but nothing addresses that **every existing test** now boots into a `system`-resolved theme where `matchMedia` is undefined in jsdom.

Why it matters: `window.matchMedia` is `undefined` in jsdom unless polyfilled. The store's `system` subscription (tasks 1.3) and graceful-fallback (1.4) must both no-op safely in tests, or the whole suite throws on import — turning "all green" false for the entire web test suite, not just new tests. This is the concrete, will-break gap.

Suggested fix: add a task to (1) provide a jsdom `matchMedia` stub in the test setup (`src/test/setup`), and (2) assert the store's `localStorage`/`matchMedia` access is fully guarded (the D2/1.4 try-catch must also guard `matchMedia` being absent, not just `localStorage` throwing). State explicitly that existing contract tests must remain green with the new default.

### [IMPROVEMENT] C2 — CSP hash mechanism is sound but the "build/test-time hash check" owner is unspecified, and a Vite `transformIndexHtml` interaction is unaddressed
Evidence: D1 + scenario (spec.md:36-39) require a build/test check that the inline-script sha256 matches the CSP entry. tasks 2.3 says "加构建/测试期校验" but doesn't say where it runs. Vite may transform/whitespace `index.html`; the hashed bytes must be the **post-build** bytes the browser sees.

Why it matters: a hash computed over the source `index.html` inline script can differ from what Vite emits to `dist/index.html`, silently blocking the boot script in production while dev (which doesn't enforce built output) looks fine. This is the classic CSP-hash footgun.

Suggested fix: specify the check computes the hash over the **built** `dist/index.html` inline script bytes (or use a Vite plugin that injects the hash post-transform), and run it as part of `npm run build` acceptance, not just a unit test over source. Add it to tasks 5.4/6.1.

### [IMPROVEMENT] C3 — Default-behavior change (`system`) is a user-facing semantic change that arguably warrants touching `web-design-system`, not only the new capability
Evidence: C deliberately keeps all deltas in the new `web-theme-switching` capability (proposal.md:19-22) "to avoid colliding with A/B." But the archived `CSS-Variable Theme Tokens` requirement contains the scenario *"Default theme resolves without a class toggle"* (archived spec.md:40-43; carried into A spec.md:32-35 and B spec.md:22-25) which asserts the MVP default is carried by `:root`/no-`.dark`. C **invalidates** that scenario (default is now preference-driven, possibly `.dark`).

Why it matters: after C, a still-active requirement in `web-design-system` ("Default theme resolves without a class toggle") becomes false, but no delta retires/modifies it. The new-vs-modified split is *mostly* clean (good call isolating the mechanism), but this one scenario is a genuine collision C claims to avoid.

Suggested fix: have C add a small MODIFIED delta against `web-design-system`'s `CSS-Variable Theme Tokens` that revises (or removes) the *"Default theme resolves without a class toggle"* scenario to reflect preference-driven boot — or move that scenario's obligation into `web-theme-switching`. Otherwise the archived spec self-contradicts after C.

### [IMPROVEMENT] C4 — Toggle UI lives in the DropdownMenu, but a tri-state select inside a Radix menu has known focus/interaction quirks not flagged
Evidence: D3 (design.md:37-39) puts light/dark/system into the user-area `DropdownMenu`. SideNav uses Radix `DropdownMenuItem` with `onSelect` that closes the menu (SideNav.tsx:124-139). A tri-state cycle item or a submenu both have UX implications: `onSelect` closes the menu by default, so a "cycle" item closes on each click; a submenu needs `DropdownMenuSub*` primitives not currently vendored.

Why it matters: implementation detail that affects whether new primitives must be vendored (touches `components/ui/`, which `web/AGENTS.md` says add on-demand) — relevant to scope/size.

Suggested fix: pick the interaction in design (recommend three explicit `DropdownMenuItem`s `theme-option-{light,dark,system}` with `onSelect` preventing close via `e.preventDefault()`, or `DropdownMenuRadioGroup` which must be vendored). Note any new vendored primitive in Impact.

### [IMPROVEMENT] C5 — "Control reflects active selection" scenario needs to distinguish *preference* vs *resolved* theme
Evidence: spec.md:50-53 — "the control MUST reflect the active selection." With `system`, the *preference* is `system` but the *resolved* theme is light or dark. The scenario is ambiguous about which the control shows.

Why it matters: a checkmark on `system` vs on the resolved `dark` are different UIs; tests will encode one. Ambiguity here causes test/impl mismatch.

Suggested fix: clarify the scenario: the control reflects the stored **preference** (`light`/`dark`/`system`), and `system` may additionally indicate the currently-resolved theme. Pin which `data-testid` carries the selected state.

---

## Cross-cutting

### [BLOCKING] X1 — The default-theme story across A→B→C is internally contradictory and never reconciled in one place
Evidence: A keeps `:root` = dark default and restates the *"Default theme resolves without a class toggle"* scenario (A spec.md:32-35). B keeps default dark but now `:root` holds the **light** palette and `.dark` holds dark (B spec.md:13-16, D1) — meaning after B, the "default carried by `:root` with no `.dark`" scenario would render **light**, not dark, unless B also pre-mounts `.dark`. B's scenario "Default appearance remains dark with no toggle" (B spec.md:22-25) asserts dark, but B's own palette layout (`:root`=light) makes "no `.dark` = light." Then C flips default to `system` (C D4), invalidating both.

Why it matters: this is the highest-value finding. There is a real, latent contradiction: **B moves light into `:root`** while simultaneously promising "default stays dark with no `.dark` class." Those can't both hold — if `:root` is light and `<html>` has no `.dark`, the app renders light. B must either (a) keep dark in `:root` and light in a `.light` class (non-standard for shadcn), or (b) pre-mount `.dark` on `<html>` for the MVP default (a structural change B disclaims), or (c) accept that "default dark with no toggle" is only true until C and document the seam. None of the three changes states which.

Suggested fix: decide the convention once and thread it through all three:
- Recommended: `:root` = light, `.dark` = dark (standard shadcn). Then **B's "default stays dark" can only be honored by mounting `.dark`** — so either B ships a static `class="dark"` on `<html>` in `index.html` (explicit, structural, call it out), or B explicitly defers the default to C and its "default remains dark" scenario is reworded to "the dark palette is complete and is what the MVP will default to once boot logic lands (C)."
- Add a single "default-theme transition" note (in B's design and C's design) stating: A=dark-in-`:root` (no `.dark`); B=light-in-`:root` + dark-in-`.dark`, default still dark via `<X>`; C=preference-driven (`system` fallback). Make the `<X>` mechanism explicit.

### [IMPROVEMENT] X2 — The archived "at least" token list is stale vs. shipping code; fix it once (in A) so B/C inherit a correct contract
Evidence: archived `CSS-Variable Theme Tokens` "at least" list (spec.md:23) lacks `--secondary`, `--success`, `--warning`, `--popover` even though `globals.css` and `tailwind.config.js` ship them. A and B both restate the list inconsistently (A omits them at spec.md:5; B includes them at spec.md:5).

Why it matters: the required-token contract should have exactly one correct source; today it's wrong in the archive and the two changes disagree. See A's MODIFIED block as the right place to correct it (ties to B2).

Suggested fix: in A's MODIFIED `CSS-Variable Theme Tokens`, expand the "at least" list to include `--popover(-foreground)`, `--secondary(-foreground)`, `--success(-foreground)`, `--warning(-foreground)`. B then only changes values and references the corrected list.

### [IMPROVEMENT] X3 — PR-size (<500 LOC) check: A is the risk; B and C are fine
Evidence: root `AGENTS.md §7` caps PRs at ~500 lines (generated code/tests excluded). B is value edits in one CSS file (small). C is a store + boot script + SideNav edit + tests (moderate; tests excluded). A touches deps, vite/postcss/tailwind config, globals.css rewrite, eslint config rewrite, components.json, possibly every `components/ui/*` for visual correction, **plus** the AGENTS.md/spec docs — and includes a spike.

Why it matters: A realistically risks exceeding the soft cap, and the D6 spike makes its diff conditional (path a vs b produce different eslint diffs). A is also the only change with a genuinely uncertain blast radius (D7 "correct primitives if they drift").

Suggested fix: not necessarily a split, but state in A's proposal that the spike (§1) lands as a separate prep commit, and that primitive visual-correction (D7/6.2), if it touches many files, may be carved into a follow-up. Keep config+globals+docs as the core PR.

### [IMPROVEMENT] X4 — Dependency order is stated but not machine-guarded; archive order matters for spec reconciliation
Evidence: each change states "depends on A" / "depends on A+B" in prose (A proposal.md:3, B proposal.md:29, C design.md:3). All three `.openspec.yaml` share `created: 2026-06-13` with no ordering field. Because A and B both MODIFY the same `web-design-system` requirement, applying/archiving them out of order corrupts the delta base (B's MODIFIED text assumes A's OKLCH wording is already in the archive).

Why it matters: if B is archived before A, B's MODIFIED block (which should restate A's post-migration content) overwrites the requirement with text that omits A's v4/OKLCH constraints — silent regression of the spec.

Suggested fix: add an explicit ordering note at the top of B's and C's proposals ("MUST be applied and archived strictly after A" / "after A and B"), and when fixing B1, ensure B's restated `CSS-Variable Theme Tokens` text carries forward A's OKLCH/`@theme` clauses so archiving B-after-A is idempotent on the structural parts.

---

## Resolution log

All findings verified against live code before applying. Disposition:

| ID | Severity | Disposition | Where applied |
|---|---|---|---|
| A1 | BLOCKING | Applied — grep gate scoped to runtime (exclude `*.test.*`/comments); `cn.test.ts` fixture migrated to `var(--color-ring)` | A design D4, tasks 4.3/4.4, spec OKLCH scenario |
| A2 | BLOCKING | Applied — explicit comment marking Component Foundation intentionally unchanged | A spec header, proposal |
| A3 | IMPROVEMENT | Applied — `tailwind-merge`↔v4 alignment verify task | A tasks 2.5 |
| A4 | IMPROVEMENT | Applied — lint boundary: rule targets `src` className/style only, excludes `globals.css` token defs | A design D6, tasks 5.1/5.3 |
| A5 | IMPROVEMENT | Applied — flat-config removal subtask (plugin/settings/2 rules) + cn.test.ts comment | A design D6, tasks 5.2 |
| A6 | IMPROVEMENT | Applied — `components.json` `tailwind.config: ""` | A tasks 2.4 |
| B1 | BLOCKING | Applied — `## MODIFIED`→`## ADDED`, requirement kept as new `Neutral-Minimal Visual Identity` (decouples from A) | B spec |
| B2 | BLOCKING | Applied — token-list correction moved to A; B is value-only, references A's list | A spec (list), B spec/proposal |
| B3 | IMPROVEMENT | Applied — dev-only light-render walk task + forward-ref to C | B tasks 3.2 |
| B4 | IMPROVEMENT | Applied — explicit `--ring` keeps accent hue under monochrome primary | B design D2, tasks 1.2/3.4, spec scenario |
| B5 | IMPROVEMENT | Applied — fonts self-hosted only; all CSP edits stay in C | B proposal, design D4 |
| C1 | BLOCKING | Applied — guard `matchMedia` + `localStorage`; `test/setup` stub; assert existing suite stays green | C spec, design risks, tasks 1.4/5.1/5.5 |
| C2 | IMPROVEMENT | Applied — hash computed over built `dist/index.html`, checked in `npm run build` | C spec scenario, design D1, tasks 2.2/2.3 |
| C3 | IMPROVEMENT | Applied — C adds MODIFIED on `web-design-system` revising the default scenario | C `specs/web-design-system/spec.md`, proposal |
| C4 | IMPROVEMENT | Applied — interaction pinned (3 items + `preventDefault`, or vendored RadioGroup noted) | C design D3, tasks 4.2, spec |
| C5 | IMPROVEMENT | Applied — control reflects **preference** not resolved; testid pinned | C spec scenario, design D3, tasks 4.3 |
| X1 | BLOCKING | Applied (divergent) — convention flip concentrated in C; B value-only with `.light` authored-inactive | B design D1, C design D5, both specs |
| X2 | IMPROVEMENT | Applied — "at least" token list corrected once, in A | A spec, proposal |
| X3 | IMPROVEMENT | Applied — PR-size note: spike as prep commit, primitive correction may carve out | A proposal Impact |
| X4 | IMPROVEMENT | Applied — explicit "apply/archive strictly after" ordering notes | B proposal, C proposal |
