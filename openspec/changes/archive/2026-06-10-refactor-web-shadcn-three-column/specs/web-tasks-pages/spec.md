## MODIFIED Requirements

### Requirement: Version Artifacts Expandable List With Direct Download

The TaskDetail page SHALL surface a version's produced artifacts through **row selection in the version tree driving the right-column Artifact Preview panel**, replacing the previous in-row expand/collapse disclosure. Each `VersionTree` row MUST present a **selection** control (e.g. row click with `aria-selected`/`data-selected`, exposed via a stable `version-select` affordance); selecting a row MUST set the global UI store `selectedVersionId` to that row's `version_id`, and the right-column Artifact Preview panel (see `web-artifact-preview`) MUST render that version's artifacts. Because the backend exposes artifacts strictly per version (no task-level aggregate), the tree MUST allow **any** version row — not only the current version — to be selected; the default selection on first render MUST be the task's `current_version` (or no selection when there is none). The artifact list for the selected version MUST be **lazily** fetched (consuming the `features/artifacts/` slice keyed by that `version_id`); switching selection re-keys the read so only the selected version issues a request.

The Artifact Preview panel MUST render distinct loading, empty, and error states for the selected version's artifacts: a pending fetch shows a loading affordance; an empty result (`artifacts: []`) shows an explicit "no artifacts" empty message (NOT a not-found or error screen); and a transport/server failure shows an error affordance without crashing the page. Each artifact entry MUST display its `kind`, `mime` (or a neutral placeholder when `null`), a human-readable size derived from `bytes` (with a neutral placeholder when `bytes` is `null`), and a **Download** action. These rendering details are owned by `web-artifact-preview`; this requirement governs only the version-tree selection contract and its binding to the panel.

The Download action MUST mint a fresh presigned URL via the `features/artifacts/` presign action and then hand the browser directly to that URL (e.g. an anchor navigation), so artifact bytes are downloaded straight from OSS and never proxied through the web app. The app MUST NOT cache or reuse a previously minted URL for a subsequent click — each Download re-mints. A presign failure MUST surface exactly one user-visible error (toast and/or inline) and MUST NOT leave the panel in a silent broken state.

Selecting a version MUST NOT alter the existing version-tree behavior (parent-linked flattening, status/cost badges, the `current` marker) and MUST NOT block the tree on the artifact fetch — the tree renders immediately and the panel loads per selection.

#### Scenario: Selecting a version drives the preview panel and lazily loads its artifacts

- **GIVEN** the TaskDetail version tree is rendered with a version that produced two artifacts
- **WHEN** the user selects that version's row
- **THEN** the UI store `selectedVersionId` MUST become that `version_id`, the page MUST issue exactly one artifact-list request for it, and the right-column Artifact Preview panel MUST render the two entries in server order each with `kind`, `mime`, a human-readable size, and a Download action

#### Scenario: Any version (not only current) is selectable

- **GIVEN** a version tree with multiple versions where the current version is not the oldest
- **WHEN** the user selects a non-current version row
- **THEN** the panel MUST load and show that selected version's artifacts (not the current version's)

#### Scenario: Selecting a version with no artifacts shows an empty state

- **GIVEN** a version whose artifact list is `[]`
- **WHEN** the user selects its row
- **THEN** the preview panel MUST show an explicit "no artifacts" empty message, NOT a not-found or error screen

#### Scenario: Download mints a fresh URL and navigates to OSS directly

- **GIVEN** a selected version listing an artifact
- **WHEN** the user clicks Download
- **THEN** the app MUST request a fresh presigned URL for that artifact and navigate the browser to the returned `url` (direct OSS download, bytes not proxied through the app), and a second click MUST re-mint rather than reuse the prior URL

#### Scenario: A presign failure surfaces a single error, tree stays intact

- **WHEN** the Download presign request fails (`artifact_not_found` or `internal_error`)
- **THEN** the page MUST surface exactly one visible error (a single toast and/or inline hint, never a duplicate toast), MUST NOT navigate, and MUST keep the version tree and the preview panel rendered

#### Scenario: Nullable artifact metadata renders with placeholders

- **GIVEN** a selected version with an artifact whose `mime` and `bytes` are `null`
- **THEN** the entry MUST still render with a neutral placeholder for the missing `mime` and size and MUST still offer the Download action (which echoes the same nullable fields from presign)
