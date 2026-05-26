import type { JSX, ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { MemoryRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { TaskCreate } from "@/routes/TaskCreate";

const navigateMock = vi.fn();
vi.mock("react-router-dom", async (orig) => {
  const actual = await orig<typeof import("react-router-dom")>();
  return { ...actual, useNavigate: () => navigateMock };
});

function wrap(ui: ReactNode): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe("TaskCreate", () => {
  it("blocks submit when params is not valid JSON", async () => {
    let posted = false;
    server.use(
      http.post("http://localhost/api/v1/tasks", () => {
        posted = true;
        return HttpResponse.json({ code: 0, message: "ok", data: {}, trace_id: "t" });
      }),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("title-input"), "T");
    await userEvent.type(screen.getByTestId("prompt-input"), "P");
    // `{{` types a literal `{` (userEvent treats `{` as a key descriptor).
    await userEvent.type(screen.getByTestId("params-input"), "{{not json");
    await userEvent.click(screen.getByTestId("submit-button"));
    expect(screen.getByText("params must be valid JSON")).toBeInTheDocument();
    expect(posted).toBe(false);
  });

  it("navigates to the new task on success", async () => {
    navigateMock.mockClear();
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("title-input"), "My task");
    await userEvent.type(screen.getByTestId("prompt-input"), "do it");
    await userEvent.click(screen.getByTestId("submit-button"));
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/tasks/task-new"));
  });

  it("shows an inline field error on 400 invalid_input", async () => {
    server.use(
      http.post("http://localhost/api/v1/tasks", () =>
        HttpResponse.json(
          {
            code: "invalid_input",
            message: "invalid_input: title: must not be empty",
            data: { field: "title", reason: "must not be empty" },
            trace_id: "t",
          },
          { status: 400 },
        ),
      ),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "p");
    await userEvent.click(screen.getByTestId("submit-button"));
    expect(await screen.findByText("must not be empty")).toBeInTheDocument();
  });
});
