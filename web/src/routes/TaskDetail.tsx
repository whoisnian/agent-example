import type { JSX } from "react";
import { useState } from "react";
import { useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/primitives/Button";
import { StatusBadge } from "@/components/tasks/StatusBadge";
import { CostBadge } from "@/components/tasks/CostBadge";
import { VersionTree } from "@/components/tasks/VersionTree";
import { EventLog } from "@/components/tasks/EventLog";
import { ApiError } from "@/services/http";
import { useUiStore } from "@/features/ui/store";
import { useTaskQuery, useVersionsQuery, useVersionEventsQuery } from "@/features/tasks/queries";
import { useIterateTaskMutation } from "@/features/tasks/mutations";
import { isActiveStatus, type ActiveVersionConflict } from "@/features/tasks/types";
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

  useTaskLive(id, currentVersionId, queryClient);

  const iterate = useIterateTaskMutation();
  const [showIterate, setShowIterate] = useState(false);
  const [iteratePrompt, setIteratePrompt] = useState("");

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
      </div>

      <div className="mb-6">
        <h2 className="mb-2 text-lg font-medium text-text">Versions</h2>
        {versionsQuery.data ? (
          <VersionTree
            versions={versionsQuery.data.items}
            currentVersionId={currentVersionId}
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
