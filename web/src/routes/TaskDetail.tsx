import type { JSX, KeyboardEvent as ReactKeyboardEvent } from "react";
import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/tasks/StatusBadge";
import { CostBadge } from "@/components/tasks/CostBadge";
import { ControlBar } from "@/components/tasks/ControlBar";
import { ConversationTurn } from "@/components/tasks/ConversationTurn";
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
  const setSelectedVersionId = useUiStore((s) => s.setSelectedVersionId);

  const taskQuery = useTaskQuery(id);
  const task = taskQuery.data?.task;
  const isActive = task ? isActiveStatus(task.status) : false;
  const currentVersionId = task?.current_version ?? null;

  // Default the right-column Artifact Preview to this task's current version,
  // and clear the selection when leaving the detail page so the preview panel
  // does not show a stale version on the list/cost pages. The version tree can
  // override the selection while the page is mounted.
  useEffect(() => {
    setSelectedVersionId(currentVersionId);
    return () => setSelectedVersionId(null);
  }, [currentVersionId, setSelectedVersionId]);

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
  const [iteratePrompt, setIteratePrompt] = useState("");

  // Follow new turns/events with the scroll only when the user is already near
  // the bottom; never steal the position while they are reading back.
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const versionCount = versionsQuery.data?.items.length ?? 0;
  const eventCount = eventsQuery.data?.items.length ?? 0;
  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [versionCount, eventCount]);

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
        <h1 className="text-2xl font-semibold text-foreground">Task not found</h1>
        <p className="text-sm text-muted-foreground">No task with id {id}.</p>
      </section>
    );
  }

  if (taskQuery.isPending) {
    return (
      <p data-testid="task-detail-loading" className="text-sm text-muted-foreground">
        Loading…
      </p>
    );
  }

  const detail = taskQuery.data;
  if (!detail) {
    return (
      <p data-testid="task-detail-error" className="text-sm text-destructive">
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
          // Clear on success only — a failed submission keeps the typed prompt.
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

  // Chat-style composer keys: Enter sends, Ctrl/Cmd+Enter inserts a newline
  // (inverting the textarea default). An Enter that is confirming an IME
  // composition (e.g. Chinese input) MUST NOT submit — guarded via isComposing.
  const onPromptKeyDown = (e: ReactKeyboardEvent<HTMLTextAreaElement>): void => {
    if (e.key !== "Enter" || e.nativeEvent.isComposing) return;
    if (e.ctrlKey || e.metaKey) {
      // Newline at the caret (the browser inserts nothing for Ctrl+Enter).
      // setRangeText mutates the element value AND advances the caret
      // synchronously; syncing state to el.value keeps the controlled value in
      // step without a caret-restoring rAF (which races typing in tests).
      e.preventDefault();
      const el = e.currentTarget;
      el.setRangeText("\n", el.selectionStart, el.selectionEnd, "end");
      setIteratePrompt(el.value);
      return;
    }
    if (e.shiftKey) return; // Shift+Enter falls through to the default newline.
    // Plain Enter → send, mirroring the button's guards.
    e.preventDefault();
    if (!isActive && !iterate.isPending && iteratePrompt.trim()) {
      submitIterate();
    }
  };

  const versions = versionsQuery.data?.items ?? null;
  const byId = new Map((versions ?? []).map((v) => [v.id, v]));
  const busyReason = isActive
    ? "Task is busy — wait for the active version to finish"
    : undefined;

  return (
    <section data-testid="task-detail-page" className="flex h-full min-h-0 flex-col">
      {/* Compact header: identity row + the full cost breakdown as a slim bar. */}
      <div className="flex shrink-0 flex-col gap-2 border-b border-border pb-3">
        <div className="flex items-center gap-3">
          <h1 className="truncate text-xl font-semibold text-foreground">{loadedTask.title}</h1>
          <StatusBadge status={loadedTask.status} />
          <span className="text-sm text-muted-foreground">{loadedTask.task_type}</span>
          <CostBadge cost={detail.cost} />
          <ControlBar status={loadedTask.status} pending={control.isPending} onAction={onControl} />
        </div>
        <div data-testid="task-cost-panel">
          {costQuery.data ? (
            <TokenBar cost={costQuery.data.total} />
          ) : costQuery.isPending ? (
            <p className="text-sm text-muted-foreground">Loading cost…</p>
          ) : (
            // A 404 here is a defensive no-op (the page is already gated by the
            // task query); render nothing rather than a second not-found screen.
            <p className="text-sm text-muted-foreground">Cost unavailable.</p>
          )}
        </div>
      </div>

      {/* Conversation body: one turn per version, ascending version_no. */}
      <div
        ref={bodyRef}
        data-testid="conversation-body"
        className="scrollbar-themed min-h-0 flex-1 overflow-y-auto py-4"
      >
        {versions ? (
          versions.length === 0 ? (
            <p data-testid="conversation-empty" className="text-sm text-muted-foreground">
              No versions yet.
            </p>
          ) : (
            <ol className="flex flex-col gap-6">
              {versions.map((v) => {
                const parent = v.parent_id ? byId.get(v.parent_id) : undefined;
                // A fork (rollback-branch) is a parent that is NOT the
                // immediately preceding version number.
                const forked = parent && parent.version_no !== v.version_no - 1;
                const isCurrent = v.id === currentVersionId;
                return (
                  <ConversationTurn
                    key={v.id}
                    version={v}
                    originNo={forked ? parent.version_no : undefined}
                    isCurrent={isCurrent}
                    taskActive={isActive}
                    onRollback={onRollback}
                    rollbackPending={rollback.isPending}
                  >
                    {isCurrent ? (
                      eventsQuery.data ? (
                        <EventLog events={eventsQuery.data.items} />
                      ) : (
                        <p className="text-sm text-muted-foreground">Loading events…</p>
                      )
                    ) : null}
                  </ConversationTurn>
                );
              })}
            </ol>
          )
        ) : (
          <p className="text-sm text-muted-foreground">Loading versions…</p>
        )}
        {versions && !currentVersionId ? (
          <p data-testid="no-current-version" className="mt-4 text-sm text-muted-foreground">
            No current version yet.
          </p>
        ) : null}
      </div>

      {/* Persistent iterate composer (replaces the toggle-revealed form). */}
      <div className="flex shrink-0 flex-col gap-2 border-t border-border pt-3">
        <textarea
          data-testid="iterate-prompt"
          value={iteratePrompt}
          onChange={(e) => setIteratePrompt(e.target.value)}
          onKeyDown={onPromptKeyDown}
          rows={3}
          disabled={isActive || iterate.isPending}
          title={busyReason}
          placeholder="Describe the change for the next version…"
          className="resize-none rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground disabled:opacity-50"
        />
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs text-muted-foreground">
            Enter to send · Ctrl+Enter for a new line
          </span>
          <Button
            data-testid="iterate-submit"
            disabled={isActive || iterate.isPending}
            title={busyReason}
            onClick={submitIterate}
          >
            {iterate.isPending ? "Submitting…" : "Iterate"}
          </Button>
        </div>
      </div>
    </section>
  );
}
