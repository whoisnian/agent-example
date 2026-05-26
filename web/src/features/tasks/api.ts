/** Thin `apiFetch` wrappers for the task read+write endpoints. */
import { apiFetch } from "@/services/http";
import type {
  CreateTaskRequest,
  CreateTaskResponse,
  EventPage,
  IterateTaskRequest,
  IterateTaskResponse,
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
): Promise<TaskListPage> {
  const q = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (status) q.set("status", status);
  return apiFetch<TaskListPage>(`/api/v1/tasks?${q.toString()}`, signal ? { signal } : {});
}

export function getTask(id: string, signal?: AbortSignal): Promise<TaskDetail> {
  return apiFetch<TaskDetail>(`/api/v1/tasks/${id}`, signal ? { signal } : {});
}

export function listVersions(taskId: string, signal?: AbortSignal): Promise<VersionTree> {
  return apiFetch<VersionTree>(`/api/v1/tasks/${taskId}/versions`, signal ? { signal } : {});
}

export function getVersion(versionId: string, signal?: AbortSignal): Promise<VersionDetail> {
  return apiFetch<VersionDetail>(`/api/v1/versions/${versionId}`, signal ? { signal } : {});
}

export function listVersionEvents(
  versionId: string,
  afterId: number,
  limit: number,
  signal?: AbortSignal,
): Promise<EventPage> {
  const q = new URLSearchParams({ after_id: String(afterId), limit: String(limit) });
  return apiFetch<EventPage>(`/api/v1/versions/${versionId}/events?${q.toString()}`, signal ? { signal } : {});
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
