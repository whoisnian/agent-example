import type { JSX } from "react";
import type { CostSummary } from "@/features/tasks/types";
import { formatAmount } from "@/features/costs/format";

/**
 * Renders `amount_usd` for display. The value is a decimal STRING; we never
 * parse it to a number. `formatAmount` trims to 4 fractional digits by string
 * slicing (truncation, not rounding); the full value stays in the title.
 */
export interface CostBadgeProps {
  cost: CostSummary;
}

export function CostBadge({ cost }: CostBadgeProps): JSX.Element {
  const title =
    `amount=${cost.amount_usd} USD · in=${cost.input_tokens} out=${cost.output_tokens} ` +
    `cached=${cost.cached_tokens} tools=${cost.tool_calls} wall=${cost.wall_time_ms}ms`;
  return (
    <span data-testid="cost-badge" title={title} className="font-mono text-xs text-text-muted">
      {formatAmount(cost.amount_usd)}
    </span>
  );
}
