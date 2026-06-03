## ADDED Requirements

### Requirement: Version Artifacts Expandable List With Direct Download

The TaskDetail page SHALL surface a version's produced artifacts inline in the version tree. Each `VersionTree` row MUST present a per-version disclosure (expand/collapse) control. The artifact list for a version MUST be **lazily** fetched only when its row is first expanded (consuming the `features/artifacts/` slice keyed by that `version_id`), so collapsed versions issue no artifact request. Because the backend exposes artifacts strictly per version (no task-level aggregate), the surface MUST allow any version row — not only the current version — to be expanded.

When expanded, the row MUST render distinct loading, empty, and error states for that version's artifacts: a pending fetch shows a loading affordance; an empty result (`artifacts: []`) shows an explicit "no artifacts" message (NOT a not-found or error screen); and a transport/server failure shows a per-version error affordance without crashing the page or collapsing sibling rows. Each artifact entry MUST display its `kind`, `mime` (or a neutral placeholder when `null`), a human-readable size derived from `bytes` (with a neutral placeholder when `bytes` is `null`), and a **Download** action.

The Download action MUST mint a fresh presigned URL via the `features/artifacts/` presign action and then hand the browser directly to that URL (e.g. an anchor navigation), so artifact bytes are downloaded straight from OSS and never proxied through the web app. The app MUST NOT cache or reuse a previously minted URL for a subsequent click — each Download re-mints. A presign failure MUST surface a user-visible error (toast and/or inline) and MUST NOT leave the row in a silent broken state.

The expandable artifact surface MUST NOT alter the existing version-tree behavior (parent-linked flattening, status/cost badges, the `current` marker) and MUST NOT block on the artifact fetch — the tree renders immediately and artifacts load per expansion.

#### Scenario: Expanding a version lazily loads and lists its artifacts

- **GIVEN** the TaskDetail version tree is rendered with a version that produced two artifacts
- **WHEN** the user expands that version's row for the first time
- **THEN** the page MUST issue exactly one artifact-list request for that `version_id`, render the two entries in server order each with `kind`, `mime`, a human-readable size, and a Download action, and MUST NOT have issued any artifact request for still-collapsed rows

#### Scenario: Expanding a version with no artifacts shows an empty state

- **GIVEN** a version whose artifact list is `[]`
- **WHEN** the user expands its row
- **THEN** the row MUST show an explicit "no artifacts" empty message, NOT a not-found or error screen

#### Scenario: Download mints a fresh URL and navigates to OSS directly

- **GIVEN** an expanded version listing an artifact
- **WHEN** the user clicks Download
- **THEN** the app MUST request a fresh presigned URL for that artifact and navigate the browser to the returned `url` (direct OSS download, bytes not proxied through the app), and a second click MUST re-mint rather than reuse the prior URL

#### Scenario: A presign failure surfaces a single error, tree stays intact

- **WHEN** the Download presign request fails (`artifact_not_found` or `internal_error`)
- **THEN** the page MUST surface exactly one visible error (a single toast and/or inline hint, never a duplicate toast), MUST NOT navigate, and MUST keep the version tree and other expanded rows rendered

#### Scenario: Nullable artifact metadata renders with placeholders

- **GIVEN** an expanded version with an artifact whose `mime` and `bytes` are `null`
- **THEN** the entry MUST still render with a neutral placeholder for the missing `mime` and size and MUST still offer the Download action (which echoes the same nullable fields from presign)
