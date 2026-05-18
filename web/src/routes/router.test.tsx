import type { JSX } from "react";
import { describe, expect, it, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { useAuthStore } from "@/features/auth/store";
import { RequireAuth } from "@/routes/require-auth";
import { RootLayout } from "@/routes/root-layout";
import { TaskListPlaceholder } from "@/routes/placeholders/TaskListPlaceholder";
import { TaskDetailPlaceholder } from "@/routes/placeholders/TaskDetailPlaceholder";
import { CostDashboardPlaceholder } from "@/routes/placeholders/CostDashboardPlaceholder";
import { SettingsPlaceholder } from "@/routes/placeholders/SettingsPlaceholder";
import { LoginPlaceholder } from "@/routes/placeholders/LoginPlaceholder";
import { NotFoundPlaceholder } from "@/routes/placeholders/NotFoundPlaceholder";

/**
 * The production app uses `createBrowserRouter` (data router). The data router
 * builds an internal `Request` object on every navigation, which trips the
 * jsdom × undici AbortSignal interop bug. We assemble the same route tree with
 * the legacy `<Routes>` API for tests — same components, same paths, no Request
 * construction — and verify route → component resolution.
 */
function TestApp({ path }: { path: string }): JSX.Element {
  return (
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/login" element={<LoginPlaceholder />} />
        <Route
          path="/"
          element={
            <RequireAuth>
              <RootLayout />
            </RequireAuth>
          }
        >
          <Route index element={<Navigate to="/tasks" replace />} />
          <Route path="tasks" element={<TaskListPlaceholder />} />
          <Route path="tasks/:id" element={<TaskDetailPlaceholder />} />
          <Route path="cost" element={<CostDashboardPlaceholder />} />
          <Route path="settings" element={<SettingsPlaceholder />} />
        </Route>
        <Route path="*" element={<NotFoundPlaceholder />} />
      </Routes>
    </MemoryRouter>
  );
}

function renderAt(path: string): void {
  render(<TestApp path={path} />);
}

describe("router skeleton", () => {
  beforeEach(() => {
    window.localStorage.clear();
    useAuthStore.setState({ token: null });
  });

  it("redirects unauthenticated /tasks to /login", () => {
    renderAt("/tasks");
    expect(screen.getByTestId("placeholder-login")).toBeInTheDocument();
  });

  it("renders the shell + TaskList placeholder when authenticated", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/tasks");
    expect(screen.getByTestId("top-bar")).toBeInTheDocument();
    expect(screen.getByTestId("side-nav")).toBeInTheDocument();
    expect(screen.getByTestId("placeholder-tasks")).toBeInTheDocument();
  });

  it("parses :id param on /tasks/:id", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/tasks/abc-123");
    expect(screen.getByTestId("task-id")).toHaveTextContent("abc-123");
  });

  it("redirects / to /tasks", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/");
    expect(screen.getByTestId("placeholder-tasks")).toBeInTheDocument();
  });

  it("renders NotFound for unknown routes", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/does-not-exist");
    expect(screen.getByTestId("placeholder-not-found")).toBeInTheDocument();
  });
});
