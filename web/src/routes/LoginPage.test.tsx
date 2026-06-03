import type { JSX } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse, delay } from "msw";
import { MemoryRouter, type InitialEntry } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { DEV_LOGIN } from "@/test/mocks/handlers";
import { useAuthStore } from "@/features/auth/store";
import { useUiStore } from "@/features/ui/store";
import { LoginPage } from "@/routes/LoginPage";

const navigateMock = vi.fn();
vi.mock("react-router-dom", async (orig) => {
  const actual = await orig<typeof import("react-router-dom")>();
  return { ...actual, useNavigate: () => navigateMock };
});

function wrap(entry: InitialEntry = "/login"): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[entry]}>
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

async function submitCreds(email: string, password: string): Promise<void> {
  await userEvent.type(screen.getByTestId("email-input"), email);
  await userEvent.type(screen.getByTestId("password-input"), password);
  await userEvent.click(screen.getByTestId("login-submit"));
}

describe("LoginPage", () => {
  beforeEach(() => {
    navigateMock.mockClear();
    window.localStorage.clear();
    useAuthStore.setState({ token: null, user: null });
    useUiStore.setState({ toasts: [] });
  });

  it("stores the session and redirects to the intended `from` route", async () => {
    render(wrap({ pathname: "/login", state: { from: "/tasks/abc-123" } }));
    await submitCreds(DEV_LOGIN.email, DEV_LOGIN.password);
    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith("/tasks/abc-123", { replace: true }),
    );
    expect(useAuthStore.getState().token).toBe("test-jwt-token");
    expect(useAuthStore.getState().user?.email).toBe(DEV_LOGIN.email);
  });

  it("falls back to /tasks when `from` is an unsafe (open-redirect) path", async () => {
    render(wrap({ pathname: "/login", state: { from: "/\\evil.example" } }));
    await submitCreds(DEV_LOGIN.email, DEV_LOGIN.password);
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/tasks", { replace: true }));
  });

  it("shows one indistinct message on invalid_credentials and does not redirect", async () => {
    render(wrap());
    await submitCreds("wrong@example.com", "nope");
    expect(await screen.findByTestId("login-error")).toHaveTextContent(
      "Incorrect email or password.",
    );
    expect(navigateMock).not.toHaveBeenCalled();
    expect(useAuthStore.getState().token).toBeNull();
  });

  it("disables submit while the request is pending", async () => {
    server.use(
      http.post("http://localhost/api/v1/auth/login", async () => {
        await delay(50);
        return HttpResponse.json(
          { code: "invalid_credentials", message: "x", data: null, trace_id: "t" },
          { status: 401 },
        );
      }),
    );
    render(wrap());
    await submitCreds(DEV_LOGIN.email, DEV_LOGIN.password);
    await waitFor(() => expect(screen.getByTestId("login-submit")).toBeDisabled());
  });

  it("never raises a global toast on a login error", async () => {
    render(wrap());
    await submitCreds("wrong@example.com", "nope");
    await screen.findByTestId("login-error");
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });
});
