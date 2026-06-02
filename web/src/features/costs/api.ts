/** Thin `apiFetch` wrappers for the cost read endpoints (`task-cost-api`). */
import { apiFetch } from "@/services/http";
import type { CostGroupBy, CostRollup, TaskCostBreakdown } from "./types";

export interface MyCostParams {
  /** Omitted for the lifetime Total view; set for the grouped views. */
  groupBy?: CostGroupBy | undefined;
  /** RFC3339; sent only for grouped requests (Total is an open window). */
  from?: string | undefined;
  to?: string | undefined;
}

/**
 * `GET /api/v1/me/cost`. For the Total (ungrouped) view we send NEITHER
 * `group_by` NOR `from`/`to` — the backend's ungrouped default is an open
 * window ("everything I ever spent"). For a grouped view we send `group_by`
 * plus the RFC3339 window. The dashboard owns its error UX in-page, so this
 * read suppresses the transport toast (`toastOnError:false`); the query also
 * sets `meta.silent` for the cache layer.
 */
export function getMyCost(
  { groupBy, from, to }: MyCostParams,
  signal?: AbortSignal,
): Promise<CostRollup> {
  const q = new URLSearchParams();
  if (groupBy) {
    q.set("group_by", groupBy);
    if (from) q.set("from", from);
    if (to) q.set("to", to);
  }
  const qs = q.toString();
  return apiFetch<CostRollup>(`/api/v1/me/cost${qs ? `?${qs}` : ""}`, {
    toastOnError: false,
    ...(signal ? { signal } : {}),
  });
}

export function getTaskCost(taskId: string, signal?: AbortSignal): Promise<TaskCostBreakdown> {
  return apiFetch<TaskCostBreakdown>(`/api/v1/tasks/${taskId}/cost`, signal ? { signal } : {});
}
