import type { JSX } from "react";
import { useEffect, useState } from "react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useUiStore } from "@/features/ui/store";
import { formatBytes } from "@/features/artifacts/format";
import {
  useArtifactPresignMutation,
  useVersionArtifactsQuery,
} from "@/features/artifacts/queries";
import type { ArtifactMeta } from "@/features/artifacts/types";

/** First 64KB of a text artifact are previewed inline; the rest is elided. */
export const TEXT_PREVIEW_CAP_BYTES = 64 * 1024;

function isImage(mime: string | null): boolean {
  return !!mime && mime.startsWith("image/");
}

function isTextLike(mime: string | null): boolean {
  if (!mime) return false;
  if (mime.startsWith("text/")) return true;
  return /(json|yaml|xml|javascript|x-sh|csv)/.test(mime);
}

/** mime, or a neutral placeholder when null. */
function mimeLabel(a: ArtifactMeta): string {
  return a.mime ?? "—";
}

/** Human-readable size, or a neutral placeholder when bytes is null. */
function sizeLabel(a: ArtifactMeta): string {
  return a.bytes === null ? "—" : formatBytes(a.bytes);
}

/**
 * Right-column Artifact Preview. Anchored to the UI store's `selectedVersionId`
 * (set by the Task Detail version tree). Lists the selected version's artifacts
 * and renders a lightweight preview for a single user-selected artifact. Reuses
 * the existing `features/artifacts/` data access; introduces no new transport.
 */
export function ArtifactPreviewPanel(): JSX.Element {
  const versionId = useUiStore((s) => s.selectedVersionId);

  if (!versionId) {
    return (
      <p
        className="p-4 text-sm text-muted-foreground"
        data-testid="preview-no-version"
      >
        Select a version to preview its artifacts.
      </p>
    );
  }

  // Key by versionId so the selected-artifact state resets on version change
  // (a remount) rather than via a synchronous setState-in-effect.
  return <VersionArtifacts key={versionId} versionId={versionId} />;
}

function VersionArtifacts({ versionId }: { versionId: string }): JSX.Element {
  const query = useVersionArtifactsQuery(versionId);
  const download = useArtifactPresignMutation();
  const pushToast = useUiStore((s) => s.pushToast);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const onDownload = (artifactId: string): void => {
    // Re-mint every click: the prior URL is short-lived and never reused.
    download.mutate(artifactId, {
      onSuccess: ({ url }) => window.location.assign(url),
      // The mutation + transport are both silent, so this is the SINGLE error
      // surface (exactly one toast), never a double-toast.
      onError: (err) =>
        pushToast({ level: "error", message: `Download failed: ${err.message}` }),
    });
  };

  if (query.isPending) {
    return (
      <div data-testid="artifact-list-loading" className="space-y-2 p-4">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-3/4" />
      </div>
    );
  }

  // A 404 (version_not_found) is a defensive no-op here; show a neutral error.
  if (query.error || !query.data) {
    return (
      <p data-testid="artifact-list-error" className="p-4 text-sm text-destructive">
        Failed to load artifacts.
      </p>
    );
  }

  const artifacts = query.data.artifacts;
  if (artifacts.length === 0) {
    return (
      <p data-testid="artifact-list-empty" className="p-4 text-sm text-muted-foreground">
        No artifacts.
      </p>
    );
  }

  const selected = artifacts.find((a) => a.id === selectedId) ?? null;

  return (
    <div className="flex h-full flex-col">
      <ul data-testid="artifact-list" className="flex flex-col gap-1 p-2">
        {artifacts.map((a) => (
          <li key={a.id} data-testid="artifact-row" data-artifact-id={a.id}>
            <div
              className={cn(
                "flex items-center gap-2 rounded-md px-2 py-1.5 text-xs",
                a.id === selectedId
                  ? "bg-accent text-accent-foreground"
                  : "hover:bg-accent/50",
              )}
            >
              <button
                type="button"
                onClick={() => setSelectedId(a.id)}
                className="flex flex-1 items-center gap-2 truncate text-left"
                data-testid="artifact-select"
                aria-pressed={a.id === selectedId}
              >
                <span className="text-muted-foreground">{a.kind}</span>
                <span className="truncate font-mono text-foreground">
                  {mimeLabel(a)}
                </span>
                <span className="ml-auto shrink-0 font-mono text-muted-foreground">
                  {sizeLabel(a)}
                </span>
              </button>
              <Button
                variant="ghost"
                size="sm"
                className="h-7 shrink-0 px-2"
                data-testid="artifact-download"
                disabled={download.isPending}
                onClick={() => onDownload(a.id)}
              >
                Download
              </Button>
            </div>
          </li>
        ))}
      </ul>

      {selected ? (
        <div className="min-h-0 flex-1 border-t border-border">
          {/* Keyed by artifact id so each selection remounts with a fresh,
              lazily-derived initial state (no setState-in-effect). */}
          <ArtifactPreviewBody
            key={selected.id}
            artifact={selected}
            onDownload={onDownload}
          />
        </div>
      ) : (
        <p
          className="border-t border-border p-4 text-xs text-muted-foreground"
          data-testid="artifact-preview-hint"
        >
          Select an artifact to preview it.
        </p>
      )}
    </div>
  );
}

