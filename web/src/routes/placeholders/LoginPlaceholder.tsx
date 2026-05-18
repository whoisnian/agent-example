import type { JSX } from "react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuthStore } from "@/features/auth/store";
import { Button } from "@/components/primitives/Button";

export function LoginPlaceholder(): JSX.Element {
  const [value, setValue] = useState("");
  const setToken = useAuthStore((s) => s.setToken);
  const navigate = useNavigate();

  const onSubmit = (e: React.FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    const t = value.trim();
    if (!t) return;
    setToken(t);
    navigate("/tasks", { replace: true });
  };

  return (
    <div
      className="flex min-h-screen items-center justify-center bg-bg p-6"
      data-testid="placeholder-login"
    >
      <form
        onSubmit={onSubmit}
        className="w-96 rounded border border-border bg-surface p-6 shadow-md"
      >
        <h1 className="mb-4 text-xl font-semibold text-text">Login</h1>
        <p className="mb-4 text-xs text-text-muted">
          Paste any non-empty token. Scaffold does not validate against a server.
        </p>
        <textarea
          value={value}
          onChange={(e): void => setValue(e.target.value)}
          rows={3}
          aria-label="Token"
          data-testid="token-input"
          className="mb-4 w-full rounded border border-border bg-bg p-2 text-sm text-text outline-none"
        />
        <Button type="submit" data-testid="login-submit">
          Continue
        </Button>
      </form>
    </div>
  );
}
