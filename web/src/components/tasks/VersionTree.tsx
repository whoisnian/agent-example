import type { JSX } from "react";
import { useState } from "react";
import type { VersionNode } from "@/features/tasks/types";
import { StatusBadge } from "./StatusBadge";
import { CostBadge } from "./CostBadge";
import { ArtifactList } from "./ArtifactList";

export interface VersionTreeProps {
  versions: VersionNode[];
  currentVersionId: string | null;
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

export function VersionTree({ versions, currentVersionId }: VersionTreeProps): JSX.Element {
  // Which version rows are expanded (showing their artifact list). Ephemeral
  // view state with no cross-component consumer, so it stays local — not in
  // Zustand (which is reserved for non-server, app-level UI state). Declared
  // before any early return to satisfy the rules-of-hooks.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const toggle = (id: string): void =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (versions.length === 0) {
    return (
      <p data-testid="version-tree-empty" className="text-sm text-text-muted">
        No versions yet.
      </p>
    );
  }
  const rows = flatten(versions);

  return (
    <ul data-testid="version-tree" className="flex flex-col gap-1">
      {rows.map(({ node, depth }) => {
        const isCurrent = node.id === currentVersionId;
        const isOpen = expanded.has(node.id);
        return (
          <li
            key={node.id}
            data-testid="version-node"
            data-current={isCurrent}
            className="flex flex-col gap-1 text-sm"
            style={{ paddingLeft: `${depth * 16}px` }}
          >
            <div className="flex items-center gap-2">
              <button
                type="button"
                data-testid="version-expand-toggle"
                aria-expanded={isOpen}
                aria-label={isOpen ? "Hide artifacts" : "Show artifacts"}
                onClick={() => toggle(node.id)}
                className="font-mono text-xs text-text-muted hover:text-text"
              >
                {isOpen ? "▾" : "▸"}
              </button>
              <span className="font-mono text-text">v{node.version_no}</span>
              <StatusBadge status={node.status} />
              <CostBadge cost={node.cost} />
              {isCurrent ? (
                <span data-testid="current-marker" className="text-xs text-accent">
                  current
                </span>
              ) : null}
            </div>
            {isOpen ? (
              <div className="pl-6">
                <ArtifactList versionId={node.id} />
              </div>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
