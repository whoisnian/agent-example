import type { JSX, ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { useUiStore } from "@/features/ui/store";
import { useVersionQuery } from "@/features/tasks/queries";
import type { RollbackMode, VersionNode } from "@/features/tasks/types";
import { formatBytes } from "@/features/artifacts/format";
import {
  useArtifactPresignMutation,
  useVersionArtifactsQuery,
} from "@/features/artifacts/queries";
import { StatusBadge } from "./StatusBadge";
import { CostBadge } from "./CostBadge";
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
  /** Extra turn content — the current turn's inline event log. */
  children?: ReactNode;
}

/**
 * One conversation turn = one version: the version's prompt as the user
 * message, a result line (version_no / status / cost / current marker), the
 * version's inline artifact list, and a rollback footer on non-current turns.
 * Quiet by design: the prompt and artifact reads degrade inline, never toast.
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

      <TurnArtifacts versionId={version.id} />

      {children}

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
 * Inline artifact list for the turn's version. Empty list → the section is
 * omitted entirely (no empty-state noise in the conversation). Activating an
 * entry drives the right preview panel via the store's atomic pair write;
 * Download re-mints a presigned URL per click and navigates straight to OSS.
 */
function TurnArtifacts({ versionId }: { versionId: string }): JSX.Element | null {
  const query = useVersionArtifactsQuery(versionId);
  const download = useArtifactPresignMutation();
  const selectArtifact = useUiStore((s) => s.selectArtifact);
  const pushToast = useUiStore((s) => s.pushToast);

  const onDownload = (artifactId: string): void => {
    download.mutate(artifactId, {
      onSuccess: ({ url }) => window.location.assign(url),
      // Mutation + transport are silent — this toast is the single error surface.
      onError: (err) =>
        pushToast({ level: "error", message: `Download failed: ${err.message}` }),
    });
  };

  if (query.isPending) {
    return <Skeleton data-testid="turn-artifacts-loading" className="h-6 w-1/2" />;
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

  return (
    <ul className="flex flex-col gap-1">
      {artifacts.map((a) => (
        <li
          key={a.id}
          data-testid="turn-artifact-item"
          data-artifact-id={a.id}
          className="flex items-center gap-2 rounded-md border border-border bg-card px-2 py-1.5 text-xs"
        >
          <button
            type="button"
            data-testid="turn-artifact-select"
            onClick={() => selectArtifact(versionId, a.id)}
            className="flex flex-1 items-center gap-2 truncate text-left hover:text-foreground"
          >
            <span className="text-muted-foreground">{a.kind}</span>
            <span className="truncate font-mono text-foreground">{a.mime ?? "—"}</span>
            <span className="ml-auto shrink-0 font-mono text-muted-foreground">
              {a.bytes === null ? "—" : formatBytes(a.bytes)}
            </span>
          </button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 shrink-0 px-2"
            data-testid="turn-artifact-download"
            disabled={download.isPending}
            onClick={() => onDownload(a.id)}
          >
            Download
          </Button>
        </li>
      ))}
    </ul>
  );
}
