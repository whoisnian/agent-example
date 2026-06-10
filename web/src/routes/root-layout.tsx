import type { JSX } from "react";
import { Outlet } from "react-router-dom";
import { SideNav } from "@/components/layout/SideNav";
import { PreviewColumn } from "@/components/layout/PreviewColumn";
import { ArtifactPreviewPanel } from "@/features/artifacts/ArtifactPreviewPanel";

/**
 * Three-column application shell: left navigation column, center route content,
 * and the right Artifact Preview column. The preview *content* is filled in by
 * the artifacts feature (PR3); this layout only owns the column structure.
 *
 * The inner wrapper exists so the preview column's percentage width resolves
 * against the width remaining beside the nav (the spec's proportion base),
 * not the full viewport. Center content is capped to a reading width.
 */
export function RootLayout(): JSX.Element {
  return (
    <div
      className="flex h-screen overflow-hidden bg-background text-foreground"
      data-testid="root-layout"
    >
      <SideNav />
      <div className="flex min-w-0 flex-1">
        <main className="min-w-0 flex-1 overflow-auto p-6" data-testid="content-slot">
          {/* h-full gives page-internal scrolling (TaskDetail's conversation
              body) a definite height; taller pages still scroll via <main>. */}
          <div className="mx-auto h-full w-full max-w-4xl">
            <Outlet />
          </div>
        </main>
        <PreviewColumn>
          <ArtifactPreviewPanel />
        </PreviewColumn>
      </div>
    </div>
  );
}
