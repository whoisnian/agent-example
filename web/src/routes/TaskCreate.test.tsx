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

describe("TaskCreate (chat composer)", () => {
  it("renders the composer without a title input", () => {
    render(wrap(<TaskCreate />));
    expect(screen.getByTestId("prompt-input")).toBeInTheDocument();
    expect(screen.getAllByTestId("task-type-chip")).toHaveLength(2);
    expect(screen.queryByTestId("title-input")).toBeNull();
    // Advanced fields are tucked away until disclosed.
    expect(screen.queryByTestId("params-input")).toBeNull();
  });

  it("submits without a title and with the selected task-type chip", async () => {
    navigateMock.mockClear();
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post("http://localhost/api/v1/tasks", async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { task_id: "task-new", version_id: "v1", version_no: 1, status: "pending" },
          trace_id: "t",
        });
      }),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "do it");
    await userEvent.click(screen.getByText("research"));
    await userEvent.click(screen.getByTestId("submit-button"));

    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/tasks/task-new"));
    expect(body).not.toBeNull();
    expect(body!["prompt"]).toBe("do it");
    expect(body!["task_type"]).toBe("research");
    expect("title" in body!).toBe(false);
  });

  it("submits via Ctrl+Enter in the prompt", async () => {
    navigateMock.mockClear();
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "do it");
    await userEvent.keyboard("{Control>}{Enter}{/Control}");
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith("/tasks/task-new"));
  });

  it("disables submit while the prompt is empty", () => {
    render(wrap(<TaskCreate />));
    expect(screen.getByTestId("submit-button")).toBeDisabled();
  });

  it("blocks submit when params is not valid JSON", async () => {
    let posted = false;
    server.use(
      http.post("http://localhost/api/v1/tasks", () => {
        posted = true;
        return HttpResponse.json({ code: 0, message: "ok", data: {}, trace_id: "t" });
      }),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "P");
    await userEvent.click(screen.getByTestId("advanced-toggle"));
    // `{{` types a literal `{` (userEvent treats `{` as a key descriptor).
    await userEvent.type(screen.getByTestId("params-input"), "{{not json");
    await userEvent.click(screen.getByTestId("submit-button"));
    expect(screen.getByText("params must be valid JSON")).toBeInTheDocument();
    expect(posted).toBe(false);
    // The advanced panel stays open so the error remains visible.
    expect(screen.getByTestId("advanced-panel")).toBeInTheDocument();
  });

  it("shows an inline field error on 400 invalid_input and keeps the input", async () => {
    server.use(
      http.post("http://localhost/api/v1/tasks", () =>
        HttpResponse.json(
          {
            code: "invalid_input",
            message: "invalid_input: prompt: exceeds 16384 characters",
            data: { field: "prompt", reason: "exceeds 16384 characters" },
            trace_id: "t",
          },
          { status: 400 },
        ),
      ),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "p");
    await userEvent.click(screen.getByTestId("submit-button"));
    expect(await screen.findByText("exceeds 16384 characters")).toBeInTheDocument();
    expect(screen.getByTestId("prompt-input")).toHaveValue("p");
  });

  it("routes an unknown-field 400 to the form-level error", async () => {
    server.use(
      http.post("http://localhost/api/v1/tasks", () =>
        HttpResponse.json(
          {
            code: "invalid_input",
            message: "invalid_input: body: malformed",
            data: { field: "body", reason: "malformed" },
            trace_id: "t",
          },
          { status: 400 },
        ),
      ),
    );
    render(wrap(<TaskCreate />));
    await userEvent.type(screen.getByTestId("prompt-input"), "p");
    await userEvent.click(screen.getByTestId("submit-button"));
    expect(await screen.findByTestId("form-error")).toHaveTextContent("malformed");
  });
});
