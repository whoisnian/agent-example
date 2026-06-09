# web-artifact-preview Specification

## Purpose
三栏布局右栏的 Artifact 预览面板：基于选中版本展示产物列表、空态、按需 presign 下载与轻量预览的 UI 行为契约（数据访问仍由既有 `web-artifacts-views` 提供）。

## Requirements
### Requirement: Artifact Preview Panel

The web client SHALL render an Artifact Preview panel as the right column of the three-column shell. The panel MUST be data-anchored to a single **selected version** id sourced from the global UI store (`selectedVersionId`), which defaults to the task's `current_version` and is updated when the user selects a version in the Task Detail version tree. The panel MUST consume the existing `features/artifacts/` data access (`useVersionArtifactsQuery`, `useArtifactPresignMutation`) and MUST NOT introduce a new artifacts transport. When no version is selected (e.g. a task with no current version), the panel MUST render a neutral empty/placeholder state, not an error.

The panel MUST list the selected version's artifacts in the server-provided order (no client re-sort), each row showing `kind`, a mime label (`—` when null), and a size label (human-readable bytes, `—` when null). Download MUST mint a fresh presigned URL on each click (never cached) and navigate the browser directly to OSS. The panel MUST render distinct loading, empty (`artifacts: []`), and error states, preserving the single-error-surface convention: the list read stays silent at both toast layers (`meta.silent` + `toastOnError:false`) and the panel's own `onError` is the sole error surface (no double toast). Existing artifact `data-testid` selectors (`artifact-row`, `artifact-download`, `artifact-list-empty`, `artifact-list-loading`, `artifact-list-error`) MUST be preserved so existing contract tests continue to apply.

#### Scenario: Panel reflects the selected version

- **WHEN** the user selects a version in the Task Detail version tree
- **THEN** the preview panel MUST update `selectedVersionId` and list that version's artifacts via `useVersionArtifactsQuery`, in the server-provided order

#### Scenario: Empty version renders an empty state

- **WHEN** the selected version's artifact read returns `{ artifacts: [] }` (HTTP 200)
- **THEN** the panel MUST render the empty state (`artifact-list-empty`), distinct from a not-found/error state

#### Scenario: Download mints a fresh URL per click

- **WHEN** the user clicks download on an artifact row
- **THEN** the panel MUST invoke the presign mutation (re-minting the URL each time) and navigate the browser to the returned `url`, never reusing a cached URL

#### Scenario: Download failure surfaces exactly one error

- **WHEN** the presign action fails (`artifact_not_found` 404 or `internal_error` 500)
- **THEN** the failure MUST surface as a single toast from the panel's `onError`, with no second toast from the transport or mutation cache

### Requirement: Lightweight Artifact Content Preview

The Artifact Preview panel SHALL provide an in-panel lightweight preview for a **single user-selected artifact** (not eagerly for the whole list). For an `image/*` artifact the panel MUST render the presigned URL directly via an `<img>` (bytes never proxied through the app). For a text-like artifact (`mime` beginning `text/`, or a JSON/YAML mime) the panel MUST fetch the presigned URL and render a truncated text preview bounded by a fixed byte cap (default 64KB); content beyond the cap MUST be elided with a "download to view full" affordance. For any other type the panel MUST offer only the download action, with no inline preview. Preview fetches (image `<img>` load and text `fetch`) MUST be triggered by selecting an artifact, MUST NOT fire from merely switching the selected version, and MUST reuse the non-cached presign action (re-minting per selection).

Because the preview loads bytes from OSS in the browser, two cross-origin gates apply and MUST be satisfied: (1) the HTML `Content-Security-Policy` `img-src` directive MUST permit the OSS image source (see "Content Security Policy for OSS Preview" below); (2) the text-preview `fetch` against the presigned OSS URL is subject to CORS — the OSS bucket MUST return an `Access-Control-Allow-Origin` permitting the app origin, otherwise the text preview MUST degrade to download-only with a clear inline message rather than throwing an unhandled error.

#### Scenario: Image artifact previews inline

- **WHEN** the user selects an `image/png` artifact in the panel
- **THEN** the panel MUST render an `<img>` pointed at a freshly presigned URL, without proxying bytes through the application

#### Scenario: Text artifact previews truncated

- **WHEN** the user selects a `text/plain` artifact larger than the byte cap
- **THEN** the panel MUST show the first cap-bytes as text and indicate the content is truncated with a download affordance for the full file

#### Scenario: Text preview fetch failure degrades to a single inline error

- **WHEN** the text-preview `fetch` of the presigned OSS URL fails (network, CORS, or non-2xx)
- **THEN** the panel MUST show exactly one inline preview-error affordance (distinct from the download error path, never a duplicate toast) and MUST still offer the download action

#### Scenario: Binary artifact offers download only

- **WHEN** the user selects an artifact whose mime is neither image nor text-like (e.g. `application/octet-stream`)
- **THEN** the panel MUST show no inline preview and MUST offer the download action

### Requirement: Content Security Policy for OSS Preview

The application's HTML `Content-Security-Policy` (in `web/index.html`) SHALL be updated so that presigned OSS artifact previews load rather than being blocked. The `img-src` directive MUST permit the OSS origin used for presigned URLs (either by enumerating the OSS host(s) or, given multi-bucket/multi-region OSS per `docs/ARCHITECTURE.md`, by allowing `https:`), while keeping the rest of the policy (`script-src 'self'`, `object-src 'none'`, `frame-ancestors 'none'`) intact. The existing `connect-src 'self' ws: wss: http: https:` already permits the text-preview `fetch` (subject to CORS, above).

#### Scenario: Presigned image is not blocked by CSP

- **WHEN** the panel renders an `<img>` whose `src` is a presigned OSS URL
- **THEN** the image MUST load with no CSP `img-src` violation in the browser console

#### Scenario: Script policy stays locked down

- **WHEN** the CSP is updated to permit OSS images
- **THEN** `script-src` MUST remain `'self'` (no inline/eval), and `object-src`/`frame-ancestors` MUST remain `'none'`
