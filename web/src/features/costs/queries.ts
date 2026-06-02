/** React Query keys + read hooks for the cost views. */
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import { getMyCost, getTaskCost, type MyCostParams } from "./api";
import type { CostRollup, TaskCostBreakdown } from "./types";

export const costKeys = {
  all: ["cost"] as const,
  rollup: (params: MyCostParams) => ["cost", "me", params] as const,
  taskCost: (taskId: string) => ["cost", "task", taskId] as const,
};

export function useMyCostQuery(params: MyCostParams): UseQueryResult<CostRollup, ApiError> {
  return useQuery({
    queryKey: costKeys.rollup(params),
    queryFn: ({ signal }) => getMyCost(params, signal),
    // The dashboard renders its own in-page error (with a retry hint); don't
    // auto-retry-loop on a 5xx, and suppress the cache toast (the transport
    // toast is already suppressed in getMyCost).
    retry: false,
    meta: { silent: true },
  });
}

export function useTaskCostQuery(taskId: string): UseQueryResult<TaskCostBreakdown, ApiError> {
  return useQuery({
    queryKey: costKeys.taskCost(taskId),
    queryFn: ({ signal }) => getTaskCost(taskId, signal),
    enabled: !!taskId,
    // Mirror useTaskQuery: a 404 is a render state, not an error to retry. This
    // suppresses the cache toast; the panel is a defensive no-op on the (near
    // impossible, since the page is already gated by useTaskQuery) 404.
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 404) && failureCount < 2,
    meta: { silent: true },
  });
}
