import type { JSX, ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { MemoryRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { taskSummaryFixture } from "@/test/mocks/handlers";
import { TaskList } from "@/routes/TaskList";

function wrap(ui: ReactNode): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe("TaskList", () => {
  it("renders rows from the list endpoint", async () => {
    render(wrap(<TaskList />));
    expect(await screen.findByTestId("task-list-table")).toBeInTheDocument();
    expect(screen.getByText("First task")).toBeInTheDocument();
    expect(screen.getByTestId("status-badge")).toHaveAttribute("data-status", "succeeded");
  });

  it("shows an empty state when there are no tasks", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks", () =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: { items: [], page: 1, page_size: 20, total: 0 },
          trace_id: "t",
        }),
      ),
    );
    render(wrap(<TaskList />));
    expect(await screen.findByTestId("task-list-empty")).toBeInTheDocument();
  });

  it("sends the status filter as a query param", async () => {
    let lastStatus: string | null = "unset";
    server.use(
      http.get("http://localhost/api/v1/tasks", ({ request }) => {
        lastStatus = new URL(request.url).searchParams.get("status");
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { items: [], page: 1, page_size: 20, total: 0 },
          trace_id: "t",
        });
      }),
    );
    render(wrap(<TaskList />));
    await screen.findByTestId("task-list-empty");
    await userEvent.selectOptions(screen.getByTestId("status-filter"), "running");
    await waitFor(() => expect(lastStatus).toBe("running"));
  });

  it("deletes a task only after confirmation, then refreshes the list", async () => {
    let listCalls = 0;
    let deleteCalled = false;
    server.use(
      http.get("http://localhost/api/v1/tasks", () => {
        listCalls += 1;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            items: [taskSummaryFixture("task-1", "First task", "succeeded")],
            page: 1,
            page_size: 20,
            total: 1,
          },
          trace_id: "t",
        });
      }),
      http.delete("http://localhost/api/v1/tasks/:id", () => {
        deleteCalled = true;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { deleted: true, task_id: "task-1" },
          trace_id: "t",
        });
      }),
    );
    render(wrap(<TaskList />));
    await screen.findByText("First task");
    const callsBefore = listCalls;

    // Opening the dialog must NOT send the request.
    await userEvent.click(screen.getByTestId("task-delete"));
    expect(await screen.findByTestId("task-delete-dialog")).toBeInTheDocument();
    expect(deleteCalled).toBe(false);

    // Confirm → DELETE sent and the list re-fetches (lists invalidated).
    await userEvent.click(screen.getByTestId("task-delete-confirm"));
    await waitFor(() => expect(deleteCalled).toBe(true));
    await waitFor(() => expect(listCalls).toBeGreaterThan(callsBefore));
  });

  it("disables the delete control for an active task", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks", () =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            items: [taskSummaryFixture("task-1", "Running task", "running")],
            page: 1,
            page_size: 20,
            total: 1,
          },
          trace_id: "t",
        }),
      ),
    );
    render(wrap(<TaskList />));
    await screen.findByText("Running task");
    expect(screen.getByTestId("task-delete")).toBeDisabled();
  });
});
