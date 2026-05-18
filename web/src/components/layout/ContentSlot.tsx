import type { JSX } from "react";
import type { ReactNode } from "react";

export function ContentSlot({ children }: { children: ReactNode }): JSX.Element {
  return (
    <main className="flex-1 overflow-auto bg-bg p-6" data-testid="content-slot">
      {children}
    </main>
  );
}
