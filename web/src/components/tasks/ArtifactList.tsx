import type { JSX } from "react";
import { Button } from "@/components/primitives/Button";
import { useUiStore } from "@/features/ui/store";
import { formatBytes } from "@/features/artifacts/format";
import { useArtifactPresignMutation, useVersionArtifactsQuery } from "@/features/artifacts/queries";
import type { ArtifactMeta } from "@/features/artifacts/types";

export interface ArtifactListProps {
  versionId: string;
}

/**
 * Lazily lists one version's artifacts and offers a direct-to-OSS download per
 * row. Mounted only when a VersionTree row is expanded, so the query (keyed by
 * versionId) fires on first expand. Renders distinct loading / empty / error
 * states. Download mints a fresh presigned URL on each click (never cached) and
 * navigates the browser straight to OSS — bytes are never proxied through the app.
 */
export function ArtifactList({ versionId }: ArtifactListProps): JSX.Element {
  const query = useVersionArtifactsQuery(versionId);
  const presign = useArtifactPresignMutation();
  const pushToast = useUiStore((s) => s.pushToast);

  const onDownload = (artifactId: string): void => {
    // Re-mint every click: the prior URL is short-lived and never reused.
    presign.mutate(artifactId, {
      onSuccess: ({ url }) => {
        // Same-tab navigation to the presigned GET; OSS Content-Disposition
        // drives the filename. No target=_blank (popup-blocker risk).
        window.location.assign(url);
      },
      // The mutation + transport are both silent, so this is the SINGLE error
      // surface (exactly one toast), never a double-toast.
      onError: (err) => {
        pushToast({ level: "error", message: `Download failed: ${err.message}` });
      },
    });
  };

  if (query.isPending) {
    return (
      <p data-testid="artifact-list-loading" className="text-xs text-text-muted">
        Loading artifacts…
      </p>
    );
  }

  // A 404 (version_not_found) is a defensive no-op here — the row only renders
  // for a version already present in the tree; show a neutral error rather than
  // a not-found screen. Any other transport/server error lands here too.
  if (query.error || !query.data) {
    return (
      <p data-testid="artifact-list-error" className="text-xs text-danger">
        Failed to load artifacts.
      </p>
    );
  }

  const artifacts = query.data.artifacts;
  if (artifacts.length === 0) {
    return (
      <p data-testid="artifact-list-empty" className="text-xs text-text-muted">
        No artifacts.
      </p>
    );
  }

  return (
    <ul data-testid="artifact-list" className="flex flex-col gap-1">
      {artifacts.map((a) => (
        <li
          key={a.id}
          data-testid="artifact-row"
          data-artifact-id={a.id}
          className="flex items-center gap-3 text-xs"
        >
          <span className="text-text-muted">{a.kind}</span>
          <span className="font-mono text-text">{mimeLabel(a)}</span>
          <span className="font-mono text-text-muted">{sizeLabel(a)}</span>
          <Button
            data-testid="artifact-download"
            disabled={presign.isPending}
            onClick={() => onDownload(a.id)}
          >
            Download
          </Button>
        </li>
      ))}
    </ul>
  );
}

/** mime, or a neutral placeholder when null. */
function mimeLabel(a: ArtifactMeta): string {
  return a.mime ?? "—";
}

/** Human-readable size, or a neutral placeholder when bytes is null. */
function sizeLabel(a: ArtifactMeta): string {
  return a.bytes === null ? "—" : formatBytes(a.bytes);
}
