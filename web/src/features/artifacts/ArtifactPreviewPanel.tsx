import type { JSX, ReactNode } from "react";
import { useEffect, useState } from "react";
import { Code, Copy, Eye, RefreshCw, X } from "lucide-react";
import { cn } from "@/lib/cn";
import { resolveApiUrl } from "@/services/http";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useUiStore } from "@/features/ui/store";
import { formatBytes } from "@/features/artifacts/format";
import {
  useArtifactPresignMutation,
  usePreviewMintMutation,
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

function isHtml(mime: string | null): boolean {
  return mime === "text/html";
}

/** mime, or a neutral placeholder when null. */
function mimeLabel(a: ArtifactMeta): string {
  return a.mime ?? "—";
}

/** Preferred display label: the version-relative path, falling back to the
 *  opaque `kind` for legacy null-path rows (never an empty label). */
function displayLabel(a: ArtifactMeta): string {
  return a.path ?? a.kind;
}

/** Build a preview file URL under the tokenized base: `<base>/<encoded path>`,
 *  encoding each path segment but preserving the `/` separators so relative
 *  asset references resolve correctly. */
function previewFileURL(baseURL: string, path: string): string {
  const encoded = path.split("/").map(encodeURIComponent).join("/");
  return `${baseURL}/${encoded}`;
}

/** Human-readable size, or a neutral placeholder when bytes is null. */
function sizeLabel(a: ArtifactMeta): string {
  return a.bytes === null ? "—" : formatBytes(a.bytes);
}

interface ToolbarProps {
  /** Selected artifact for the title; null falls back to the generic title. */
  artifact: ArtifactMeta | null;
  /** Copy is enabled iff a handler is present; otherwise reason explains why. */
  onCopy?: (() => void) | undefined;
  copyReason?: string | undefined;
  onRefresh?: (() => void) | undefined;
  /** Render/source toggle for HTML artifacts (null = not applicable). */
  viewToggle?: ReactNode;
  children: ReactNode;
}

/**
 * Panel chrome: the header toolbar (title · type, Copy, Refresh, close) above
 * the content area. Rendered in EVERY panel state — the panel is mounted
 * shell-wide, so the close control must stay reachable even with no version
 * selected or while the list is loading/empty/failed.
 */
function PanelChrome({
  artifact,
  onCopy,
  copyReason,
  onRefresh,
  viewToggle,
  children,
}: ToolbarProps): JSX.Element {
  const togglePreview = useUiStore((s) => s.togglePreview);
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div
        data-testid="preview-toolbar"
        className="flex h-12 shrink-0 items-center gap-1 border-b border-border px-3"
      >
        <span
          data-testid="preview-title"
          className="min-w-0 flex-1 truncate text-sm font-medium text-foreground"
        >
          {artifact ? `${displayLabel(artifact)} · ${mimeLabel(artifact)}` : "Artifact Preview"}
        </span>
        {viewToggle}
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          data-testid="preview-copy"
          disabled={!onCopy}
          title={onCopy ? "Copy content" : copyReason}
          aria-label="Copy content"
          onClick={onCopy}
        >
          <Copy className="size-4" aria-hidden />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          data-testid="preview-refresh"
          disabled={!onRefresh}
          title={onRefresh ? "Re-mint the URL and reload the preview" : "Select an artifact first"}
          aria-label="Refresh preview"
          onClick={onRefresh}
        >
          <RefreshCw className="size-4" aria-hidden />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          data-testid="preview-close"
          aria-label="Collapse artifact preview"
          onClick={togglePreview}
        >
          <X className="size-4" aria-hidden />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">{children}</div>
    </div>
  );
}

/**
 * Right-column Artifact Preview. Anchored to the UI store's selection pair
 * (`selectedVersionId` + `selectedArtifactId`), written by the conversation
 * turns' artifact cards and by the panel's own rows (one shared selection).
 * Lists the selected version's artifacts and previews the selected artifact.
 * Reuses the existing `features/artifacts/` data access; no new transport.
 */
