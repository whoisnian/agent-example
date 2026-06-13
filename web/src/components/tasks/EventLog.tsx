import type { JSX } from "react";
import { Bot, Check, RotateCw } from "lucide-react";
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

/**
 * Kinds that render nothing in the log. `artifact` is intentionally hidden:
 * produced files surface ONLY via the per-turn aggregate artifact card (and
 * only once the version completes — see ConversationTurn), so a mid-run file
 * line never creates ambiguity about what the final products are. `title`
 * already lives in the page header.
 */
const HIDDEN_KINDS = new Set(["title", "artifact"]);

function planSteps(event: EventItem): unknown[] | null {
  const p = event.payload;
  if (event.kind !== "plan" || !isRecord(p) || !Array.isArray(p["steps"])) return null;
  const steps = p["steps"] as unknown[];
  return steps.length > 0 ? steps : null;
}

/** The plan as its own card — an ordered step list, separate from the step
 *  progress and the final summary (no longer crammed into one bubble). */
function PlanCard({ steps }: { steps: unknown[] }): JSX.Element {
  return (
    <div data-testid="event-plan" className="rounded-lg bg-muted px-3 py-2 text-sm text-foreground">
      <span className="text-xs font-medium text-muted-foreground">Plan</span>
      {/* Render the ordinal in a fixed column (not a CSS list marker): a real
          `list-decimal` marker is positioned `outside` and detaches far to the
          left of CJK text. A flex row keeps "N." tight to its text and wraps
          long lines under the text (hanging indent). */}
      <ol className="mt-1 flex flex-col gap-0.5">
        {steps.map((s, i) => (
          <li key={i} className="flex gap-2">
            <span className="shrink-0 tabular-nums text-muted-foreground">{i + 1}.</span>
            <span className="min-w-0 break-words">
              {typeof s === "string" ? s : String(isRecord(s) ? (s["title"] ?? "") : s)}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

/** One row inside the process card: step progress, status/log activity,
 *  errors, or a compact fallback for an unknown / malformed-payload kind. */
function ProcessRow({ event }: { event: EventItem }): JSX.Element {
  const p = event.payload;
  switch (event.kind) {
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
        <li
          data-testid="event-row"
          data-kind="log"
          className="font-mono text-xs text-muted-foreground"
        >
          {message}
        </li>
      );
    }
    case "error": {
      const code = str(p, "code") || "error";
      const message = str(p, "message");
      return (
        <li
          data-testid="event-row"
          data-kind="error"
          className="flex gap-2 text-sm text-destructive"
        >
          <span className="shrink-0 font-medium">{code}</span>
          <span className="break-words">{message || preview(p)}</span>
        </li>
      );
    }
    default:
      // Unknown kind, or a recognized kind with a malformed payload (e.g. a
      // plan with no steps) that didn't qualify for its own card.
      return (
        <li data-testid="event-row" data-kind="fallback" className="flex gap-2 text-sm">
          <span className="shrink-0 text-primary">{event.kind}</span>
          <span className="truncate text-muted-foreground">{preview(p)}</span>
        </li>
      );
  }
}

/** The run summary as the assistant's answer — its own bordered card, visually
 *  distinct from the muted plan/process cards. */
function SummaryCard({ text }: { text: string }): JSX.Element {
  return (
    <div
      data-testid="event-summary"
      className="rounded-lg border border-border bg-card px-3 py-2 text-sm text-foreground"
    >
      <p className="whitespace-pre-wrap break-words">{text}</p>
    </div>
  );
}

export interface EventLogProps {
  events: EventItem[];
  /** True when the events list was capped at the page limit (older events not
   *  shown). Renders a non-blocking truncation hint. */
  truncated?: boolean;
}

/**
 * The turn's execution stream as the assistant message, split into distinct
 * blocks rather than one mixed bubble: a Plan card, a Process card (step
 * progress + status/log/error activity), and a Summary card (the final
 * answer). Artifact events are not rendered here — products live in the
 * per-turn aggregate card. The whole group sits at the assistant position
 * (left-aligned), mirroring the right-aligned user prompt.
 */
export function EventLog({ events, truncated = false }: EventLogProps): JSX.Element {
  if (events.length === 0) {
    return (
      <p data-testid="event-log-empty" className="text-sm text-muted-foreground">
        No events yet.
      </p>
    );
  }

  const planEvent = events.find((e) => planSteps(e) !== null);
  // The run emits at most one summary; render the last non-empty one as the
  // answer card. Empty summaries (no text) render nothing.
  const summaryEvent = [...events]
    .reverse()
    .find((e) => e.kind === "summary" && str(e.payload, "summary"));
  // Everything else recognized/unknown (steps, status, log, error, a malformed
  // plan, unknown kinds) becomes a process row — never the dedicated cards.
  const processEvents = events.filter(
    (e) => !HIDDEN_KINDS.has(e.kind) && e !== planEvent && e.kind !== "summary",
  );

  return (
    <div data-testid="event-log" className="flex max-w-[85%] items-start gap-2 self-start">
      <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Bot className="size-4" aria-hidden />
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {truncated ? (
          <p data-testid="event-log-truncated" className="text-xs italic text-muted-foreground">
            Showing the latest events; earlier ones are not shown.
          </p>
        ) : null}
        {planEvent ? <PlanCard steps={planSteps(planEvent) as unknown[]} /> : null}
        {processEvents.length > 0 ? (
          <ul
            data-testid="event-process"
            className="flex flex-col gap-1.5 rounded-lg bg-muted px-3 py-2"
          >
            {processEvents.map((e) => (
              <ProcessRow key={e.id} event={e} />
            ))}
          </ul>
        ) : null}
        {summaryEvent ? <SummaryCard text={str(summaryEvent.payload, "summary")} /> : null}
      </div>
    </div>
  );
}
