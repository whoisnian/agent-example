import type { JSX } from "react";
import { useState } from "react";
import { Button } from "@/components/primitives/Button";
import type { RollbackMode } from "@/features/tasks/types";

export interface RollbackControlProps {
  /** Disable the branch action (task active). */
  branchDisabled: boolean;
  branchReason?: string | undefined;
  /** Disable the switch action (task active, or target not terminal). */
  switchDisabled: boolean;
  switchReason?: string | undefined;
  /** True while a rollback request is in flight — disables both actions. */
  pending: boolean;
  onRollback: (mode: RollbackMode, prompt?: string) => void;
}

/**
 * Per-version rollback picker for a VersionTree row. Presentational: the parent
 * (TaskDetail, via VersionTree) owns the mutation, toasts, and the version id —
 * this only picks a mode and, for branch, an optional prompt. Mirrors the
 * ControlBar ↔ TaskDetail split; a disabled action explains why via `title`.
 * `switch` is immediate; `branch` reveals an optional prompt (blank is valid —
 * the backend auto-fills it).
 */
export function RollbackControl({
  branchDisabled,
  branchReason,
  switchDisabled,
  switchReason,
  pending,
  onRollback,
}: RollbackControlProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const [branchOpen, setBranchOpen] = useState(false);
  const [prompt, setPrompt] = useState("");

  const busyReason = pending ? "A rollback request is in progress" : undefined;

  return (
    <div data-testid="rollback-control" className="flex flex-col gap-1">
      <button
        type="button"
        data-testid="rollback-button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="self-start font-mono text-xs text-text-muted hover:text-text"
      >
        Rollback…
      </button>
      {open ? (
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <Button
              data-testid="rollback-switch"
              variant="ghost"
              disabled={pending || switchDisabled}
              title={busyReason ?? switchReason}
              onClick={() => onRollback("switch")}
            >
              Switch here
            </Button>
            <Button
              data-testid="rollback-branch"
              variant="ghost"
              disabled={pending || branchDisabled}
              title={busyReason ?? branchReason}
              onClick={() => setBranchOpen((v) => !v)}
            >
              Branch from here
            </Button>
          </div>
          {branchOpen ? (
            <div className="flex max-w-xl flex-col gap-2">
              <textarea
                data-testid="rollback-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                rows={3}
                placeholder="Optional — describe the change (blank rolls back as-is)…"
                className="rounded border border-border bg-surface px-2 py-1 text-text"
              />
              <div>
                <Button
                  data-testid="rollback-submit"
                  disabled={pending || branchDisabled}
                  onClick={() => onRollback("branch", prompt)}
                >
                  {pending ? "Submitting…" : "Submit branch"}
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
