import { QueryClient, QueryCache, MutationCache } from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import { useUiStore } from "@/features/ui/store";

function isSilent(meta: Record<string, unknown> | undefined): boolean {
  return Boolean(meta && meta["silent"] === true);
}

function handleError(error: unknown, meta: Record<string, unknown> | undefined): void {
  if (isSilent(meta)) return;
  if (error instanceof ApiError) {
    // 401 is already handled centrally by `apiFetch`; no toast here.
    if (error.code === "unauthenticated") return;
    useUiStore.getState().pushToast({ level: "error", message: error.message });
    return;
  }
  const msg = error instanceof Error ? error.message : "unknown error";
  useUiStore.getState().pushToast({ level: "error", message: msg });
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        retry: (failureCount, error): boolean => {
          if (error instanceof ApiError) {
            if (error.code === "unauthenticated") return false;
            if (error.status === 409) return false;
          }
          return failureCount < 2;
        },
      },
      mutations: {
        retry: false,
      },
    },
    queryCache: new QueryCache({
      onError: (error, query) => handleError(error, query.meta),
    }),
    mutationCache: new MutationCache({
      onError: (error, _vars, _ctx, mutation) => handleError(error, mutation.meta),
    }),
  });
}

/**
 * Singleton instance used by the app. Tests construct fresh clients via
 * `createQueryClient()` to avoid cross-test cache leaks.
 */
export const queryClient = createQueryClient();
