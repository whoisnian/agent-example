import type { JSX } from "react";
import { describe, expect, it, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { server } from "@/test/mocks/server";
import { createQueryClient } from "@/services/query-client";
import { RequireAuth } from "@/routes/require-auth";
import { RootLayout } from "@/routes/root-layout";
import { useAuthStore } from "@/features/auth/store";
import { useUiStore } from "@/features/ui/store";
import { SideNav } from "@/components/layout/SideNav";

const USER = { id: "u1", tenant_id: "t1", email: "dev@example.com" } as const;

/** SideNav consumes useTasksQuery (Recents), so every render needs a QueryClient. */
function renderNav(initial = "/", opts?: { retry?: boolean }): void {
  const client = createQueryClient();
  if (opts?.retry === false) {
    // Error-path tests skip the production retry/backoff to settle fast.
    const defaults = client.getDefaultOptions();
    client.setDefaultOptions({ ...defaults, queries: { ...defaults.queries, retry: false } });
  }
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initial]}>
        <SideNav />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

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
            <Route path="tasks/new" element={<div data-testid="task-create-stub" />} />
            <Route path="cost" element={<div data-testid="cost-stub" />} />
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
    useUiStore.setState({ toasts: [] });
  });

  it("shows the logged-in user's email", () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav();
    expect(screen.getByTestId("user-email")).toHaveTextContent(USER.email);
  });

  it("renders no identity text when user is null (no crash)", () => {
    useAuthStore.setState({ token: null, user: null });
    renderNav();
    expect(screen.queryByTestId("user-email")).toBeNull();
    expect(screen.queryByTestId("user-avatar")).toBeNull();
    expect(screen.getByTestId("user-area")).toBeInTheDocument();
  });

  it("renders no collapse toggle", () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav();
    expect(screen.queryByTestId("nav-collapse-toggle")).toBeNull();
  });

  it("renders an avatar derived from the user's email initial", () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav();
    expect(screen.getByTestId("user-avatar")).toHaveTextContent("d");
  });

  it("user-area menu hosts Tasks / Cost / Settings / Logout", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav();
    expect(screen.queryByTestId("nav-tasks")).toBeNull();

    await userEvent.click(screen.getByTestId("user-area"));

    expect(await screen.findByTestId("nav-tasks")).toBeInTheDocument();
    expect(screen.getByTestId("nav-cost")).toBeInTheDocument();
    expect(screen.getByTestId("nav-settings")).toBeInTheDocument();
    expect(screen.getByTestId("logout-button")).toBeInTheDocument();
  });

  it("menu entry navigates and closes the menu", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(<GatedTree initial="/tasks" />);
    expect(screen.getByTestId("tasks-stub")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("user-area"));
    await userEvent.click(await screen.findByTestId("nav-cost"));

    expect(await screen.findByTestId("cost-stub")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByTestId("nav-cost")).toBeNull());
  });

  it("menu marks the active route entry", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav("/cost");

    await userEvent.click(screen.getByTestId("user-area"));

    expect(await screen.findByTestId("nav-cost")).toHaveClass("bg-accent");
    expect(screen.getByTestId("nav-tasks")).not.toHaveClass("bg-accent");
  });

  it("New task action navigates to /tasks/new", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(<GatedTree initial="/tasks" />);
    expect(screen.getByTestId("tasks-stub")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("nav-new-task"));

    expect(screen.getByTestId("task-create-stub")).toBeInTheDocument();
  });

  it("Recents lists tasks from the list read and links to detail", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav();
    const item = await screen.findByTestId("recent-task-item");
    expect(item).toHaveTextContent("First task");
    expect(item).toHaveAttribute("href", "/tasks/task-1");
  });

  it("Recents highlights the currently open task", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    renderNav("/tasks/task-1");
    const item = await screen.findByTestId("recent-task-item");
    expect(item).toHaveAttribute("aria-current", "page");
  });

  it("Recents shows an empty placeholder for a zero-task list", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    server.use(
      http.get("http://localhost/api/v1/tasks", () =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: { items: [], page: 1, page_size: 8, total: 0 },
          trace_id: "trace-empty",
        }),
      ),
    );
    renderNav();
    expect(await screen.findByTestId("recent-tasks-empty")).toBeInTheDocument();
  });

  it("Recents stays quiet on failure (inline placeholder, no toast)", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    server.use(
      http.get("http://localhost/api/v1/tasks", () =>
        HttpResponse.json(
          { code: "internal_error", message: "boom", data: null, trace_id: "trace-err" },
          { status: 500 },
        ),
      ),
    );
    renderNav("/", { retry: false });
    expect(await screen.findByTestId("recent-tasks-error")).toBeInTheDocument();
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("logout (in the menu) clears the session and gating routes /tasks → /login", async () => {
    useAuthStore.setState({ token: "t", user: USER });
    render(<GatedTree initial="/tasks" />);
    expect(screen.getByTestId("tasks-stub")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("user-area"));
    await userEvent.click(await screen.findByTestId("logout-button"));

    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
    await waitFor(() => expect(screen.getByTestId("login-stub")).toBeInTheDocument());
  });
});
