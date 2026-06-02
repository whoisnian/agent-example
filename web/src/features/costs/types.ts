/**
 * TypeScript mirrors of the `task-cost-api` DTOs the web cost views consume
 * (`/me/cost`, `/tasks/{id}/cost`).
 *
 * IMPORTANT: `amount_usd` is a decimal STRING (NUMERIC(18,8) rendered to 8 dp,
 * e.g. "0.06750000"). Never parse it to `number` for display — the API
 * deliberately avoids float rounding and so must the UI (see CostBadge /
 * features/costs/format.ts).
 */
import type { CostSummary } from "@/features/tasks/types";

export type { CostSummary };

/** The three groupings `/me/cost` accepts. `none` is a UI-only sentinel for the
 *  ungrouped (lifetime total) request — it is never sent on the wire. */
export const COST_GROUP_BYS = ["day", "task_type", "model"] as const;
export type CostGroupBy = (typeof COST_GROUP_BYS)[number];

export interface CostGroupItem {
  key: string;
  totals: CostSummary;
}

/**
 * The discriminated `/me/cost` response. The backend guarantees the payload
 * carries ONLY `total` (ungrouped) OR ONLY `group_by` + `items` (grouped), so
 * consumers branch on the presence of the `group_by` key — never on optional
 * fields. `items` arrive `key`-ascending and MUST be rendered in that order.
 */
export type CostRollup = { total: CostSummary } | { group_by: CostGroupBy; items: CostGroupItem[] };

export interface TaskVersionCost {
  version_id: string;
  version_no: number;
  created_at: string;
  cost: CostSummary;
}

/**
 * `/tasks/{id}/cost` response. NOTE: `by_version` is modeled for response
 * fidelity but is intentionally UNCONSUMED this round — per-version cost already
 * renders in `VersionTree` from the task read DTO. Kept so the type matches the
 * contract verbatim.
 */
export interface TaskCostBreakdown {
  task_id: string;
  total: CostSummary;
  by_version: TaskVersionCost[];
}
