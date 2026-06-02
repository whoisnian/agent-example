/** Create / iterate mutations. Both opt out of the global error toast
 *  (`meta.silent`) so the page handles invalid_input / 409 inline. */
import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import { controlTask, createTask, iterateTask } from "./api";
import { taskKeys } from "./queries";
import type {
  ControlRequest,
  ControlResponse,
  CreateTaskRequest,
  CreateTaskResponse,
  IterateTaskRequest,
  IterateTaskResponse,
} from "./types";

export function useCreateTaskMutation(): UseMutationResult<
  CreateTaskResponse,
  ApiError,
  CreateTaskRequest
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateTaskRequest) => createTask(body),
    meta: { silent: true },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: taskKeys.all });
    },
  });
}

export interface IterateVars {
  taskId: string;
  body: IterateTaskRequest;
}

export function useIterateTaskMutation(): UseMutationResult<
  IterateTaskResponse,
  ApiError,
  IterateVars
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ taskId, body }: IterateVars) => iterateTask(taskId, body),
    meta: { silent: true },
    onSettled: (_data, _err, { taskId }) => {
      // Refetch task + versions whether we succeeded or hit a 409 race.
      void qc.invalidateQueries({ queryKey: taskKeys.detail(taskId) });
      void qc.invalidateQueries({ queryKey: taskKeys.versions(taskId) });
    },
  });
}

export interface ControlVars {
  taskId: string;
  body: ControlRequest;
}

export function useControlTaskMutation(): UseMutationResult<
  ControlResponse,
  ApiError,
  ControlVars
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ taskId, body }: ControlVars) => controlTask(taskId, body),
    // Silent so the page owns all control UX (confirmation / 409 / best_effort).
    // Paired with controlTask's toastOnError:false (transport layer).
    meta: { silent: true },
    onSettled: (_data, _err, { taskId }) => {
      // Status flips asynchronously (worker → event-ingest → live stream); the
      // 202 is not a state change. Invalidate so the bar re-reads the new status
      // even if the live frame is delayed/dropped.
      void qc.invalidateQueries({ queryKey: taskKeys.detail(taskId) });
      void qc.invalidateQueries({ queryKey: taskKeys.versions(taskId) });
    },
  });
}
