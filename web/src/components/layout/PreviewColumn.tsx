import type { JSX, ReactNode } from "react";
import { PanelRightOpen, X } from "lucide-react";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { useUiStore } from "@/features/ui/store";

/**
 * Right column of the three-column shell — the responsive container for the
 * Artifact Preview. On `lg+` it is a static column; below `lg` it becomes a
 * right-side drawer/overlay with a backdrop, keeping the center content usable.
 * Collapse state lives in the UI store. The preview *content* is passed as
 * children (the artifacts surface is owned by features/artifacts).
 */
export function PreviewColumn({ children }: { children: ReactNode }): JSX.Element {
  const collapsed = useUiStore((s) => s.previewCollapsed);
  const togglePreview = useUiStore((s) => s.togglePreview);

  return (
    <>
      {/* Re-open affordance when the panel is collapsed. */}
      {collapsed && (
        <Button
          variant="outline"
          size="icon"
          className="fixed right-3 top-3 z-30 size-9"
          onClick={togglePreview}
          aria-label="Open artifact preview"
          data-testid="preview-open"
        >
          <PanelRightOpen className="size-4" aria-hidden />
        </Button>
      )}

      {/* Backdrop for the drawer on small screens. */}
      {!collapsed && (
        <button
          type="button"
          aria-label="Close preview overlay"
          onClick={togglePreview}
          className="fixed inset-0 z-30 bg-black/50 lg:hidden"
          data-testid="preview-backdrop"
        />
      )}

      <aside
        data-testid="preview-column"
        aria-hidden={collapsed}
        className={cn(
          "flex w-80 shrink-0 flex-col border-l border-border bg-card",
          // Drawer on small screens; static column on lg+.
          "fixed inset-y-0 right-0 z-40 transition-transform lg:static lg:z-auto lg:translate-x-0",
          collapsed ? "translate-x-full lg:hidden" : "translate-x-0 lg:flex",
        )}
      >
        <div className="flex h-14 shrink-0 items-center justify-between border-b border-border px-3">
          <span className="text-sm font-medium text-foreground">
            Artifact Preview
          </span>
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            onClick={togglePreview}
            aria-label="Collapse artifact preview"
            data-testid="preview-close"
          >
            <X className="size-4" aria-hidden />
          </Button>
        </div>
        <div className="flex-1 overflow-auto">{children}</div>
      </aside>
    </>
  );
}
