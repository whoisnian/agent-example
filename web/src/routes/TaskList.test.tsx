import type { JSX, ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { MemoryRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
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
});
