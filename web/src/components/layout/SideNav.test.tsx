import type { JSX } from "react";
import { describe, expect, it, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { RequireAuth } from "@/routes/require-auth";
import { RootLayout } from "@/routes/root-layout";
import { useAuthStore } from "@/features/auth/store";
import { useUiStore } from "@/features/ui/store";
import { SideNav } from "@/components/layout/SideNav";

const USER = { id: "u1", tenant_id: "t1", email: "dev@example.com" } as const;

function GatedTree({ initial }: { initial: string }): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[initial]}>
        <Routes>
          <Route path="/login" element={<div data-testid="login-stub" />} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <RootLayout />
              </RequireAuth>
            }
          >
            <Route path="tasks" element={<div data-testid="tasks-stub" />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("SideNav (left navigation column)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    useAuthStore.setState({ token: null, user: null });
    // Expanded nav so the user email is visible (it hides on the icon rail).
    useUiStore.setState({ navCollapsed: false });
  });

  it("shows the logged-in user's email", () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(
      <MemoryRouter>
        <SideNav />
      </MemoryRouter>,
    );
    expect(screen.getByTestId("user-email")).toHaveTextContent(USER.email);
  });

  it("renders no identity text when user is null (no crash)", () => {
    useAuthStore.setState({ token: null, user: null });
    render(
      <MemoryRouter>
        <SideNav />
      </MemoryRouter>,
    );
    expect(screen.queryByTestId("user-email")).toBeNull();
    expect(screen.getByTestId("logout-button")).toBeInTheDocument();
  });

  it("collapse toggle hides the brand name and user email", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(
      <MemoryRouter>
        <SideNav />
      </MemoryRouter>,
    );
    expect(screen.getByTestId("user-email")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("nav-collapse-toggle"));
    expect(screen.queryByTestId("user-email")).toBeNull();
    // Nav links remain (icon-only) so navigation still works when collapsed.
    expect(screen.getByTestId("nav-tasks")).toBeInTheDocument();
  });

  it("logout clears the session and gating routes /tasks → /login", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(<GatedTree initial="/tasks" />);
    expect(screen.getByTestId("tasks-stub")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("logout-button"));

    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
    await waitFor(() =>
      expect(screen.getByTestId("login-stub")).toBeInTheDocument(),
    );
  });
});
