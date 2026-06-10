import type { JSX } from "react";
import { Bot } from "lucide-react";
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

function isRecord(x: unknown): x is Record<string, unknown> {
  return typeof x === "object" && x !== null;
}

/** One readable line per event; status / error get a human-readable form. */
function EventLine({ event }: { event: EventItem }): JSX.Element {
  if (event.kind === "status") {
    const status = isRecord(event.payload) ? String(event.payload["status"] ?? "") : "";
    return (
      <li data-testid="event-row" className="flex gap-2 text-sm text-foreground">
        <span className="shrink-0 text-muted-foreground">Status →</span>
        <span>{status || preview(event.payload)}</span>
      </li>
    );
  }
  if (event.kind === "error") {
    const code = isRecord(event.payload) ? String(event.payload["code"] ?? "error") : "error";
    const message = isRecord(event.payload) ? String(event.payload["message"] ?? "") : "";
    return (
      <li data-testid="event-row" className="flex gap-2 text-sm text-destructive">
        <span className="shrink-0 font-medium">{code}</span>
        <span className="break-words">{message || preview(event.payload)}</span>
      </li>
    );
  }
  return (
    <li data-testid="event-row" className="flex gap-2 text-sm">
      <span className="shrink-0 text-primary">{event.kind}</span>
      <span className="truncate text-muted-foreground">{preview(event.payload)}</span>
    </li>
  );
}

export interface EventLogProps {
  events: EventItem[];
}

/**
 * The current turn's execution stream as an assistant message: a left-aligned
 * bubble (assistant position, mirroring the right-aligned user prompt) with
 * one readable line per event instead of the former raw monospace log.
 */
export function EventLog({ events }: EventLogProps): JSX.Element {
  if (events.length === 0) {
    return (
      <p data-testid="event-log-empty" className="text-sm text-muted-foreground">
        No events yet.
      </p>
    );
  }
  return (
    <div className="flex max-w-[85%] items-start gap-2 self-start">
      <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Bot className="size-4" aria-hidden />
      </div>
      <ul
        data-testid="event-log"
        className="flex min-w-0 flex-1 flex-col gap-1.5 rounded-lg bg-muted px-3 py-2"
      >
        {events.map((e) => (
          <EventLine key={e.id} event={e} />
        ))}
      </ul>
    </div>
  );
}
