import type { JSX, ReactNode } from "react";
import { FileArchive, FileText } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { useUiStore } from "@/features/ui/store";
import { useVersionQuery, useVersionEventsQuery, EVENTS_PAGE_LIMIT } from "@/features/tasks/queries";
import { isActiveStatus, type RollbackMode, type VersionNode } from "@/features/tasks/types";
import { formatBytes } from "@/features/artifacts/format";
import {
  useArchivePresignMutation,
  useVersionArtifactsQuery,
} from "@/features/artifacts/queries";
import type { ArtifactMeta } from "@/features/artifacts/types";
import { StatusBadge } from "./StatusBadge";
import { CostBadge } from "./CostBadge";
import { EventLog } from "./EventLog";
import { RollbackControl } from "./RollbackControl";

const BUSY_REASON = "Task is busy — wait for the active version to finish";

export interface ConversationTurnProps {
  version: VersionNode;
  /** Parent `version_no` when this turn forks from a non-preceding version
   *  (rollback-branch); undefined for linear history. */
  originNo?: number | undefined;
  isCurrent: boolean;
  /** Task-level mutex: disables ALL rollback actions while active. */
  taskActive: boolean;
  onRollback?: ((versionId: string, mode: RollbackMode, prompt?: string) => void) | undefined;
  rollbackPending?: boolean;
  /** The CURRENT turn's live execution log, supplied by TaskDetail (it owns the
   *  live/polling events query). Historical turns render their own lazily. */
  children?: ReactNode;
}

/**
 * One conversation turn = one version, rendered in conversation order: the
 * version's prompt (user message) → result line → the execution section
 * (assistant reply; live for the current turn, collapsed+lazy for historical
 * turns) → the version's aggregate artifact card → a rollback footer on
 * non-current turns. Quiet by design: prompt and artifact reads degrade inline,
 * never toast.
 */
export function ConversationTurn({
  version,
  originNo,
  isCurrent,
  taskActive,
  onRollback,
  rollbackPending = false,
  children,
}: ConversationTurnProps): JSX.Element {
  return (
    <li
      data-testid="conversation-turn"
      data-version-id={version.id}
      data-current={isCurrent}
      className="flex flex-col gap-2"
    >
      <TurnPrompt versionId={version.id} />

      {/* Result line */}
      <div className="flex items-center gap-2 text-sm">
        <span className="font-mono text-foreground">v{version.version_no}</span>
        <StatusBadge status={version.status} />
        <CostBadge cost={version.cost} />
        {isCurrent ? (
          <span data-testid="current-marker" className="text-xs text-primary">
            current
          </span>
        ) : null}
        {originNo !== undefined ? (
          <span data-testid="turn-origin" className="text-xs text-muted-foreground">
            from v{originNo}
          </span>
        ) : null}
      </div>

      {/* Execution section: the current turn's live log (passed by TaskDetail),
          or an inline log for a historical turn. */}
      {isCurrent ? children : <HistoricalExecution versionId={version.id} />}

      <TurnArtifacts version={version} />

      {onRollback && !isCurrent ? (
        <RollbackControl
          branchDisabled={taskActive}
          branchReason={taskActive ? BUSY_REASON : undefined}
          switchDisabled={taskActive || version.is_active}
          switchReason={
            taskActive
              ? BUSY_REASON
              : version.is_active
                ? "Can only switch to a finished version"
                : undefined
          }
          pending={rollbackPending}
          onRollback={(mode, prompt) => onRollback(version.id, mode, prompt)}
        />
      ) : null}
    </li>
  );
}

/**
 * The version's prompt as the user-message position. Lazily fetched via the
 * (silent) version detail read; a failure degrades to no prompt text — the
 * result line still identifies the turn by version number.
 */
function TurnPrompt({ versionId }: { versionId: string }): JSX.Element | null {
  const query = useVersionQuery(versionId);

  if (query.isPending) {
    return <Skeleton className="h-8 w-2/3 self-end" />;
  }
  const prompt = query.data?.version.prompt;
  if (!prompt) return null;

  return (
    <div
      data-testid="turn-prompt"
      className="max-w-[85%] self-end whitespace-pre-wrap break-words rounded-lg bg-muted px-3 py-2 text-sm text-foreground"
    >
      {prompt}
    </div>
  );
}

