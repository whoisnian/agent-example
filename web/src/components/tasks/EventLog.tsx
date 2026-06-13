import type { JSX } from "react";
import { Bot, Check, FileText, RotateCw } from "lucide-react";
import { useUiStore } from "@/features/ui/store";
import type { EventItem } from "@/features/tasks/types";

function isRecord(x: unknown): x is Record<string, unknown> {
  return typeof x === "object" && x !== null;
}

function str(payload: unknown, key: string): string {
  return isRecord(payload) ? String(payload[key] ?? "") : "";
}

/** Compact JSON fallback for unrecognized kinds (the ONLY place raw-ish JSON
 *  appears — recognized kinds get conversational rendering). */
function preview(payload: unknown): string {
  try {
    const s = JSON.stringify(payload);
    if (!s) return "";
    return s.length > 200 ? `${s.slice(0, 200)}…` : s;
  } catch {
    return "";
  }
}

/** Kinds that are not conversational content and render no row (the task title
 *  lives in the page header; cost flows on a separate exchange, never here). */
const HIDDEN_KINDS = new Set(["title"]);

/**
 * One event rendered by kind — dialogue substance as body text, process info as
 * de-emphasized lines, never raw JSON for a recognized kind. A recognized kind
 * with a malformed payload degrades to the compact fallback rather than throws.
 */
function EventLine({ event }: { event: EventItem }): JSX.Element | null {
  const selectArtifact = useUiStore((s) => s.selectArtifact);
  const p = event.payload;

  switch (event.kind) {
    case "summary": {
      const summary = str(p, "summary");
      if (!summary) return null;
      return (
        <li data-testid="event-row" data-kind="summary" className="text-sm text-foreground">
          <p className="whitespace-pre-wrap break-words">{summary}</p>
        </li>
      );
    }
    case "plan": {
      const steps = isRecord(p) && Array.isArray(p["steps"]) ? (p["steps"] as unknown[]) : null;
      if (!steps || steps.length === 0) {
        return <FallbackLine event={event} />;
      }
      return (
        <li data-testid="event-row" data-kind="plan" className="text-sm text-foreground">
          <span className="text-xs text-muted-foreground">Plan</span>
          <ol className="ml-4 list-decimal space-y-0.5">
            {steps.map((s, i) => (
              <li key={i} className="break-words">
                {typeof s === "string" ? s : String(isRecord(s) ? (s["title"] ?? "") : s)}
              </li>
            ))}
          </ol>
        </li>
      );
    }
    case "step": {
      const verdict = str(p, "verdict");
      const title = str(p, "title");
      const summary = str(p, "summary");
      const done = verdict === "finish" || verdict === "advance";
      return (
        <li data-testid="event-row" data-kind="step" className="flex gap-2 text-sm">
          <span className="mt-0.5 shrink-0 text-muted-foreground">
            {done ? (
              <Check className="size-3.5 text-primary" aria-hidden />
            ) : (
              <RotateCw className="size-3.5" aria-hidden />
            )}
          </span>
          <span className="min-w-0">
            <span className="text-foreground">{title || verdict || "step"}</span>
            {summary ? (
              <span className="ml-2 break-words text-xs text-muted-foreground">{summary}</span>
            ) : null}
          </span>
        </li>
      );
    }
    case "artifact": {
      const path = str(p, "path");
      const artifactId = str(p, "artifact_id");
      const label = path || "artifact";
      return (
        <li data-testid="event-row" data-kind="artifact">
          <button
            type="button"
            data-testid="event-artifact"
            onClick={() => artifactId && selectArtifact(event.version_id, artifactId)}
            className="flex items-center gap-1.5 text-left text-sm text-foreground hover:underline"
          >
            <FileText className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            <span className="truncate font-mono text-xs">{label}</span>
          </button>
        </li>
      );
    }
    case "status": {
      const status = str(p, "status");
      return (
        <li data-testid="event-row" data-kind="status" className="text-xs text-muted-foreground">
          Status → {status || preview(p)}
        </li>
      );
    }
    case "log": {
      const message = str(p, "message") || preview(p);
      return (
        <li data-testid="event-row" data-kind="log" className="font-mono text-xs text-muted-foreground">
          {message}
        </li>
      );
    }
    case "error": {
      const code = str(p, "code") || "error";
      const message = str(p, "message");
      return (
        <li data-testid="event-row" data-kind="error" className="flex gap-2 text-sm text-destructive">
          <span className="shrink-0 font-medium">{code}</span>
          <span className="break-words">{message || preview(p)}</span>
        </li>
      );
    }
    default:
      if (HIDDEN_KINDS.has(event.kind)) return null;
      return <FallbackLine event={event} />;
  }
}

/** Compact fallback row for unknown kinds / malformed recognized payloads. */
function FallbackLine({ event }: { event: EventItem }): JSX.Element {
  return (
    <li data-testid="event-row" data-kind="fallback" className="flex gap-2 text-sm">
      <span className="shrink-0 text-primary">{event.kind}</span>
      <span className="truncate text-muted-foreground">{preview(event.payload)}</span>
    </li>
  );
}

export interface EventLogProps {
  events: EventItem[];
  /** True when the events list was capped at the page limit (older events not
   *  shown). Renders a non-blocking truncation hint. */
  truncated?: boolean;
}

/**
 * The turn's execution stream as an assistant message: a left-aligned bubble
 * (mirroring the right-aligned user prompt) with one readable line per event,
 * rendered by kind. `artifact` events are de-duplicated by `artifact_id` so a
 * resumed run that re-emits a file shows it once.
 */
export function EventLog({ events, truncated = false }: EventLogProps): JSX.Element {
  if (events.length === 0) {
    return (
      <p data-testid="event-log-empty" className="text-sm text-muted-foreground">
        No events yet.
      </p>
    );
  }

  // De-dupe artifact rows by artifact_id (last occurrence wins), preserving the
  // position of that last occurrence; all other events pass through in order.
  const seenArtifact = new Map<string, number>();
  events.forEach((e, i) => {
    if (e.kind === "artifact") {
      const id = str(e.payload, "artifact_id");
      if (id) seenArtifact.set(id, i);
    }
  });
  const visible = events.filter((e, i) => {
    if (e.kind !== "artifact") return true;
    const id = str(e.payload, "artifact_id");
    return !id || seenArtifact.get(id) === i;
  });

  return (
    <div className="flex max-w-[85%] items-start gap-2 self-start">
      <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Bot className="size-4" aria-hidden />
      </div>
      <ul
        data-testid="event-log"
        className="flex min-w-0 flex-1 flex-col gap-1.5 rounded-lg bg-muted px-3 py-2"
      >
        {truncated ? (
          <li
            data-testid="event-log-truncated"
            className="text-xs italic text-muted-foreground"
          >
            Showing the latest events; earlier ones are not shown.
          </li>
        ) : null}
        {visible.map((e) => (
          <EventLine key={e.id} event={e} />
        ))}
      </ul>
    </div>
  );
}
