# web-artifact-preview Delta — add-artifact-download-proxy

## MODIFIED Requirements

### Requirement: Artifact Preview Panel

The web client SHALL render an Artifact Preview panel as the right column of the three-column shell. The panel MUST be data-anchored to the global UI store's selection pair — `selectedVersionId` plus a **selected-artifact id** — where `selectedVersionId` defaults to the task's `current_version` and both are updated when the user activates an artifact card in a Task Detail conversation turn (see `web-tasks-pages`). Artifact-level selection state MUST live in the global UI store (not panel-internal state) so the conversation turns and the panel share one selection: selecting an artifact row inside the panel writes the same store field. When the store carries a selected artifact, the panel MUST preview that exact artifact; when it carries only a version, the panel lists that version's artifacts with no artifact preview active. The panel MUST consume the existing `features/artifacts/` data access (`useVersionArtifactsQuery`, `useArtifactPresignMutation`) and MUST NOT introduce a new artifacts transport. When no version is selected (e.g. a task with no current version), the panel MUST render a neutral empty/placeholder state, not an error.

The panel MUST render a **header toolbar** as its first row, owned by the panel (not the shell column container): the left side shows the selected artifact's identity (its `kind` and a mime label) or the generic panel title when no artifact is selected; the right side offers a **Copy** action, a **Refresh** action, and the **close/collapse** control (which flips the right-preview collapse flag in the global UI store, preserving the existing close behavior and its test selector). The toolbar MUST render in **every** panel state — including the no-version placeholder, loading, error, and empty states — so the close control is always reachable (the panel is mounted shell-wide, not only on the detail page); in those states the title falls back and Copy / Refresh are disabled. Copy and Refresh semantics are defined in "Lightweight Artifact Content Preview"; both MUST be disabled (with a reason) when no artifact is selected.

**Selection consistency invariant**: changing `selectedVersionId` alone (e.g. the detail page re-anchoring to a new `current_version` after an iterate/rollback completes) MUST clear the selected-artifact id; only the paired conversation-turn activation writes both atomically. If the store's selected artifact is not present in the currently listed version's artifacts, the panel MUST treat it as no-artifact-selected (list-only rendering), never an error.

