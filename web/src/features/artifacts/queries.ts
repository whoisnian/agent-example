/** React Query keys + hooks for the artifact views. */
import {
  useMutation,
  useQuery,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import { getArtifactPresign, getVersionArtifacts } from "./api";
import type { PresignResult, VersionArtifacts } from "./types";

export const artifactKeys = {
  all: ["artifacts"] as const,
  byVersion: (versionId: string) => ["artifacts", "version", versionId] as const,
};

/**
 * Lazily reads a version's artifact list. `enabled:!!versionId` so it only fires
 * when a consumer (an expanded VersionTree row) actually asks. Mirrors
 * `useTaskCostQuery`: a 404 (`version_not_found`) is a render state, not an error
 * to retry, and the cache toast is suppressed (`meta.silent`). The owned-but-
 * empty case is `artifacts: []` (HTTP 200), distinguished from 404 by the caller.
 */
export function useVersionArtifactsQuery(
  versionId: string,
): UseQueryResult<VersionArtifacts, ApiError> {
  return useQuery({
    queryKey: artifactKeys.byVersion(versionId),
    queryFn: ({ signal }) => getVersionArtifacts(versionId, signal),
    enabled: !!versionId,
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 404) && failureCount < 2,
    meta: { silent: true },
  });
}

/**
 * Presign action. Modeled as a mutation (not a cached query) because the URL is
 * a short-lived credential, not cacheable server state — each call re-mints.
 * `meta:{silent:true}` so the global `mutationCache.onError` does NOT toast;
 * paired with `getArtifactPresign`'s `toastOnError:false`, the calling
 * component's `onError` is the single error surface (no double toast).
 */
export function useArtifactPresignMutation(): UseMutationResult<PresignResult, ApiError, string> {
  return useMutation({
    mutationFn: (artifactId: string) => getArtifactPresign(artifactId),
    meta: { silent: true },
  });
}
