import type { JSX } from "react";
import { useState } from "react";
import { useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/primitives/Button";
import { StatusBadge } from "@/components/tasks/StatusBadge";
import { CostBadge } from "@/components/tasks/CostBadge";
import { ControlBar } from "@/components/tasks/ControlBar";
import { VersionTree } from "@/components/tasks/VersionTree";
import { EventLog } from "@/components/tasks/EventLog";
import { TokenBar } from "@/components/costs/TokenBar";
import { ApiError } from "@/services/http";
import { useUiStore } from "@/features/ui/store";
import { useTaskQuery, useVersionsQuery, useVersionEventsQuery } from "@/features/tasks/queries";
import { useTaskCostQuery } from "@/features/costs/queries";
import {
  useControlTaskMutation,
  useIterateTaskMutation,
  useRollbackTaskMutation,
} from "@/features/tasks/mutations";
import {
  isActiveStatus,
  type ActiveVersionConflict,
  type ControlAction,
  type RollbackMode,
} from "@/features/tasks/types";
import { useTaskLive, liveRefetchInterval } from "@/features/tasks/use-task-live";

function isConflictData(x: unknown): x is ActiveVersionConflict {
  return (
    typeof x === "object" &&
    x !== null &&
    typeof (x as Record<string, unknown>)["active_version_id"] === "string"
  );
}

export function TaskDetail(): JSX.Element {
  const { id = "" } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const pushToast = useUiStore((s) => s.pushToast);

  const taskQuery = useTaskQuery(id);
  const task = taskQuery.data?.task;
  const isActive = task ? isActiveStatus(task.status) : false;
  const currentVersionId = task?.current_version ?? null;

  const interval = liveRefetchInterval(isActive);
  // Re-run the queries with polling now that we know active/version state.
  const versionsQuery = useVersionsQuery(id, interval);
  const eventsQuery = useVersionEventsQuery(currentVersionId, interval);
  // Cost panel source of truth (the dedicated endpoint). The inline CostBadge
  // keeps using the read DTO's cost; the two converge on refetch.
  const costQuery = useTaskCostQuery(id);

  useTaskLive(id, currentVersionId, queryClient);

  const iterate = useIterateTaskMutation();
  const control = useControlTaskMutation();
  const rollback = useRollbackTaskMutation();
  const [showIterate, setShowIterate] = useState(false);
  const [iteratePrompt, setIteratePrompt] = useState("");

  // Roll back to a historical version. The requested mode is read here (the two
  // response bodies share no discriminator); both responses carry version_no.
  // Status is NOT mutated optimistically — it flows back via the live/poll path.
  const onRollback = (versionId: string, mode: RollbackMode, prompt?: string): void => {
    rollback.mutate(
      {
        taskId: id,
        body: { target_version_id: versionId, mode, ...(prompt ? { prompt } : {}) },
      },
      {
        onSuccess: (data) => {
          pushToast({
            level: "success",
            message: `Rollback (${mode}) → version ${data.version_no}`,
          });
        },
        onError: (err) => {
          if (err instanceof ApiError && err.code === "active_version_exists") {
            const d = err.data;
            const reason = isConflictData(d)
              ? `active version ${d.active_version_id} is ${d.active_version_status}`
              : "an active version already exists";
            pushToast({ level: "warning", message: `Cannot roll back: ${reason}` });
          } else if (err instanceof ApiError && err.code === "invalid_state") {
            // e.g. switch to a non-terminal target; message names the reason.
            pushToast({ level: "warning", message: err.message });
          } else if (err instanceof ApiError) {
            pushToast({ level: "error", message: err.message });
          }
        },
      },
    );
  };

  // Issue a pause/resume/cancel control. The 202 is NOT a status change — the
  // worker flips status asynchronously and the new value arrives via useTaskLive
  // (live) / the onSettled refetch — so we only confirm, never mutate status here.
  const onControl = (action: ControlAction): void => {
    control.mutate(
      { taskId: id, body: { action } },
      {
        onSuccess: (data) => {
          pushToast({ level: "success", message: `${action} requested` });
          if (action === "cancel" && data.effective === "best_effort") {
            pushToast({
              level: "info",
              message: "No active run yet — cancel will take effect once the task is claimed.",
            });
          }
        },
        onError: (err) => {
          if (err instanceof ApiError && err.code === "invalid_state") {
            // message names the current status, e.g. cannot pause task in status "paused".
            pushToast({ level: "warning", message: err.message });
          } else if (err instanceof ApiError) {
            pushToast({ level: "error", message: err.message });
          }
        },
      },
    );
  };

  // 404 → not-found render state (the query is configured not to retry/toast it).
  if (taskQuery.error instanceof ApiError && taskQuery.error.status === 404) {
    return (
      <section data-testid="task-not-found">
        <h1 className="text-2xl font-semibold text-text">Task not found</h1>
        <p className="text-sm text-text-muted">No task with id {id}.</p>
      </section>
    );
  }

  if (taskQuery.isPending) {
    return (
      <p data-testid="task-detail-loading" className="text-sm text-text-muted">
        Loading…
      </p>
    );
  }

  const detail = taskQuery.data;
  if (!detail) {
    return (
      <p data-testid="task-detail-error" className="text-sm text-danger">
        Failed to load task.
      </p>
    );
  }
  const loadedTask = detail.task;

  const submitIterate = (): void => {
    iterate.mutate(
      { taskId: id, body: { prompt: iteratePrompt } },
      {
        onSuccess: () => {
          setShowIterate(false);
          setIteratePrompt("");
        },
        onError: (err) => {
          if (err instanceof ApiError && err.code === "active_version_exists") {
            const d = err.data;
            const detail = isConflictData(d)
              ? `active version ${d.active_version_id} is ${d.active_version_status}`
              : "an active version already exists";
            pushToast({ level: "warning", message: `Cannot iterate: ${detail}` });
          } else if (err instanceof ApiError) {
            pushToast({ level: "error", message: err.message });
          }
        },
      },
    );
  };

  return (
    <section data-testid="task-detail-page">
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-2xl font-semibold text-text">{loadedTask.title}</h1>
        <StatusBadge status={loadedTask.status} />
        <span className="text-sm text-text-muted">{loadedTask.task_type}</span>
        <CostBadge cost={detail.cost} />
        <ControlBar status={loadedTask.status} pending={control.isPending} onAction={onControl} />
      </div>

      <div data-testid="task-cost-panel" className="mb-6">
        <h2 className="mb-2 text-lg font-medium text-text">Cost</h2>
        {costQuery.data ? (
          <TokenBar cost={costQuery.data.total} />
        ) : costQuery.isPending ? (
          <p className="text-sm text-text-muted">Loading cost…</p>
        ) : (
          // A 404 here is a defensive no-op (the page is already gated by the
          // task query); render nothing rather than a second not-found screen.
          <p className="text-sm text-text-muted">Cost unavailable.</p>
        )}
      </div>

      <div className="mb-6">
        <h2 className="mb-2 text-lg font-medium text-text">Versions</h2>
        {versionsQuery.data ? (
          <VersionTree
            versions={versionsQuery.data.items}
            currentVersionId={currentVersionId}
            taskActive={isActive}
            onRollback={onRollback}
            rollbackPending={rollback.isPending}
          />
        ) : (
          <p className="text-sm text-text-muted">Loading versions…</p>
        )}
        <div className="mt-3">
          <Button
            data-testid="iterate-button"
            disabled={isActive}
            title={isActive ? "Task is busy — wait for the active version to finish" : undefined}
            onClick={() => setShowIterate((v) => !v)}
          >
            Iterate
          </Button>
          {showIterate ? (
            <div className="mt-2 flex max-w-xl flex-col gap-2">
              <textarea
                data-testid="iterate-prompt"
                value={iteratePrompt}
                onChange={(e) => setIteratePrompt(e.target.value)}
                rows={3}
                placeholder="Describe the change for the next version…"
                className="rounded border border-border bg-surface px-2 py-1 text-text"
              />
              <div>
                <Button
                  data-testid="iterate-submit"
                  disabled={iterate.isPending}
                  onClick={submitIterate}
                >
                  {iterate.isPending ? "Submitting…" : "Submit iteration"}
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      </div>

      <div>
        <h2 className="mb-2 text-lg font-medium text-text">Events</h2>
        {currentVersionId ? (
          eventsQuery.data ? (
            <EventLog events={eventsQuery.data.items} />
          ) : (
            <p className="text-sm text-text-muted">Loading events…</p>
          )
        ) : (
          <p data-testid="no-current-version" className="text-sm text-text-muted">
            No current version yet.
          </p>
        )}
      </div>
    </section>
  );
}
