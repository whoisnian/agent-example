import type { JSX } from "react";
import { useState, type FormEvent } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Button } from "@/components/primitives/Button";
import { ApiError } from "@/services/http";
import { useAuthStore } from "@/features/auth/store";
import { useLoginMutation } from "@/features/auth/mutations";

/**
 * Resolve the post-login redirect target. Accept only a safe internal path:
 * begins with `/` and the second char is neither `/` nor `\` (rejecting the
 * protocol-relative `//evil` and `/\evil` open-redirect vectors). Otherwise
 * fall back to `/tasks` (see design D6 / S5).
 */
function safeRedirect(from: unknown): string {
  if (typeof from === "string" && from.startsWith("/") && from[1] !== "/" && from[1] !== "\\") {
    return from;
  }
  return "/tasks";
}

export function LoginPage(): JSX.Element {
  const navigate = useNavigate();
  const location = useLocation();
  const setSession = useAuthStore((s) => s.setSession);
  const mutation = useLoginMutation();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);

  const onSubmit = (e: FormEvent): void => {
    e.preventDefault();
    setError(null);
    mutation.mutate(
      { email, password },
      {
        onSuccess: (data) => {
          setSession(data.token, data.user);
          const from = (location.state as { from?: unknown } | null)?.from;
          navigate(safeRedirect(from), { replace: true });
        },
        onError: (err) => {
          // Credential failure: one fixed message, never revealing which field
          // was wrong (mirrors the API's indistinguishability).
          if (err instanceof ApiError && err.code === "invalid_credentials") {
            setError("Incorrect email or password.");
          } else if (err instanceof ApiError && err.code === "invalid_input") {
            setError("Please check your input and try again.");
          } else if (err instanceof ApiError) {
            setError(err.message);
          } else {
            setError("Login failed. Please try again.");
          }
        },
      },
    );
  };

  return (
    <div
      className="flex min-h-screen items-center justify-center bg-bg p-6"
      data-testid="login-page"
    >
      <form
        onSubmit={onSubmit}
        className="w-96 rounded border border-border bg-surface p-6 shadow-md"
      >
        <h1 className="mb-4 text-xl font-semibold text-text">Login</h1>
        {error ? (
          <p data-testid="login-error" className="mb-4 text-sm text-danger">
            {error}
          </p>
        ) : null}
        <label className="mb-4 flex flex-col gap-1 text-sm text-text-muted">
          Email
          <input
            type="email"
            value={email}
            onChange={(e): void => setEmail(e.target.value)}
            data-testid="email-input"
            autoComplete="username"
            className="rounded border border-border bg-bg px-2 py-1 text-text outline-none"
          />
        </label>
        <label className="mb-4 flex flex-col gap-1 text-sm text-text-muted">
          Password
          <input
            type="password"
            value={password}
            onChange={(e): void => setPassword(e.target.value)}
            data-testid="password-input"
            autoComplete="current-password"
            className="rounded border border-border bg-bg px-2 py-1 text-text outline-none"
          />
        </label>
        <Button type="submit" disabled={mutation.isPending} data-testid="login-submit">
          {mutation.isPending ? "Signing in…" : "Sign in"}
        </Button>
      </form>
    </div>
  );
}