export function ArtifactPreviewPanel(): JSX.Element {
  const versionId = useUiStore((s) => s.selectedVersionId);

  if (!versionId) {
    return (
      <PanelChrome artifact={null} copyReason="Select an artifact first">
        <p className="p-4 text-sm text-muted-foreground" data-testid="preview-no-version">
          Select a version to preview its artifacts.
        </p>
      </PanelChrome>
    );
  }

  // Key by versionId so per-version local state (refresh nonce, html view)
  // resets on version change via a remount.
  return <VersionArtifacts key={versionId} versionId={versionId} />;
}

interface LoadedText {
  artifactId: string;
  text: string;
  truncated: boolean;
}

function VersionArtifacts({ versionId }: { versionId: string }): JSX.Element {
  const query = useVersionArtifactsQuery(versionId);
  const download = useArtifactPresignMutation();
  const pushToast = useUiStore((s) => s.pushToast);
  const selectedArtifactId = useUiStore((s) => s.selectedArtifactId);
  const setSelectedArtifactId = useUiStore((s) => s.setSelectedArtifactId);

  // Refresh remounts the preview body (re-mints presign, replays the load).
  const [refreshNonce, setRefreshNonce] = useState(0);
  // HTML artifacts default to the rendered view; "source" reuses the text path.
  const [htmlView, setHtmlView] = useState<"render" | "source">("render");
  // The loaded preview text (for Copy), reported up by the preview body.
  const [loaded, setLoaded] = useState<LoadedText | null>(null);

  // Selection changes (panel row OR conversation card) reset the view + buffer.
  // Adjusted during render (not in an effect) per the React guidance.
  const [prevSelectedId, setPrevSelectedId] = useState(selectedArtifactId);
  if (prevSelectedId !== selectedArtifactId) {
    setPrevSelectedId(selectedArtifactId);
    setHtmlView("render");
    setLoaded(null);
  }

  const artifacts = query.data?.artifacts ?? [];
  // A dangling selection (artifact not in this version's list) renders as
  // no-artifact-selected — never an error.
  const selected = artifacts.find((a) => a.id === selectedArtifactId) ?? null;

  const onDownload = (artifactId: string): void => {
    // Re-mint every click: the prior URL is short-lived and never reused.
    download.mutate(artifactId, {
      onSuccess: ({ url }) => window.location.assign(url),
      // The mutation + transport are both silent, so this is the SINGLE error
      // surface (exactly one toast), never a double-toast.
      onError: (err) => pushToast({ level: "error", message: `Download failed: ${err.message}` }),
    });
  };

  // --- toolbar wiring ---
  const clipboardAvailable =
    typeof navigator !== "undefined" && typeof navigator.clipboard?.writeText === "function";
  const copyText = selected && loaded && loaded.artifactId === selected.id ? loaded : null;
  let copyReason = "Select an artifact first";
  if (selected) {
    if (!clipboardAvailable) copyReason = "Clipboard unavailable in this context";
    else if (!copyText) copyReason = "No text content loaded to copy";
    else if (copyText.truncated)
      copyReason = "Content exceeds the preview cap — download the full file";
  }
  const canCopy = !!copyText && !copyText.truncated && clipboardAvailable;
  const onCopy = canCopy
    ? (): void => {
        void navigator.clipboard.writeText(copyText.text).then(
          () => pushToast({ level: "success", message: "Copied to clipboard" }),
          () => pushToast({ level: "error", message: "Copy failed" }),
        );
      }
    : undefined;
  const onRefresh = selected
    ? (): void => {
        setLoaded(null);
        setRefreshNonce((n) => n + 1);
      }
    : undefined;
  // A prominent icon+label button (spec: not a text-only ghost affordance).
  const viewToggle =
    selected && isHtml(selected.mime) ? (
      <Button
        variant="outline"
        size="sm"
        className="h-8 gap-1.5 px-2.5 text-xs"
        data-testid="preview-view-toggle"
        aria-label={htmlView === "render" ? "View source" : "View rendered"}
        onClick={() => setHtmlView((v) => (v === "render" ? "source" : "render"))}
      >
        {htmlView === "render" ? (
          <>
            <Code className="size-3.5" aria-hidden />
            Source
          </>
        ) : (
          <>
            <Eye className="size-3.5" aria-hidden />
            Render
          </>
        )}
      </Button>
    ) : null;

  const chrome = {
    artifact: selected,
    onCopy,
    copyReason,
    onRefresh,
    viewToggle,
  };

  if (query.isPending) {
    return (
      <PanelChrome {...chrome}>
        <div data-testid="artifact-list-loading" className="space-y-2 p-4">
          <Skeleton className="h-6 w-full" />
          <Skeleton className="h-6 w-3/4" />
        </div>
      </PanelChrome>
    );
  }

  // A 404 (version_not_found) is a defensive no-op here; show a neutral error.
  if (query.error || !query.data) {
    return (
      <PanelChrome {...chrome}>
        <p data-testid="artifact-list-error" className="p-4 text-sm text-destructive">
          Failed to load artifacts.
        </p>
      </PanelChrome>
    );
  }

  if (artifacts.length === 0) {
    return (
      <PanelChrome {...chrome}>
        <p data-testid="artifact-list-empty" className="p-4 text-sm text-muted-foreground">
          No artifacts.
        </p>
      </PanelChrome>
    );
  }

  return (
    <PanelChrome {...chrome}>
      <div className="flex h-full flex-col">
        <ul data-testid="artifact-list" className="flex flex-col gap-1 p-2">
          {artifacts.map((a) => (
            <li key={a.id} data-testid="artifact-row" data-artifact-id={a.id}>
              <div
                className={cn(
                  "flex items-stretch gap-2 rounded-md pr-2 text-xs",
                  a.id === selected?.id ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                )}
              >
                {/* Padding lives on the button so the selection hit area
                    spans the full row height (spec: full-row selection). */}
                <button
                  type="button"
                  onClick={() => setSelectedArtifactId(a.id)}
                  className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left"
                  data-testid="artifact-select"
                  aria-pressed={a.id === selected?.id}
                >
                  <span className="truncate font-mono text-foreground">{displayLabel(a)}</span>
                  <span className="shrink-0 text-muted-foreground">{mimeLabel(a)}</span>
                  <span className="ml-auto shrink-0 font-mono text-muted-foreground">
                    {sizeLabel(a)}
                  </span>
                </button>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 shrink-0 self-center px-2"
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
            {/* Keyed so a new selection, a Refresh, or a render/source toggle
                remounts with fresh state (each remount re-mints the presign). */}
            <ArtifactPreviewBody
              key={`${selected.id}:${refreshNonce}:${htmlView}`}
              artifact={selected}
              versionId={versionId}
              htmlRender={isHtml(selected.mime) && htmlView === "render"}
              onDownload={onDownload}
              onTextLoaded={(text, truncated) =>
                setLoaded({ artifactId: selected.id, text, truncated })
              }
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
    </PanelChrome>
  );
}

type PreviewState =
  | { phase: "loading" }
  | { phase: "image"; url: string }
  | { phase: "html"; url: string }
  | { phase: "text"; text: string; truncated: boolean }
  | { phase: "binary" }
  | { phase: "presign-error" }
  | { phase: "error" };

/**
 * Preview for a single artifact. All loads use the freshly minted same-origin
 * signed download URL (API download proxy, add-artifact-download-proxy — the
 * browser never contacts OSS). Images load via <img>. HTML artifacts in the
 * rendered view load the URL in a sandboxed iframe (`allow-scripts`, never
 * `allow-same-origin`) — the frame runs in an opaque origin, so an in-frame
 * HTTP failure is still NOT detectable from the host page and recovery is the
 * toolbar Refresh, while a presign failure renders an inline error. Text-like
 * artifacts (and the HTML source view) are fetched same-origin (no CORS gate)
 * and truncated to TEXT_PREVIEW_CAP_BYTES. Other types are download-only.
 * Presign is re-minted per mount and never cached.
 */
function ArtifactPreviewBody({
  artifact,
  versionId,
  htmlRender,
  onDownload,
  onTextLoaded,
}: {
  artifact: ArtifactMeta;
  versionId: string;
  htmlRender: boolean;
  onDownload: (id: string) => void;
  onTextLoaded?: ((text: string, truncated: boolean) => void) | undefined;
}): JSX.Element {
  const presign = useArtifactPresignMutation();
  const previewMint = usePreviewMintMutation();
  const mime = artifact.mime;
  const previewable = htmlRender || isImage(mime) || isTextLike(mime);
  // Initial phase is derived synchronously at mount (this body is keyed by
  // artifact id + nonce + view, so any change remounts): "loading" for
  // previewable types, "binary" for everything else.
  const [state, setState] = useState<PreviewState>(() =>
    previewable ? { phase: "loading" } : { phase: "binary" },
  );

  useEffect(() => {
    if (!previewable) return;
    let cancelled = false;
    void (async () => {
      // HTML rendered view: prefer the directory-aware preview base so the
      // document's relative css/js/img references resolve to sibling artifacts
      // of the version (src = <base>/<path>). Fall back to the single-file
      // signed URL for a legacy null-path artifact (no relative resolution).
      if (htmlRender && artifact.path) {
        let baseURL: string;
        try {
          ({ base_url: baseURL } = await previewMint.mutateAsync(versionId));
        } catch {
          if (!cancelled) setState({ phase: "presign-error" });
          return;
        }
        if (cancelled) return;
        setState({ phase: "html", url: previewFileURL(baseURL, artifact.path) });
        return;
      }

      let url: string;
      try {
        // Re-mint a short-lived URL for THIS artifact only (not cached).
        ({ url } = await presign.mutateAsync(artifact.id));
      } catch {
        // Presign failure is reliably detectable → inline error + Refresh.
        if (!cancelled) setState({ phase: "presign-error" });
        return;
      }
      if (cancelled) return;
      try {
        if (htmlRender) {
          // Null-path fallback: mount the iframe on the single-file URL; what
          // happens inside the sandboxed opaque-origin frame is not observable.
          setState({ phase: "html", url });
          return;
        }
        if (isImage(mime)) {
          setState({ phase: "image", url });
          return;
        }
        // Text-like: the second hop is a plain same-origin fetch of the
        // download proxy URL (no CORS gate since add-artifact-download-proxy).
        // The presign `url` is an opaque API-relative path — resolve it with
        // the shared transport base (node's fetch under vitest also rejects
        // relative URLs).
        const res = await fetch(resolveApiUrl(url));
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const full = await res.text();
        if (cancelled) return;
        const truncated = full.length > TEXT_PREVIEW_CAP_BYTES;
        const text = truncated ? full.slice(0, TEXT_PREVIEW_CAP_BYTES) : full;
        setState({ phase: "text", text, truncated });
        onTextLoaded?.(text, truncated);
      } catch {
        if (!cancelled) setState({ phase: "error" });
      }
    })();

    return () => {
      cancelled = true;
    };
    // Keyed remount per selection/refresh/view; identities are stable in between.
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

  if (state.phase === "html") {
    return (
      <iframe
        data-testid="preview-html-frame"
        src={state.url}
        // Scripts may run, but in an opaque origin: NEVER add allow-same-origin.
        sandbox="allow-scripts"
        title={`Rendered preview of ${artifact.kind}`}
        className="size-full border-0 bg-background"
      />
    );
  }

  if (state.phase === "image") {
    return (
      <div data-testid="artifact-preview-image" className="overflow-auto p-4">
        {/* Bytes come from the same-origin API download proxy via <img>. */}
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
        <pre className="scrollbar-themed min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs text-foreground">
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

  if (state.phase === "presign-error") {
    return (
      <div
        data-testid="preview-presign-error"
        className="flex flex-col items-start gap-2 p-4 text-xs text-muted-foreground"
      >
        <span>Could not prepare the preview — use Refresh to retry.</span>
        {downloadButton}
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