/**
 * A historical (non-current) turn's execution section, rendered inline and
 * expanded — a prior version's conversation stays visible after iterating,
 * like a chat history (no collapse, no truncated summary line). Each turn reads
 * its own version's events lazily (React Query caches; terminal versions are
 * static, so no polling). The summary surfaces inside the log itself as the
 * assistant reply (the `summary` event), so no separate summary line is needed.
 */
function HistoricalExecution({ versionId }: { versionId: string }): JSX.Element {
  const events = useVersionEventsQuery(versionId);

  if (events.isPending) {
    return <Skeleton data-testid="execution-events-loading" className="h-6 w-1/2 self-start" />;
  }
  if (!events.data) {
    return (
      <p
        data-testid="execution-events-error"
        className="self-start text-xs text-muted-foreground"
      >
        Execution log unavailable.
      </p>
    );
  }
  return (
    <EventLog
      events={events.data.items}
      truncated={events.data.items.length >= EVENTS_PAGE_LIMIT}
    />
  );
}

/** Total of the non-null byte sizes in a version's artifacts. */
function totalBytes(artifacts: ArtifactMeta[]): number {
  return artifacts.reduce((sum, a) => sum + (a.bytes ?? 0), 0);
}

/** A short, comma-joined preview of the first few artifact paths (kind fallback). */
function pathSummary(artifacts: ArtifactMeta[]): string {
  const labels = artifacts.map((a) => a.path ?? a.kind);
  const shown = labels.slice(0, 3).join(", ");
  return labels.length > 3 ? `${shown} +${labels.length - 3} more` : shown;
}

/**
 * The turn's produced artifacts as a SINGLE aggregate card below the execution
 * section — rendered ONLY once the version reaches a terminal status. While the
 * version is still active the card is withheld, because a mid-run product set
 * is still changing and showing it is ambiguous (the live updates still arrive
 * via the realtime status frame, so the card appears at completion with no
 * manual refresh). Empty list → omitted (no conversation noise). Activating the
 * card drives the right preview panel to this version (selecting the first
 * artifact); Download zip re-mints a version-archive URL per click.
 */
function TurnArtifacts({ version }: { version: VersionNode }): JSX.Element | null {
  const versionId = version.id;
  const query = useVersionArtifactsQuery(versionId);
  const archive = useArchivePresignMutation();
  const selectArtifact = useUiStore((s) => s.selectArtifact);
  const pushToast = useUiStore((s) => s.pushToast);

  const onDownloadZip = (): void => {
    archive.mutate(versionId, {
      onSuccess: ({ url }) => window.location.assign(url),
      onError: (err) =>
        pushToast({ level: "error", message: `Download failed: ${err.message}` }),
    });
  };

  // Products are shown only after the version finishes (succeeded / failed /
  // cancelled) — never while it is still producing them.
  if (isActiveStatus(version.status)) return null;

  if (query.isPending) {
    return <Skeleton data-testid="turn-artifacts-loading" className="h-6 w-1/2 self-start" />;
  }
  if (query.error || !query.data) {
    return (
      <p data-testid="turn-artifacts-error" className="text-xs text-muted-foreground">
        Artifacts unavailable for this version.
      </p>
    );
  }
  const artifacts = query.data.artifacts;
  if (artifacts.length === 0) return null;

  const first = artifacts[0]!;
  const total = totalBytes(artifacts);

  return (
    <div
      data-testid="turn-artifact-card"
      data-version-id={versionId}
      className="flex max-w-[85%] items-center gap-3 self-start rounded-lg border border-border bg-card p-3 transition-colors hover:border-ring"
    >
      {/* The card body (minus Download) opens the version's files in the panel. */}
      <button
        type="button"
        data-testid="turn-artifact-open"
        onClick={() => selectArtifact(versionId, first.id)}
        className="flex min-w-0 flex-1 items-center gap-3 text-left"
      >
        <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <FileArchive className="size-4" aria-hidden />
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="text-sm font-medium text-foreground">
            {artifacts.length} file{artifacts.length === 1 ? "" : "s"}
            {total > 0 ? ` · ${formatBytes(total)}` : ""}
          </span>
          <span className="truncate font-mono text-xs text-muted-foreground">
            {pathSummary(artifacts)}
          </span>
        </span>
      </button>
      <Button
        variant="outline"
        size="sm"
        className="h-8 shrink-0 gap-1.5 px-3"
        data-testid="turn-artifact-download-zip"
        disabled={archive.isPending}
        onClick={onDownloadZip}
      >
        <FileText className="size-3.5" aria-hidden />
        Download zip
      </Button>
    </div>
  );
}
