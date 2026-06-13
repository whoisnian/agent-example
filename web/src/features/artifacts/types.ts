/**
 * TypeScript mirrors of the `artifacts-api` DTOs the web consumes
 * (`/versions/{id}/artifacts`, `/artifacts/{id}/presign`).
 *
 * The list endpoint never leaks `oss_key`; the browser obtains bytes only
 * through the presign endpoint. `mime`, `bytes`, and `sha256` are nullable in
 * the table and arrive present-and-null (never omitted), so they are modeled as
 * `T | null` — NOT optional.
 *
 * NOTE: `bytes` is a plain integer file size (int8), not money. It MAY be used
 * as a `number` for human-readable formatting (see ./format.ts). The decimal-
 * STRING discipline applies ONLY to monetary `amount_usd` values (features/costs).
 */

export interface ArtifactMeta {
  id: string;
  /** Opaque free text recorded by the worker (today always "file"). Never
   *  validated or enumerated client-side. */
  kind: string;
  /** Version-relative file path (e.g. `index.html`, `css/style.css`). The
   *  preferred display label; nullable for legacy rows (fall back to `kind`). */
  path: string | null;
  mime: string | null;
  bytes: number | null;
  sha256: string | null;
  created_at: string;
}

/** `GET /versions/{version_id}/artifacts`. `artifacts` arrive
 *  `created_at ASC, id ASC`; render in that order, never re-sort. An owned
 *  version with no artifacts returns `artifacts: []` (HTTP 200, never 404). */
export interface VersionArtifacts {
  version_id: string;
  artifacts: ArtifactMeta[];
}

/** `GET /artifacts/{artifact_id}/presign`. Short-lived single-object GET URL
 *  plus the echoed (nullable) metadata so the client can label the download
 *  without a second call. `expires_at` is advisory (OSS is the authority). */
export interface PresignResult {
  url: string;
  expires_at: string;
  bytes: number | null;
  mime: string | null;
  sha256: string | null;
}

/** `GET /versions/{version_id}/artifacts/archive/presign`. A short-lived
 *  API-relative URL that streams a zip of the version's artifacts. */
export interface ArchivePresignResult {
  url: string;
  expires_at: string;
}

/** `GET /versions/{version_id}/preview`. An opaque API-relative base URL
 *  (`/api/v1/versions/{id}/preview/<token>`) under which a rendered HTML
 *  artifact's relative asset references resolve to sibling artifacts. Compose
 *  per-file URLs as `base_url + "/" + <segment-encoded path>`; never parse the
 *  token segment. `expires_at` is advisory. */
export interface PreviewMintResult {
  base_url: string;
  expires_at: string;
}
