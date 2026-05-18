import type { JSX } from "react";
import { Outlet } from "react-router-dom";
import { TopBar } from "@/components/layout/TopBar";
import { SideNav } from "@/components/layout/SideNav";
import { ContentSlot } from "@/components/layout/ContentSlot";

export function RootLayout(): JSX.Element {
  return (
    <div className="flex h-screen flex-col bg-bg text-text" data-testid="root-layout">
      <TopBar />
      <div className="flex flex-1 overflow-hidden">
        <SideNav />
        <ContentSlot>
          <Outlet />
        </ContentSlot>
      </div>
    </div>
  );
}