type PreviewState =
  | { phase: "loading" }
  | { phase: "image"; url: string }
  | { phase: "text"; text: string; truncated: boolean }
  | { phase: "binary" }
  | { phase: "error" };

/**
 * Lightweight preview for a single artifact. Images load via <img> from a fresh
 * presigned URL (CSP `img-src` must permit OSS). Text-like artifacts are fetched
 * (subject to OSS CORS) and truncated to TEXT_PREVIEW_CAP_BYTES; a fetch failure
 * degrades to a single inline error with download-only. Other types are
 * download-only with no inline preview. Presign is re-minted per selection and
 * never cached.
 */
function ArtifactPreviewBody({
  artifact,
  onDownload,
}: {
  artifact: ArtifactMeta;
  onDownload: (id: string) => void;
}): JSX.Element {
  const presign = useArtifactPresignMutation();
  const mime = artifact.mime;
  const previewable = isImage(mime) || isTextLike(mime);
  // Initial phase is derived synchronously at mount (this body is keyed by
  // artifact id, so a new selection remounts): "loading" for previewable types,
  // "binary" for everything else. The effect only ever setState()s after an
  // await, never synchronously.
  const [state, setState] = useState<PreviewState>(() =>
    previewable ? { phase: "loading" } : { phase: "binary" },
  );

  useEffect(() => {
    if (!previewable) return;
    let cancelled = false;
    void (async () => {
      try {
        // Re-mint a short-lived URL for THIS artifact only (not cached).
        const { url } = await presign.mutateAsync(artifact.id);
        if (cancelled) return;
        if (isImage(mime)) {
          setState({ phase: "image", url });
          return;
        }
        // Text-like: the second hop (fetch of the OSS URL) is subject to CORS.
        const res = await fetch(url);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const full = await res.text();
        if (cancelled) return;
        const truncated = full.length > TEXT_PREVIEW_CAP_BYTES;
        setState({
          phase: "text",
          text: truncated ? full.slice(0, TEXT_PREVIEW_CAP_BYTES) : full,
          truncated,
        });
      } catch {
        if (!cancelled) setState({ phase: "error" });
      }
    })();

    return () => {
      cancelled = true;
    };
    // Keyed by artifact id (remount per selection); presign identity is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const downloadButton = (
    <Button
      variant="outline"
      size="sm"
      data-testid="preview-download"
      onClick={() => onDownload(artifact.id)}
    >
      Download
    </Button>
  );

  if (state.phase === "loading") {
    return (
      <div data-testid="artifact-preview-loading" className="space-y-2 p-4">
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (state.phase === "image") {
    return (
      <div data-testid="artifact-preview-image" className="overflow-auto p-4">
        {/* Bytes load straight from OSS; never proxied through the app. */}
        <img
          src={state.url}
          alt={`Preview of ${artifact.kind}`}
          className="max-w-full rounded-md border border-border"
        />
      </div>
    );
  }

  if (state.phase === "text") {
    return (
      <div data-testid="artifact-preview-text" className="flex h-full flex-col">
        <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs text-foreground">
          {state.text}
        </pre>
        {state.truncated && (
          <p
            className="flex items-center gap-2 border-t border-border p-2 text-xs text-muted-foreground"
            data-testid="artifact-preview-truncated"
          >
            Preview truncated — {downloadButton} to view the full file.
          </p>
        )}
      </div>
    );
  }

  if (state.phase === "error") {
    return (
      <div
        data-testid="artifact-preview-error"
        className="flex flex-col items-start gap-2 p-4 text-xs text-muted-foreground"
      >
        <span>Preview unavailable. {downloadButton} the file instead.</span>
      </div>
    );
  }

  // binary: download-only, no inline preview.
  return (
    <div
      data-testid="artifact-preview-binary"
      className="flex flex-col items-start gap-2 p-4 text-xs text-muted-foreground"
    >
      <span>No inline preview for this file type.</span>
      {downloadButton}
    </div>
  );
}