The panel MUST list the selected version's artifacts in the server-provided order (no client re-sort), each row showing `kind`, a mime label (`—` when null), and a size label (human-readable bytes, `—` when null). **The row's selection hit area MUST span the full row height and width except the Download control** — activating any point of the row (other than Download) selects that artifact. Download MUST mint a fresh signed download URL on each click (never cached) and navigate the browser to the returned **same-origin API download URL** (the presign action's `url` is an opaque relative path; the browser never contacts OSS). The panel MUST render distinct loading, empty (`artifacts: []`), and error states, preserving the single-error-surface convention: the list read stays silent at both toast layers (`meta.silent` + `toastOnError:false`) and the panel's own `onError` is the sole error surface (no double toast). Existing artifact `data-testid` selectors (`artifact-row`, `artifact-select`, `artifact-download`, `artifact-list-empty`, `artifact-list-loading`, `artifact-list-error`) MUST be preserved so existing contract tests continue to apply.

#### Scenario: Panel reflects the conversation selection

- **WHEN** the user activates an artifact card in a conversation turn
- **THEN** the store's `selectedVersionId` and selected-artifact id update, and the panel MUST list that version's artifacts via `useVersionArtifactsQuery` (server order) with the activated artifact's preview active

#### Scenario: Panel-internal selection shares the store

- **WHEN** the user selects an artifact row inside the panel
- **THEN** the same global store selected-artifact field MUST update (no panel-private selection state), keeping conversation turns and panel consistent

#### Scenario: Full-row selection hit area

- **WHEN** the user clicks an artifact row at its vertical edge (top or bottom padding), outside the Download control
- **THEN** the artifact MUST become selected exactly as if its text content had been clicked

#### Scenario: Toolbar shows the selected artifact identity

- **WHEN** the user selects an artifact in the panel
- **THEN** the header toolbar MUST show that artifact's `kind` and mime label, and the Copy / Refresh actions MUST reflect that artifact; with no artifact selected the toolbar MUST fall back to the generic panel title with Copy / Refresh disabled

#### Scenario: Version re-anchor clears a dangling artifact selection

- **GIVEN** the user activated an artifact from an older turn and the task then completes a new version (live/refetch moves `current_version`)
- **WHEN** the detail page re-anchors `selectedVersionId` to the new current version
- **THEN** the selected-artifact id MUST be cleared and the panel MUST render the new version's list with no artifact preview active (no stale cross-version preview, no error)

#### Scenario: Toolbar renders in contentless states

- **WHEN** the panel is in the no-version, loading, error, or empty state
- **THEN** the header toolbar MUST still render with the generic title, disabled Copy / Refresh, and a working close control

#### Scenario: Toolbar close collapses the panel

- **WHEN** the user activates the toolbar close control
- **THEN** the right-preview collapse flag in the global UI store MUST flip and the column MUST collapse, identically to the previous column-header close behavior

#### Scenario: Empty version renders an empty state

- **WHEN** the selected version's artifact read returns `{ artifacts: [] }` (HTTP 200)
- **THEN** the panel MUST render the empty state (`artifact-list-empty`), distinct from a not-found/error state

#### Scenario: Download mints a fresh URL per click

- **WHEN** the user clicks download on an artifact row
- **THEN** the panel MUST invoke the presign mutation (re-minting the URL each time) and navigate the browser to the returned `url` (a same-origin relative download path), never reusing a cached URL

#### Scenario: Download failure surfaces exactly one error

- **WHEN** the presign action fails (`artifact_not_found` 404 or `internal_error` 500)
- **THEN** the failure MUST surface as a single toast from the panel's `onError`, with no second toast from the transport or mutation cache

### Requirement: Lightweight Artifact Content Preview

The Artifact Preview panel SHALL provide an in-panel preview for a **single user-selected artifact** (not eagerly for the whole list). All preview loads use the freshly minted **same-origin signed download URL** returned by the presign action (an opaque relative path served by the API's download proxy); the browser never loads artifact bytes from OSS. For an `image/*` artifact the panel MUST render the signed download URL directly via an `<img>`. For a **`text/html` artifact the panel MUST default to a rendered view**: a sandboxed `<iframe>` whose `src` is a freshly minted signed download URL and whose `sandbox` attribute grants **`allow-scripts` only — never `allow-same-origin`** (the document runs in an opaque origin, isolated from the app's cookies, storage, and DOM; the download response's own `Content-Security-Policy: sandbox` header provides the same isolation as defense in depth). The toolbar MUST offer a **render / source toggle** for HTML artifacts, rendered as a **prominent labeled button with an icon** (not a text-only ghost affordance): in the rendered view the button MUST present the switch-to-source action (icon + label), and in the source view the switch-to-render action; the source view reuses the text preview path below. Failure handling for the rendered view distinguishes what the browser can and cannot detect: a **presign failure** for the rendered view MUST surface as a single inline preview error with the Refresh affordance (no toast); an HTTP or network failure **inside** the iframe (e.g. an expired token returning an error document) is NOT reliably detectable from a sandboxed opaque-origin iframe (the error document fires `load`, and the opaque origin forbids content inspection) — the panel MUST NOT pretend to detect it, and instead the always-present toolbar Refresh is the documented recovery affordance. To minimize that window, the iframe MUST be mounted immediately after minting (fresh-URL load). For a non-HTML text-like artifact (`mime` beginning `text/`, or a JSON/YAML mime) the panel MUST fetch the signed download URL and render a truncated text preview bounded by a fixed byte cap (default 64KB); content beyond the cap MUST be elided with a "download to view full" affordance. For any other type the panel MUST offer only the download action, with no inline preview. Preview fetches (image `<img>` load, iframe load, and text `fetch`) MUST be triggered by selecting an artifact, MUST NOT fire from merely switching the selected version, and MUST reuse the non-cached presign action (re-minting per selection).

The toolbar **Copy** action MUST copy the loaded preview text (including the HTML source view) to the clipboard via the async clipboard API. Copy MUST be enabled only when the full text content is loaded within the byte cap; when the content is truncated, when the artifact has no text preview (image/binary, or HTML still in rendered view with no source loaded), or when the clipboard API is unavailable, Copy MUST be disabled with a reason rather than copying partial content. The toolbar **Refresh** action MUST re-mint a signed download URL for the selected artifact and replay its preview (reload the iframe / re-fetch the text / reload the image).

Because the preview loads bytes from the app's own origin, no cross-origin gate applies: the text-preview `fetch` is a plain same-origin request (no CORS precondition, no CORS-specific degradation path), and CSP needs only `'self'` (see "Content Security Policy for OSS Preview" below). A text-preview `fetch` failure (network or non-2xx) MUST surface as a single inline preview error with the download action still offered.

#### Scenario: HTML artifact renders in a sandboxed iframe by default

- **WHEN** the user selects a `text/html` artifact in the panel
- **THEN** the panel MUST render a sandboxed `<iframe>` (sandbox granting `allow-scripts` and NOT `allow-same-origin`) pointed at a freshly minted same-origin signed download URL, filling the preview area

#### Scenario: Render / source toggle switches views

- **WHEN** the user toggles an HTML artifact's preview from render to source
- **THEN** the panel MUST show the truncated text preview of the HTML source (subject to the text-preview byte cap), and toggling back MUST return to the rendered iframe without re-selecting the artifact

#### Scenario: Toggle renders as an icon-labeled button

- **WHEN** an HTML artifact is selected
- **THEN** the toolbar view toggle MUST render as a visible button carrying both an icon and a text label naming the view it switches to, in both the rendered and the source state

#### Scenario: Rendered-view presign failure degrades to inline error with Refresh

- **WHEN** the presign request for the rendered HTML view fails (`artifact_not_found` or `internal_error`)
- **THEN** the panel MUST show a single inline preview error with the Refresh affordance (no toast, no iframe mounted), and activating Refresh MUST re-attempt the presign and mount the iframe on success

#### Scenario: In-frame load failure recovers via Refresh

- **GIVEN** a mounted rendered view whose signed URL has since expired or whose load failed inside the frame (not detectable by the host page)
- **WHEN** the user activates the toolbar Refresh
- **THEN** the panel MUST re-mint a signed download URL and remount the iframe, restoring the rendered view

#### Scenario: Copy copies the loaded text

- **WHEN** the user activates Copy while a text-like artifact (or an HTML source view) is fully loaded within the byte cap
- **THEN** the loaded text MUST be written to the clipboard and a success confirmation shown

#### Scenario: Copy refuses partial content

- **WHEN** the loaded preview text was truncated at the byte cap
- **THEN** Copy MUST be disabled with a reason directing the user to download the full file, and MUST NOT copy the truncated content

#### Scenario: Image artifact previews inline

- **WHEN** the user selects an `image/png` artifact in the panel
- **THEN** the panel MUST render an `<img>` pointed at a freshly minted same-origin signed download URL

#### Scenario: Text artifact previews truncated

- **WHEN** the user selects a `text/plain` artifact larger than the byte cap
- **THEN** the panel MUST show the first cap-bytes as text and indicate the content is truncated with a download affordance for the full file

#### Scenario: Text preview fetch failure degrades to a single inline error

- **WHEN** the text-preview `fetch` of the signed download URL fails (network or non-2xx)
- **THEN** the panel MUST show exactly one inline preview-error affordance (distinct from the download error path, never a duplicate toast) and MUST still offer the download action

#### Scenario: Binary artifact offers download only

- **WHEN** the user selects an artifact whose mime is neither image nor text-like (e.g. `application/octet-stream`)
- **THEN** the panel MUST show no inline preview and MUST offer the download action

### Requirement: Content Security Policy for OSS Preview

The application's HTML `Content-Security-Policy` (in `web/index.html`) SHALL treat artifact previews as same-origin content: previews load from the API's download proxy route on the app's own origin, so the `img-src` and `frame-src` directives MUST NOT permit any OSS origin (no `https:` blanket, no enumerated OSS hosts such as `http://localhost:9000`). `img-src` MUST be limited to `'self'` plus `data:`; `frame-src` MUST be `'self'`. The `connect-src` directive MUST be tightened to `'self' ws: wss:` — the `http:`/`https:` blanket sources existed solely for the cross-origin OSS text-preview `fetch`, whose rationale disappears with this change; `ws:`/`wss:` remain for the realtime WebSocket client. The rest of the policy (`script-src 'self'`, `object-src 'none'`, `frame-ancestors 'none'`, `base-uri 'self'`) MUST remain locked down, and every preview iframe MUST carry a `sandbox` attribute that never combines `allow-scripts` with `allow-same-origin`.

#### Scenario: Same-origin image preview is not blocked by CSP

- **WHEN** the panel renders an `<img>` whose `src` is a same-origin signed download URL
- **THEN** the image MUST load with no CSP `img-src` violation in the browser console

#### Scenario: Same-origin HTML iframe is not blocked by CSP

- **WHEN** the panel renders the sandboxed preview `<iframe>` whose `src` is a same-origin signed download URL
- **THEN** the document MUST load with no CSP `frame-src` violation in the browser console

#### Scenario: OSS origins are no longer whitelisted

- **WHEN** the CSP meta tag is inspected
- **THEN** `img-src` MUST equal `'self' data:`, `frame-src` MUST equal `'self'`, and `connect-src` MUST equal `'self' ws: wss:` — none may contain `http:`, `https:`, or any OSS host

#### Scenario: Script policy stays locked down

- **WHEN** the CSP is updated for same-origin previews
- **THEN** `script-src` MUST remain `'self'` (no inline/eval), `object-src`/`frame-ancestors` MUST remain `'none'`, and no preview iframe may combine `allow-scripts` with `allow-same-origin`
