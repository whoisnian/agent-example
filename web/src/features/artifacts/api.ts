/** Thin `apiFetch` wrappers for the artifact read endpoints (`artifacts-api`). */
import { apiFetch } from "@/services/http";
import type {
  ArchivePresignResult,
  PresignResult,
  PreviewMintResult,
  VersionArtifacts,
} from "./types";

/** `GET /api/v1/versions/{version_id}/artifacts` — owner-scoped metadata list
 *  (no `oss_key`). `toastOnError:false`: `ArtifactList` renders its own in-page
 *  error/empty state per expanded row, so the inline state is the single error
 *  surface (no transport toast); the query also sets `meta.silent` for the cache
 *  layer. Mirrors `getMyCost` + `useMyCostQuery`. */
export function getVersionArtifacts(
  versionId: string,
  signal?: AbortSignal,
): Promise<VersionArtifacts> {
  return apiFetch<VersionArtifacts>(`/api/v1/versions/${versionId}/artifacts`, {
    toastOnError: false,
    ...(signal ? { signal } : {}),
  });
}

/**
 * `GET /api/v1/artifacts/{artifact_id}/presign` — mint a short-lived single-
 * object GET URL. `toastOnError:false`: the calling component owns the error UX
 * (the presign mutation is also `meta:{silent:true}`), so a failure surfaces
 * exactly once via the component's `onError`, never double-toasted by the
 * transport + the global `mutationCache.onError`.
 */
export function getArtifactPresign(
  artifactId: string,
  signal?: AbortSignal,
): Promise<PresignResult> {
  return apiFetch<PresignResult>(`/api/v1/artifacts/${artifactId}/presign`, {
    toastOnError: false,
    ...(signal ? { signal } : {}),
  });
}

/**
 * `GET /api/v1/versions/{version_id}/artifacts/archive/presign` — mint a
 * short-lived zip-archive download URL for the whole version. Silent at the
 * transport layer (the calling component owns the error UX); paired with the
 * archive-presign mutation's `meta:{silent:true}`.
 */
export function getVersionArchivePresign(
  versionId: string,
  signal?: AbortSignal,
): Promise<ArchivePresignResult> {
  return apiFetch<ArchivePresignResult>(`/api/v1/versions/${versionId}/artifacts/archive/presign`, {
    toastOnError: false,
    ...(signal ? { signal } : {}),
  });
}

/**
 * `GET /api/v1/versions/{version_id}/preview` — mint a tokenized preview base
 * URL for the version. Silent at the transport layer; the calling component
 * owns the error UX.
 */
export function getVersionPreviewMint(
  versionId: string,
  signal?: AbortSignal,
): Promise<PreviewMintResult> {
  return apiFetch<PreviewMintResult>(`/api/v1/versions/${versionId}/preview`, {
    toastOnError: false,
    ...(signal ? { signal } : {}),
  });
}
