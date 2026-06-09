import type { JSX } from "react";
import { cn } from "@/lib/cn";
import { useUiStore } from "@/features/ui/store";
import type { RollbackMode, VersionNode } from "@/features/tasks/types";
import { StatusBadge } from "./StatusBadge";
import { CostBadge } from "./CostBadge";
import { RollbackControl } from "./RollbackControl";

const BUSY_REASON = "Task is busy — wait for the active version to finish";

export interface VersionTreeProps {
  versions: VersionNode[];
  currentVersionId: string | null;
  /** Whether the task is in an active state — disables ALL rollback actions
   *  (the backend requires a non-active task for both modes). */
  taskActive?: boolean;
  /** When provided, each non-current row offers a rollback picker. The version
   *  id is supplied here; the leaf RollbackControl is id-agnostic. */
  onRollback?: (versionId: string, mode: RollbackMode, prompt?: string) => void;
  /** True while a rollback request is in flight — disables the pickers. */
  rollbackPending?: boolean;
}

interface Row {
  node: VersionNode;
  depth: number;
}

/**
 * Flatten the parent-linked versions into depth-annotated rows. Roots
 * (parent_id null or pointing outside the set) come first; children follow
 * their parent. Within a sibling group, order by version_no ascending.
 */
function flatten(versions: VersionNode[]): Row[] {
  const byParent = new Map<string | null, VersionNode[]>();
  const ids = new Set(versions.map((v) => v.id));
  for (const v of versions) {
    const key = v.parent_id && ids.has(v.parent_id) ? v.parent_id : null;
    const group = byParent.get(key) ?? [];
    group.push(v);
    byParent.set(key, group);
  }
  for (const group of byParent.values()) group.sort((a, b) => a.version_no - b.version_no);

  const rows: Row[] = [];
  const walk = (parentKey: string | null, depth: number): void => {
    for (const node of byParent.get(parentKey) ?? []) {
      rows.push({ node, depth });
      walk(node.id, depth + 1);
    }
  };
  walk(null, 0);
  return rows;
}

export function VersionTree({
  versions,
  currentVersionId,
  taskActive = false,
  onRollback,
  rollbackPending = false,
}: VersionTreeProps): JSX.Element {
  // Selecting a row anchors the right-column Artifact Preview (the preview owns
  // the artifact fetch; the tree only drives the selection). Selection lives in
  // the global UI store so the preview column can read it.
  const selectedVersionId = useUiStore((s) => s.selectedVersionId);
  const setSelectedVersionId = useUiStore((s) => s.setSelectedVersionId);

  if (versions.length === 0) {
    return (
      <p data-testid="version-tree-empty" className="text-sm text-muted-foreground">
        No versions yet.
      </p>
    );
  }
  const rows = flatten(versions);

  return (
    <ul data-testid="version-tree" className="flex flex-col gap-1">
      {rows.map(({ node, depth }) => {
        const isCurrent = node.id === currentVersionId;
        const isSelected = node.id === selectedVersionId;
        return (
          <li
            key={node.id}
            data-testid="version-node"
            data-current={isCurrent}
            data-selected={isSelected}
            className="flex flex-col gap-1 text-sm"
            style={{ paddingLeft: `${depth * 16}px` }}
          >
            <button
              type="button"
              data-testid="version-select"
              aria-selected={isSelected}
              onClick={() => setSelectedVersionId(node.id)}
              className={cn(
                "flex items-center gap-2 rounded-md px-2 py-1 text-left transition-colors",
                isSelected
                  ? "bg-accent text-accent-foreground"
                  : "hover:bg-accent/50",
              )}
            >
              <span className="font-mono text-foreground">v{node.version_no}</span>
              <StatusBadge status={node.status} />
              <CostBadge cost={node.cost} />
              {isCurrent ? (
                <span data-testid="current-marker" className="text-xs text-primary">
                  current
                </span>
              ) : null}
            </button>
            {onRollback && !isCurrent ? (
              <div className="pl-6">
                <RollbackControl
                  branchDisabled={taskActive}
                  branchReason={taskActive ? BUSY_REASON : undefined}
                  switchDisabled={taskActive || node.is_active}
                  switchReason={
                    taskActive
                      ? BUSY_REASON
                      : node.is_active
                        ? "Can only switch to a finished version"
                        : undefined
                  }
                  pending={rollbackPending}
                  onRollback={(mode, prompt) => onRollback(node.id, mode, prompt)}
                />
              </div>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
