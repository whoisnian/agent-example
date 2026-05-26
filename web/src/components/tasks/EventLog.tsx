import type { JSX } from "react";
import type { EventItem } from "@/features/tasks/types";

function preview(payload: unknown): string {
  try {
    const s = JSON.stringify(payload);
    if (!s) return "";
    return s.length > 200 ? `${s.slice(0, 200)}…` : s;
  } catch {
    return "";
  }
}

export interface EventLogProps {
  events: EventItem[];
}

export function EventLog({ events }: EventLogProps): JSX.Element {
  if (events.length === 0) {
    return (
      <p data-testid="event-log-empty" className="text-sm text-text-muted">
        No events yet.
      </p>
    );
  }
  return (
    <ul data-testid="event-log" className="flex flex-col gap-1 font-mono text-xs">
      {events.map((e) => (
        <li key={e.id} data-testid="event-row" className="flex gap-2">
          <span className="text-text-muted">#{e.seq}</span>
          <span className="text-accent">{e.kind}</span>
          <span className="truncate text-text-muted">{preview(e.payload)}</span>
        </li>
      ))}
    </ul>
  );
}
