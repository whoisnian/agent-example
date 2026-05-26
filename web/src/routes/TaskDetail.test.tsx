import type { JSX } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { __resetRealtimeForTests } from "@/services/ws";
import { useUiStore } from "@/features/ui/store";
import { taskInfoFixture, versionNodeFixture, zeroCost } from "@/test/mocks/handlers";
import { TaskDetail } from "@/routes/TaskDetail";

function wrap(id: string): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[`/tasks/${id}`]}>
        <Routes>
          <Route path="/tasks/:id" element={<TaskDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

function detailEnvelope(id: string, status: string): object {
  return {
    code: 0,
    message: "ok",
    data: {
      task: taskInfoFixture(id, status),
      current_version: versionNodeFixture("ver-1", null, 1, status),
      cost: zeroCost(),
    },
    trace_id: "t",
  };
}

afterEach(() => {
  __resetRealtimeForTests?.();
  useUiStore.setState({ toasts: [] });
});

describe("TaskDetail", () => {
  it("renders header, version tree, and event log", async () => {
    render(wrap("task-1"));
    expect(await screen.findByTestId("task-detail-page")).toBeInTheDocument();
    expect(await screen.findByTestId("version-tree")).toBeInTheDocument();
    expect(await screen.findByTestId("event-log")).toBeInTheDocument();
  });

  it("disables Iterate while the task is active", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
        HttpResponse.json(detailEnvelope(String(params["id"]), "running")),
      ),
    );
    render(wrap("task-1"));
    const btn = await screen.findByTestId("iterate-button");
    expect(btn).toBeDisabled();
  });

  it("enables Iterate in a terminal state and submits", async () => {
    let iterated = false;
    server.use(
      http.post("http://localhost/api/v1/tasks/:id/iterate", () => {
        iterated = true;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { version_id: "ver-2", version_no: 2, status: "pending" },
          trace_id: "t",
        });
      }),
    );
    render(wrap("task-1"));
    const btn = await screen.findByTestId("iterate-button");
    expect(btn).toBeEnabled();
    await userEvent.click(btn);
    await userEvent.type(screen.getByTestId("iterate-prompt"), "add a page");
    await userEvent.click(screen.getByTestId("iterate-submit"));
    await waitFor(() => expect(iterated).toBe(true));
  });

  it("surfaces a 409 conflict naming the active version", async () => {
    server.use(
      http.post("http://localhost/api/v1/tasks/:id/iterate", () =>
        HttpResponse.json(
          {
            code: "active_version_exists",
            message: "task has an active version",
            data: { active_version_id: "ver-9", active_version_status: "running" },
            trace_id: "t",
          },
          { status: 409 },
        ),
      ),
    );
    render(wrap("task-1"));
    await userEvent.click(await screen.findByTestId("iterate-button"));
    await userEvent.type(screen.getByTestId("iterate-prompt"), "x");
    await userEvent.click(screen.getByTestId("iterate-submit"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.message.includes("ver-9"))).toBe(true);
    });
  });

  it("renders a not-found state on 404", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", () =>
        HttpResponse.json(
          { code: "task_not_found", message: "task not found", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap("missing"));
    expect(await screen.findByTestId("task-not-found")).toBeInTheDocument();
  });
});
