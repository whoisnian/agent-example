/** React Query keys + read hooks for the task pages. */
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { ApiError } from "@/services/http";
import {
  getTask,
  getVersion,
  listTasks,
  listVersionEvents,
  listVersions,
  type ListTasksParams,
} from "./api";
import type { EventPage, TaskDetail, TaskListPage, VersionDetail, VersionTree } from "./types";

/** Optional refetch interval; pass the function form so connection-state /
 *  active-status changes are re-read each tick (see use-task-live). */
type RefetchInterval = number | false | (() => number | false);

export const taskKeys = {
  all: ["tasks"] as const,
  lists: ["tasks", "list"] as const,
  list: (params: ListTasksParams) => ["tasks", "list", params] as const,
  detail: (id: string) => ["task", id] as const,
  versions: (taskId: string) => ["versions", taskId] as const,
  events: (versionId: string) => ["events", versionId] as const,
};

export function useTasksQuery(
  params: ListTasksParams,
  opts?: { silent?: boolean },
): UseQueryResult<TaskListPage, ApiError> {
  const silent = opts?.silent === true;
  return useQuery({
    queryKey: taskKeys.list(params),
    queryFn: ({ signal }) => listTasks(params, signal, silent),
    // Silent consumers (nav Recents) suppress the query-cache toast as well;
    // their inline placeholder is the sole error surface.
    ...(silent ? { meta: { silent: true } } : {}),
  });
}

export function useTaskQuery(
  id: string,
  refetchInterval?: RefetchInterval,
): UseQueryResult<TaskDetail, ApiError> {
  return useQuery({
    queryKey: taskKeys.detail(id),
    queryFn: ({ signal }) => getTask(id, signal),
    // A 404 (task_not_found) is a render state, not an error to retry/toast.
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 404) && failureCount < 2,
    meta: { silent: true },
    ...(refetchInterval !== undefined ? { refetchInterval } : {}),
  });
}

export function useVersionsQuery(
  taskId: string,
  refetchInterval?: RefetchInterval,
): UseQueryResult<VersionTree, ApiError> {
  return useQuery({
    queryKey: taskKeys.versions(taskId),
    queryFn: ({ signal }) => listVersions(taskId, signal),
    ...(refetchInterval !== undefined ? { refetchInterval } : {}),
  });
}

export function useVersionQuery(versionId: string): UseQueryResult<VersionDetail, ApiError> {
  return useQuery({
    queryKey: ["version", versionId],
    queryFn: ({ signal }) => getVersion(versionId, signal),
    enabled: !!versionId,
    // Quiet read: a conversation turn degrades inline (no prompt text) rather
    // than toasting; 404 is a render state, not an error to retry.
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 404) && failureCount < 2,
    meta: { silent: true },
  });
}

const EVENTS_LIMIT = 200;

export function useVersionEventsQuery(
  versionId: string | null,
  refetchInterval?: RefetchInterval,
): UseQueryResult<EventPage, ApiError> {
  return useQuery({
    queryKey: taskKeys.events(versionId ?? "none"),
    queryFn: ({ signal }) => listVersionEvents(versionId as string, 0, EVENTS_LIMIT, signal),
    enabled: !!versionId,
    ...(refetchInterval !== undefined ? { refetchInterval } : {}),
  });
}
