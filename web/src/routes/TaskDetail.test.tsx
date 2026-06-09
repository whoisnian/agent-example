import type { JSX } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
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

  // --- control bar (add-web-control-bar) ---

  function controlEnvelope(action: string, effective: "queued" | "best_effort"): object {
    return {
      code: 0,
      message: "ok",
      data: { accepted: true, action, task_id: "task-1", effective },
      trace_id: "t",
    };
  }

  it("issues a pause control, confirms, and does not flip status optimistically", async () => {
    let posted: { action?: string } | null = null;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
        HttpResponse.json(detailEnvelope(String(params["id"]), "running")),
      ),
      http.post("http://localhost/api/v1/tasks/:id/control", async ({ request }) => {
        posted = (await request.json()) as { action?: string };
        return HttpResponse.json(controlEnvelope("pause", "queued"), { status: 202 });
      }),
    );
    render(wrap("task-1"));
    const pause = await screen.findByTestId("control-pause");
    await userEvent.click(pause);

    await waitFor(() => expect(posted).toEqual({ action: "pause" }));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "success" && t.message.includes("pause"))).toBe(true);
    });
    // Status must remain the server-reported value — never optimistically "paused".
    // The header badge is the first status-badge in DOM (version nodes render their own).
    expect(screen.getAllByTestId("status-badge")[0]).toHaveAttribute("data-status", "running");
  });

  it("surfaces a 409 invalid_state with the server message and does not retry", async () => {
    let posts = 0;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
        HttpResponse.json(detailEnvelope(String(params["id"]), "running")),
      ),
      http.post("http://localhost/api/v1/tasks/:id/control", () => {
        posts += 1;
        return HttpResponse.json(
          {
            code: "invalid_state",
            message: 'cannot pause task in status "paused"',
            data: null,
            trace_id: "t",
          },
          { status: 409 },
        );
      }),
    );
    render(wrap("task-1"));
    await userEvent.click(await screen.findByTestId("control-pause"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "warning" && t.message.includes("paused"))).toBe(true);
    });
    // mutations.retry is false → exactly one POST.
    expect(posts).toBe(1);
  });

  it("flags a best_effort cancel as possibly not-yet-effective", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
        HttpResponse.json(detailEnvelope(String(params["id"]), "pending")),
      ),
      http.post("http://localhost/api/v1/tasks/:id/control", () =>
        HttpResponse.json(controlEnvelope("cancel", "best_effort"), { status: 202 }),
      ),
    );
    render(wrap("task-1"));
    await userEvent.click(await screen.findByTestId("control-cancel"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "info" && /claimed/i.test(t.message))).toBe(true);
    });
  });

  it("surfaces a generic error toast on an unexpected control error", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
        HttpResponse.json(detailEnvelope(String(params["id"]), "running")),
      ),
      http.post("http://localhost/api/v1/tasks/:id/control", () =>
        HttpResponse.json(
          { code: "internal_error", message: "boom", data: null, trace_id: "t" },
          { status: 500 },
        ),
      ),
    );
    render(wrap("task-1"));
    await userEvent.click(await screen.findByTestId("control-pause"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "error" && t.message.includes("boom"))).toBe(true);
    });
  });

  // --- rollback (add-web-rollback-entry) ---

  // A non-current terminal sibling so the version tree offers a rollback row.
  function versionsEnvelope(): object {
    return {
      code: 0,
      message: "ok",
      data: {
        items: [
          versionNodeFixture("ver-1", null, 1, "succeeded"),
          versionNodeFixture("ver-2", "ver-1", 2, "succeeded"),
        ],
      },
      trace_id: "t",
    };
  }

  it("branch rollback posts the target + mode and confirms on success", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id/versions", () =>
        HttpResponse.json(versionsEnvelope()),
      ),
      http.post("http://localhost/api/v1/tasks/:id/rollback", async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            code: 0,
            message: "ok",
            data: { version_id: "ver-3", version_no: 3, status: "pending" },
            trace_id: "t",
          },
          { status: 201 },
        );
      }),
    );
    render(wrap("task-1"));
    const row = (await screen.findAllByTestId("version-node"))[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    await userEvent.click(within(row).getByTestId("rollback-branch"));
    await userEvent.type(within(row).getByTestId("rollback-prompt"), "go back");
    await userEvent.click(within(row).getByTestId("rollback-submit"));
    await waitFor(() =>
      expect(body).toEqual({ target_version_id: "ver-2", mode: "branch", prompt: "go back" }),
    );
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "success" && t.message.includes("branch"))).toBe(true);
    });
  });

  it("switch rollback posts a pointer move (no prompt) and confirms", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id/versions", () =>
        HttpResponse.json(versionsEnvelope()),
      ),
      http.post("http://localhost/api/v1/tasks/:id/rollback", async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            code: 0,
            message: "ok",
            data: { current_version_id: "ver-2", version_no: 2, status: "succeeded" },
            trace_id: "t",
          },
          { status: 200 },
        );
      }),
    );
    render(wrap("task-1"));
    const row = (await screen.findAllByTestId("version-node"))[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    await userEvent.click(within(row).getByTestId("rollback-switch"));
    await waitFor(() => expect(body).toEqual({ target_version_id: "ver-2", mode: "switch" }));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "success" && t.message.includes("switch"))).toBe(true);
    });
  });

  it("surfaces a 409 active_version_exists naming the active version, and does not retry", async () => {
    let posts = 0;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id/versions", () =>
        HttpResponse.json(versionsEnvelope()),
      ),
      http.post("http://localhost/api/v1/tasks/:id/rollback", () => {
        posts += 1;
        return HttpResponse.json(
          {
            code: "active_version_exists",
            message: "task has an active version",
            data: { active_version_id: "ver-9", active_version_status: "running" },
            trace_id: "t",
          },
          { status: 409 },
        );
      }),
    );
    render(wrap("task-1"));
    const row = (await screen.findAllByTestId("version-node"))[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    await userEvent.click(within(row).getByTestId("rollback-switch"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "warning" && t.message.includes("ver-9"))).toBe(true);
    });
    expect(posts).toBe(1);
  });

  it("surfaces a 409 invalid_state on switch as a warning", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id/versions", () =>
        HttpResponse.json(versionsEnvelope()),
      ),
      http.post("http://localhost/api/v1/tasks/:id/rollback", () =>
        HttpResponse.json(
          {
            code: "invalid_state",
            message: "cannot switch to a non-terminal version",
            data: null,
            trace_id: "t",
          },
          { status: 409 },
        ),
      ),
    );
    render(wrap("task-1"));
    const row = (await screen.findAllByTestId("version-node"))[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    await userEvent.click(within(row).getByTestId("rollback-switch"));
    await waitFor(() => {
      const toasts = useUiStore.getState().toasts;
      expect(toasts.some((t) => t.level === "warning" && t.message.includes("non-terminal"))).toBe(
        true,
      );
    });
  });

  // --- cost panel (add-web-cost-views) ---

  it("renders the cost panel token breakdown from /tasks/{id}/cost", async () => {
    render(wrap("task-1"));
    const panel = await screen.findByTestId("task-cost-panel");
    // Default fixture total is 1.72000000 → "$1.7200".
    expect(within(panel).getByTestId("token-bar-amount")).toHaveTextContent("$1.7200");
  });

  it("renders an all-zero panel for a zero-cost task (not an error)", async () => {
    server.use(
      http.get("http://localhost/api/v1/tasks/:id/cost", ({ params }) =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: { task_id: String(params["id"]), total: zeroCost(), by_version: [] },
          trace_id: "t",
        }),
      ),
    );
    render(wrap("task-1"));
    const panel = await screen.findByTestId("task-cost-panel");
    expect(within(panel).getByTestId("token-bar-amount")).toHaveTextContent("$0.0000");
  });

  it("shows the inline badge and the cost panel together", async () => {
    render(wrap("task-1"));
    const page = await screen.findByTestId("task-detail-page");
    // The header badge (plus per-version badges in the tree) and the panel coexist.
    expect(screen.getAllByTestId("cost-badge").length).toBeGreaterThan(0);
    const panel = await within(page).findByTestId("task-cost-panel");
    expect(panel).toBeInTheDocument();
  });

  it("re-derives the bar to all-disabled after the task settles to a terminal status", async () => {
    let gets = 0;
    server.use(
      http.get("http://localhost/api/v1/tasks/:id", ({ params }) => {
        gets += 1;
        // First render: running (cancel enabled). After the control settles, the
        // onSettled invalidation refetches and the worker has finished → succeeded.
        const status = gets <= 1 ? "running" : "succeeded";
        return HttpResponse.json(detailEnvelope(String(params["id"]), status));
      }),
      http.post("http://localhost/api/v1/tasks/:id/control", () =>
        HttpResponse.json(controlEnvelope("cancel", "queued"), { status: 202 }),
      ),
    );
    render(wrap("task-1"));
    const cancel = await screen.findByTestId("control-cancel");
    expect(cancel).toBeEnabled();
    await userEvent.click(cancel);
    // The onSettled refetch returns succeeded → all controls disabled.
    await waitFor(() => expect(screen.getByTestId("control-cancel")).toBeDisabled());
    expect(screen.getByTestId("control-pause")).toBeDisabled();
    expect(screen.getByTestId("control-resume")).toBeDisabled();
  });
});
