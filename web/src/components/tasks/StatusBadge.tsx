import type { JSX } from "react";
import { Badge, type BadgeProps } from "@/components/ui/badge";

/** Map a task/version status to a shadcn Badge variant. */
const STATUS_VARIANT: Record<string, NonNullable<BadgeProps["variant"]>> = {
  pending: "secondary",
  queued: "secondary",
  running: "default",
  paused: "warning",
  cancelling: "warning",
  cancelled: "secondary",
  succeeded: "success",
  failed: "destructive",
};

export interface StatusBadgeProps {
  status: string;
}

export function StatusBadge({ status }: StatusBadgeProps): JSX.Element {
  const variant = STATUS_VARIANT[status] ?? "secondary";
  return (
    <Badge variant={variant} data-testid="status-badge" data-status={status}>
      {status}
    </Badge>
  );
}
