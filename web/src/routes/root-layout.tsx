import type { JSX } from "react";
import { Outlet } from "react-router-dom";
import { SideNav } from "@/components/layout/SideNav";
import { PreviewColumn } from "@/components/layout/PreviewColumn";

/**
 * Three-column application shell: left navigation column, center route content,
 * and the right Artifact Preview column. The preview *content* is filled in by
 * the artifacts feature (PR3); this layout only owns the column structure.
 */
export function RootLayout(): JSX.Element {
  return (
    <div
      className="flex h-screen overflow-hidden bg-background text-foreground"
      data-testid="root-layout"
    >
      <SideNav />
      <main className="flex-1 overflow-auto p-6" data-testid="content-slot">
        <Outlet />
      </main>
      <PreviewColumn>
        <p
          className="p-4 text-sm text-muted-foreground"
          data-testid="preview-placeholder"
        >
          Select a version to preview its artifacts.
        </p>
      </PreviewColumn>
    </div>
  );
}
