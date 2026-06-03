## Why

The backend `artifacts-api` is green — it exposes a per-version artifact metadata list (`GET /versions/{version_id}/artifacts`) and a short-lived single-object presigned download URL (`GET /artifacts/{artifact_id}/presign`) — but the web client consumes neither. A version's produced files (report.md, code bundles, etc.) are completely invisible in the UI: `VersionTree` renders status and cost per node but offers no way to see or download what a run actually produced. ARCHITECTURE §3.1 lists "中间产物" as a first-class TaskDetail surface; this change delivers it.

## What Changes

- Add a `features/artifacts/` data-access slice: typed `apiFetch` wrappers + React Query access for the per-version artifact list, plus a presign action that mints a fresh single-object download URL on demand.
- Make each `VersionTree` row **expandable**: a per-version disclosure that lazy-fetches that version's artifact list when first opened (artifacts are strictly per-version; there is no task-level aggregate endpoint). Each artifact row shows `kind`, `mime`, human-readable size, and a **Download** button.
- Download flow never proxies bytes through the app: clicking Download mints a presigned GET URL via the API and hands the browser straight to OSS. A stale/expired URL is re-minted on the next click (presign results are never cached).
- Distinct loading / empty (`artifacts: []`, the owned-but-empty case the backend zero-fills, never 404) / error states per expanded version.
- MSW fixtures for both endpoints (`/versions/:id/artifacts`, `/artifacts/:id/presign`) + an `artifactFixture` helper, so the surface and the slice are unit/integration tested.

## Capabilities

### New Capabilities
- `web-artifacts-views`: the `features/artifacts/` slice — typed access to the per-version artifact list and the on-demand presign action, including nullable-metadata fidelity, owner-scoped 404 semantics, and the "presign is never cached" rule.

### Modified Capabilities
- `web-tasks-pages`: append one requirement covering the **per-version expandable artifact list with direct-OSS download** wired into `VersionTree` on the TaskDetail page (the UI surface that consumes the new slice). Existing requirements are untouched (9 → 10).

## Impact

- **Code (web only)**: new `web/src/features/artifacts/` (`types.ts`, `api.ts`, `queries.ts`, `format.ts`); new `web/src/components/tasks/ArtifactList.tsx`; `VersionTree.tsx` gains a per-row disclosure; `web/src/test/mocks/handlers.ts` gains two routes + an `artifactFixture`.
- **No backend change**: `artifacts-api` is already archived and green; this is a pure consumer.
- **No new dependencies**: download uses an anchor/navigation to the presigned URL — no charting/upload libs, consistent with the cost round.
- **Out of scope (unchanged)**: file *upload* (`/uploads/sts` is `[Deferred]` per ARCHITECTURE §A and `add-artifacts-api` D5); no client-side hashing/preview/rendering of artifact contents.
