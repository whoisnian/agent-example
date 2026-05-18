import { useEffect } from "react";
import { subscribe, type Handler } from "@/services/ws";

/**
 * Bind a topic subscription to a React component lifecycle. The handler
 * MUST be stable across renders (use useCallback) — re-subscribing every
 * render would churn the server-side subscription needlessly.
 */
export function useRealtime(topic: string | null, handler: Handler): void {
  useEffect(() => {
    if (!topic) return;
    const off = subscribe(topic, handler);
    return off;
  }, [topic, handler]);
}
