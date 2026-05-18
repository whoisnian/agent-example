import type { JSX } from "react";
export function TopBar(): JSX.Element {
  return (
    <header
      className="flex h-12 items-center justify-between border-b border-border bg-surface px-4"
      data-testid="top-bar"
    >
      <div className="flex items-center gap-3">
        <div className="size-6 rounded bg-accent" aria-hidden />
        <span className="text-base font-semibold text-text">Agent Task Platform</span>
      </div>
      <div className="text-sm text-text-muted" data-testid="user-area">
        user
      </div>
    </header>
  );
}
