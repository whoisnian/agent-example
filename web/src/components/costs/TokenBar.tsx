import type { JSX } from "react";
import type { CostSummary } from "@/features/tasks/types";
import { formatAmount } from "@/features/costs/format";

/**
 * Presentational cost breakdown: the amount (decimal-string-safe, via
 * `formatAmount`) plus the token / tool / wall-time fields. Used on the
 * CostDashboard (ungrouped total) and the TaskDetail cost panel. The amount is
 * never parsed to a float for display; the token/tool/wall fields are JSON
 * numbers and render as integers.
 */
export interface TokenBarProps {
  cost: CostSummary;
}

function num(n: number): string {
  return n.toLocaleString("en-US");
}

export function TokenBar({ cost }: TokenBarProps): JSX.Element {
  return (
    <div data-testid="token-bar" className="flex flex-col gap-1">
      <span
        data-testid="token-bar-amount"
        title={`${cost.amount_usd} USD`}
        className="font-mono text-xl font-semibold text-foreground"
      >
        {formatAmount(cost.amount_usd)}
      </span>
      <dl className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>
          <dt className="inline">in</dt>{" "}
          <dd className="inline font-mono">{num(cost.input_tokens)}</dd>
        </span>
        <span>
          <dt className="inline">out</dt>{" "}
          <dd className="inline font-mono">{num(cost.output_tokens)}</dd>
        </span>
        <span>
          <dt className="inline">cached</dt>{" "}
          <dd className="inline font-mono">{num(cost.cached_tokens)}</dd>
        </span>
        <span>
          <dt className="inline">tools</dt>{" "}
          <dd className="inline font-mono">{num(cost.tool_calls)}</dd>
        </span>
        <span>
          <dt className="inline">wall</dt>{" "}
          <dd className="inline font-mono">{num(cost.wall_time_ms)}ms</dd>
        </span>
      </dl>
    </div>
  );
}
