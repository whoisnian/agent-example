import type { JSX } from "react";
import { Button } from "@/components/ui/button";
import { controlAvailability, type ControlAction } from "@/features/tasks/types";

export interface ControlBarProps {
  /** Current task status; drives which actions are enabled. */
  status: string;
  /** True while a control request is in flight — disables all actions. */
  pending: boolean;
  onAction: (action: ControlAction) => void;
}

/**
 * Presentational pause/resume/cancel bar for TaskDetail. Enablement mirrors the
 * task-control-api preconditions (advisory — the API stays authoritative); a
 * disabled button explains why via `title`. While a request is in flight every
 * action is disabled to avoid accidental double submission.
 */
export function ControlBar({ status, pending, onAction }: ControlBarProps): JSX.Element {
  const { canPause, canResume, canCancel } = controlAvailability(status);

  const pauseReason = canPause ? undefined : "Only a pending or running task can be paused";
  const resumeReason = canResume ? undefined : "Only a paused task can be resumed";
  const cancelReason = canCancel ? undefined : "This task is already finished";

  return (
    <div data-testid="control-bar" className="flex items-center gap-2">
      <Button
        data-testid="control-pause"
        variant="ghost"
        disabled={pending || !canPause}
        title={pending ? "A control request is in progress" : pauseReason}
        onClick={() => onAction("pause")}
      >
        Pause
      </Button>
      <Button
        data-testid="control-resume"
        variant="ghost"
        disabled={pending || !canResume}
        title={pending ? "A control request is in progress" : resumeReason}
        onClick={() => onAction("resume")}
      >
        Resume
      </Button>
      <Button
        data-testid="control-cancel"
        variant="destructive"
        disabled={pending || !canCancel}
        title={pending ? "A control request is in progress" : cancelReason}
        onClick={() => onAction("cancel")}
      >
        Cancel
      </Button>
    </div>
  );
}
