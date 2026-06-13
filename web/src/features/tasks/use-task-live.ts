/**
 * Live updates for TaskDetail: WS-first (subscribe + invalidate) with a
 * React Query polling fallback until the Realtime Gateway server exists.
 *
 * See design D2. The WS client (`services/ws.ts`) already handles lazy connect,
 * reconnect, seq dedupe, and gap detection — this module wires its frames to
 * cache invalidation and registers a single id-based gap-fill handler.
 */
import { useCallback } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { RealtimeEvent } from "@/types/envelope";
import { useRealtime } from "@/hooks/use-realtime";
import { getRealtimeClient, setRealtimeOnGap } from "@/services/ws";
import { artifactKeys } from "@/features/artifacts/queries";
import { listVersionEvents } from "./api";
import { taskKeys } from "./queries";
import type { EventPage } from "./types";

const POLL_INTERVAL_MS = 3_000;
const EVENTS_LIMIT = 200;

/**
 * Subscribe to `task:<id>` (always) and `version:<currentVersionId>` (when
 * present), invalidating the matching React Query caches on each frame. The
 * handler is stable across renders (deps are the two id strings only) so it
 * doesn't churn the server-side subscription.
 */
export function useTaskLive(
  taskId: string,
  currentVersionId: string | null,
  queryClient: QueryClient,
): void {
  const handler = useCallback(
    (event: RealtimeEvent): void => {
      if (event.topic.startsWith("task:")) {
        void queryClient.invalidateQueries({ queryKey: taskKeys.detail(taskId) });
        void queryClient.invalidateQueries({ queryKey: taskKeys.versions(taskId) });
        // Status frames are low-frequency; refresh the list prefix so the
        // TaskList page and nav Recents reflect the running task's status.
        void queryClient.invalidateQueries({ queryKey: taskKeys.lists });
      } else if (event.topic.startsWith("version:")) {
        // The version topic id may be a non-current version (a historical turn
        // streaming late). Invalidate by the FRAME's version id, not just the
        // current one, so each turn's caches refresh correctly.
        const versionId = event.topic.slice("version:".length);
        if (versionId) {
          void queryClient.invalidateQueries({ queryKey: taskKeys.events(versionId) });
          // An artifact frame (file produced) or a status frame (run flipped,
          // e.g. terminal) means the version's artifact set changed — refresh
          // it so the turn's card appears/updates live, no manual refresh.
          if (event.kind === "artifact" || event.kind === "status") {
            void queryClient.invalidateQueries({ queryKey: artifactKeys.byVersion(versionId) });
          }
        }
        void queryClient.invalidateQueries({ queryKey: taskKeys.versions(taskId) });
      }
    },
    // currentVersionId is NOT read inside the handler (it keys on the frame's
    // own version id); it only drives the subscription topic below.
    [taskId, queryClient],
  );

  useRealtime(`task:${taskId}`, handler);
  useRealtime(currentVersionId ? `version:${currentVersionId}` : null, handler);
}

/**
 * Function-form `refetchInterval`: re-read each tick so a `reconnecting → open`
 * WS transition silences polling within one interval. Polls only while the
 * task is active AND no WS connection is open. Pass the result to the read
 * hooks; `isActive` flips the component re-render when task.status changes.
 */
export function liveRefetchInterval(isActive: boolean): () => number | false {
  return () =>
    isActive && getRealtimeClient().getConnectionState() !== "open" ? POLL_INTERVAL_MS : false;
}

/**
 * Register the realtime gap-fill handler ONCE at app bootstrap (next to
 * setRealtimeNavigator in main.tsx). Keyed by `version:<id>` topic — task
 * topics carry no task_events ids. Backfills by the GLOBAL event `id` cursor
 * (the events endpoint's `after_id`), NOT the per-run `seq` the onGap args
 * carry. Reads the highest id already in cache and fetches forward.
 */
export function installRealtimeGapFill(queryClient: QueryClient): void {
  setRealtimeOnGap(async (topic): Promise<void> => {
    if (!topic.startsWith("version:")) return;
    const versionId = topic.slice("version:".length);
    if (!versionId) return;

    const cached = queryClient.getQueryData<EventPage>(taskKeys.events(versionId));
    const afterId =
      cached && cached.items.length > 0
        ? cached.items.reduce((max, e) => (e.id > max ? e.id : max), 0)
        : (cached?.next_after_id ?? 0);

    const page = await listVersionEvents(versionId, afterId, EVENTS_LIMIT);
    if (page.items.length === 0) return;

    queryClient.setQueryData<EventPage>(taskKeys.events(versionId), (prev) => {
      if (!prev) return page;
      const seen = new Set(prev.items.map((e) => e.id));
      const merged = [...prev.items, ...page.items.filter((e) => !seen.has(e.id))];
      merged.sort((a, b) => a.id - b.id);
      return { items: merged, next_after_id: page.next_after_id };
    });
  });
}
