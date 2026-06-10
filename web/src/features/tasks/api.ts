/** Thin `apiFetch` wrappers for the task read+write endpoints. */
import { apiFetch } from "@/services/http";
import type {
  ControlRequest,
  ControlResponse,
  CreateTaskRequest,
  CreateTaskResponse,
  EventPage,
  IterateTaskRequest,
  IterateTaskResponse,
  RollbackBranchResponse,
  RollbackSwitchResponse,
  RollbackTaskRequest,
  TaskDetail,
  TaskListPage,
  VersionDetail,
  VersionTree,
} from "./types";

export interface ListTasksParams {
  page: number;
  pageSize: number;
  status?: string | undefined;
}

export function listTasks(
  { page, pageSize, status }: ListTasksParams,
  signal?: AbortSignal,
  silent?: boolean,
): Promise<TaskListPage> {
  const q = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (status) q.set("status", status);
  return apiFetch<TaskListPage>(`/api/v1/tasks?${q.toString()}`, {
    ...(signal ? { signal } : {}),
    // Quiet consumers (the nav Recents list) suppress the transport toast and
    // own their error surface inline; the page-level list keeps the default.
    ...(silent ? { toastOnError: false } : {}),
  });
}

export function getTask(id: string, signal?: AbortSignal): Promise<TaskDetail> {
  return apiFetch<TaskDetail>(`/api/v1/tasks/${id}`, signal ? { signal } : {});
}

export function listVersions(taskId: string, signal?: AbortSignal): Promise<VersionTree> {
  return apiFetch<VersionTree>(`/api/v1/tasks/${taskId}/versions`, signal ? { signal } : {});
}

export function getVersion(versionId: string, signal?: AbortSignal): Promise<VersionDetail> {
  return apiFetch<VersionDetail>(`/api/v1/versions/${versionId}`, {
    ...(signal ? { signal } : {}),
    // Conversation turns own the (silent) degrade path; no transport toast.
    toastOnError: false,
  });
}

export function listVersionEvents(
  versionId: string,
  afterId: number,
  limit: number,
  signal?: AbortSignal,
): Promise<EventPage> {
  const q = new URLSearchParams({ after_id: String(afterId), limit: String(limit) });
  return apiFetch<EventPage>(
    `/api/v1/versions/${versionId}/events?${q.toString()}`,
    signal ? { signal } : {},
  );
}

export function createTask(body: CreateTaskRequest): Promise<CreateTaskResponse> {
  return apiFetch<CreateTaskResponse>(`/api/v1/tasks`, {
    method: "POST",
    body: JSON.stringify(body),
    // invalid_input is shown inline on the form, not via the global toast.
    toastOnError: false,
  });
}

export function iterateTask(
  taskId: string,
  body: IterateTaskRequest,
): Promise<IterateTaskResponse> {
  return apiFetch<IterateTaskResponse>(`/api/v1/tasks/${taskId}/iterate`, {
    method: "POST",
    body: JSON.stringify(body),
    // 409 conflict is surfaced in-page (toast naming the active version).
    toastOnError: false,
  });
}

export function rollbackTask(
  taskId: string,
  body: RollbackTaskRequest,
): Promise<RollbackBranchResponse | RollbackSwitchResponse> {
  return apiFetch<RollbackBranchResponse | RollbackSwitchResponse>(
    `/api/v1/tasks/${taskId}/rollback`,
    {
      method: "POST",
      body: JSON.stringify(body),
      // 409 active_version_exists / invalid_state are surfaced in-page.
      toastOnError: false,
    },
  );
}

export function controlTask(taskId: string, body: ControlRequest): Promise<ControlResponse> {
  return apiFetch<ControlResponse>(`/api/v1/tasks/${taskId}/control`, {
    method: "POST",
    body: JSON.stringify(body),
    // 409 invalid_state / best_effort are surfaced in-page; suppress the
    // transport toast (the mutation also sets meta.silent for the cache layer).
    toastOnError: false,
  });
}
