import type { JSX } from "react";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { ApiError } from "@/services/http";
import { useUiStore } from "@/features/ui/store";
import { useDeleteTaskMutation } from "@/features/tasks/mutations";
import { isActiveStatus, isConflictData } from "@/features/tasks/types";

interface DeleteTaskButtonProps {
  taskId: string;
  taskTitle: string;
  status: string;
  /** Called after a successful (or already-deleted) delete — TaskDetail uses
   *  this to navigate back to the list. */
  onDeleted?: () => void;
  /** When true, render a labeled button (TaskDetail header); otherwise an
   *  icon-only button (TaskList row). */
  withLabel?: boolean;
}

export function DeleteTaskButton({
  taskId,
  taskTitle,
  status,
  onDeleted,
  withLabel = false,
}: DeleteTaskButtonProps): JSX.Element {
  const pushToast = useUiStore((s) => s.pushToast);
  const del = useDeleteTaskMutation();
  const active = isActiveStatus(status);
  const busyReason = active
    ? "Task is busy — cancel the active version before deleting"
    : undefined;

  const onConfirm = (): void => {
    del.mutate(taskId, {
      onSuccess: () => {
        pushToast({ level: "success", message: "Task deleted" });
        onDeleted?.();
      },
      onError: (err) => {
        if (err instanceof ApiError && err.code === "active_version_exists") {
          const d = err.data;
          const reason = isConflictData(d)
            ? `active version ${d.active_version_id} is ${d.active_version_status}`
            : "an active version is running";
          pushToast({ level: "warning", message: `Cannot delete: ${reason} — cancel it first` });
        } else if (err instanceof ApiError && err.status === 404) {
          // Already gone — treat as success (idempotent), refresh and leave.
          pushToast({ level: "info", message: "Task already deleted" });
          onDeleted?.();
        } else if (err instanceof ApiError) {
          pushToast({ level: "error", message: err.message });
        }
      },
    });
  };

  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="outline"
          size={withLabel ? "sm" : "icon"}
          disabled={active}
          title={busyReason}
          data-testid="task-delete"
          aria-label="Delete task"
        >
          <Trash2 aria-hidden />
          {withLabel ? <span>Delete</span> : null}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent data-testid="task-delete-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>Delete task?</AlertDialogTitle>
          <AlertDialogDescription>
            “{taskTitle}” will be removed from your task list. This hides the task; its run history
            is retained. This action cannot be undone from the UI.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel data-testid="task-delete-cancel">Cancel</AlertDialogCancel>
          <AlertDialogAction
            data-testid="task-delete-confirm"
            disabled={del.isPending}
            onClick={onConfirm}
          >
            {del.isPending ? "Deleting…" : "Delete"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
