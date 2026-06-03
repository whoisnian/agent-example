import type { JSX } from "react";
import { useNavigate } from "react-router-dom";
import { useAuthStore } from "@/features/auth/store";
import { Button } from "@/components/primitives/Button";

export function TopBar(): JSX.Element {
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);

  const onLogout = (): void => {
    logout();
    navigate("/login", { replace: true });
  };

  return (
    <header
      className="flex h-12 items-center justify-between border-b border-border bg-surface px-4"
      data-testid="top-bar"
    >
      <div className="flex items-center gap-3">
        <div className="size-6 rounded bg-accent" aria-hidden />
        <span className="text-base font-semibold text-text">Agent Task Platform</span>
      </div>
      <div className="flex items-center gap-3" data-testid="user-area">
        {/* Tolerate a null user (post-logout / half-rehydrated render). */}
        {user ? (
          <span className="text-sm text-text-muted" data-testid="user-email">
            {user.email}
          </span>
        ) : null}
        <Button variant="ghost" onClick={onLogout} data-testid="logout-button">
          Logout
        </Button>
      </div>
    </header>
  );
}
