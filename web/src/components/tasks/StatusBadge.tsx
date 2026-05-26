import type { JSX } from "react";

/** Map a task/version status to a semantic text colour (palette-only, no
 *  arbitrary hex per the tailwind lint rule). */
const STATUS_COLOR: Record<string, string> = {
  pending: "text-text-muted",
  queued: "text-text-muted",
  running: "text-accent",
  paused: "text-warning",
  cancelling: "text-warning",
  cancelled: "text-text-muted",
  succeeded: "text-success",
  failed: "text-danger",
};

export interface StatusBadgeProps {
  status: string;
}

export function StatusBadge({ status }: StatusBadgeProps): JSX.Element {
  const color = STATUS_COLOR[status] ?? "text-text-muted";
  return (
    <span
      data-testid="status-badge"
      data-status={status}
      className={["inline-flex items-center rounded border border-border bg-surface px-2 py-1 text-xs font-medium", color].join(
        " ",
      )}
    >
      {status}
    </span>
  );
}
