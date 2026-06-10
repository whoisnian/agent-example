import type { JSX } from "react";
import { describe, expect, it, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore } from "@/features/auth/store";
import { createQueryClient } from "@/services/query-client";
import { RequireAuth } from "@/routes/require-auth";
import { RootLayout } from "@/routes/root-layout";
import { TaskList } from "@/routes/TaskList";
import { TaskCreate } from "@/routes/TaskCreate";
import { TaskDetail } from "@/routes/TaskDetail";
import { CostDashboard } from "@/routes/CostDashboard";
import { SettingsPlaceholder } from "@/routes/placeholders/SettingsPlaceholder";
import { LoginPage } from "@/routes/LoginPage";
import { NotFoundPlaceholder } from "@/routes/placeholders/NotFoundPlaceholder";

/**
 * The production app uses `createBrowserRouter` (data router), which trips a
 * jsdom × undici AbortSignal interop bug under test. We assemble the same route
 * tree with the legacy `<Routes>` API — same components, same paths — and wrap
 * it in a fresh QueryClientProvider since the real pages fetch via React Query.
 * Page data is served by the MSW handlers (test/mocks).
 */
function TestApp({ path }: { path: string }): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <RootLayout />
              </RequireAuth>
            }
          >
            <Route index element={<Navigate to="/tasks/new" replace />} />
            <Route path="tasks" element={<TaskList />} />
            <Route path="tasks/new" element={<TaskCreate />} />
            <Route path="tasks/:id" element={<TaskDetail />} />
            <Route path="cost" element={<CostDashboard />} />
            <Route path="settings" element={<SettingsPlaceholder />} />
          </Route>
          <Route path="*" element={<NotFoundPlaceholder />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
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
    expect(screen.getByTestId("login-page")).toBeInTheDocument();
  });

  it("renders the shell + TaskList page when authenticated", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/tasks");
    expect(screen.getByTestId("side-nav")).toBeInTheDocument();
    expect(screen.getByTestId("preview-column")).toBeInTheDocument();
    expect(screen.getByTestId("task-list-page")).toBeInTheDocument();
  });

  it("renders TaskCreate on /tasks/new", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/tasks/new");
    expect(screen.getByTestId("task-create-page")).toBeInTheDocument();
  });

  it("renders TaskDetail on /tasks/:id", async () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/tasks/abc-123");
    expect(await screen.findByTestId("task-detail-page")).toBeInTheDocument();
  });

  it("redirects / to the chat-style create page", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/");
    expect(screen.getByTestId("task-create-page")).toBeInTheDocument();
  });

  it("renders NotFound for unknown routes", () => {
    useAuthStore.setState({ token: "test" });
    renderAt("/does-not-exist");
    expect(screen.getByTestId("placeholder-not-found")).toBeInTheDocument();
  });
});
