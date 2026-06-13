import type { JSX } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { useUiStore } from "@/features/ui/store";
import type { CostSummary, VersionNode } from "@/features/tasks/types";
import { ConversationTurn, type ConversationTurnProps } from "./ConversationTurn";

function zero(): CostSummary {
  return {
    amount_usd: "0.00000000",
    input_tokens: 0,
    output_tokens: 0,
    cached_tokens: 0,
    tool_calls: 0,
    wall_time_ms: 0,
  };
}

function node(id: string, versionNo: number): VersionNode {
  return {
    id,
    parent_id: null,
    version_no: versionNo,
    status: "succeeded",
    is_active: false,
    artifact_root: null,
    created_at: "2026-05-26T00:00:00Z",
    cost: zero(),
  };
}

function wrap(
  props: Partial<ConversationTurnProps> & { version: VersionNode },
  opts?: { retry?: boolean },
): JSX.Element {
  const client = createQueryClient();
  if (opts?.retry === false) {
    // Error-path tests skip the production retry/backoff to settle fast.
    const defaults = client.getDefaultOptions();
    client.setDefaultOptions({ ...defaults, queries: { ...defaults.queries, retry: false } });
  }
  return (
    <QueryClientProvider client={client}>
      <ol>
        <ConversationTurn isCurrent={false} taskActive={false} {...props} />
      </ol>
    </QueryClientProvider>
  );
}

/** Make the per-turn artifact read return an empty list (section omitted). */
function emptyArtifacts(): void {
  server.use(
    http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
      HttpResponse.json({
        code: 0,
        message: "ok",
        data: { version_id: String(params["id"]), artifacts: [] },
        trace_id: "t",
      }),
    ),
  );
}

afterEach(() => {
  useUiStore.setState({
    toasts: [],
    selectedVersionId: null,
    selectedArtifactId: null,
    previewCollapsed: false,
  });
});

describe("ConversationTurn", () => {
  it("renders the prompt as the user message and the result line", async () => {
    emptyArtifacts();
    render(wrap({ version: node("ver-1", 1), isCurrent: true }));

    // Prompt arrives via the lazy version-detail read (MSW fixture).
    const prompt = await screen.findByTestId("turn-prompt");
    expect(prompt).toHaveTextContent("Prompt for ver-1");
    expect(screen.getByText("v1")).toBeInTheDocument();
    expect(screen.getByTestId("status-badge")).toHaveAttribute("data-status", "succeeded");
    expect(screen.getByTestId("current-marker")).toBeInTheDocument();
    expect(screen.queryByTestId("turn-origin")).toBeNull();
  });

  it("degrades silently when the prompt read fails (no toast, result line intact)", async () => {
    emptyArtifacts();
    server.use(
      http.get("http://localhost/api/v1/versions/:id", () =>
        HttpResponse.json(
          { code: "version_not_found", message: "nope", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap({ version: node("ver-1", 1) }));

    // The 404 is not retried; the turn settles to no prompt text.
    await waitFor(() => expect(screen.queryByTestId("turn-prompt")).toBeNull());
    expect(screen.getByText("v1")).toBeInTheDocument();
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("labels a fork with its origin version", async () => {
    emptyArtifacts();
    render(wrap({ version: node("ver-3", 3), originNo: 1 }));
    expect(screen.getByTestId("turn-origin")).toHaveTextContent("from v1");
  });

  it("omits the artifact section entirely for an empty version", async () => {
    emptyArtifacts();
    render(wrap({ version: node("ver-1", 1) }));
    await waitFor(() => expect(screen.queryByTestId("turn-artifacts-loading")).toBeNull());
    expect(screen.queryByTestId("turn-artifact-card")).toBeNull();
    expect(screen.queryByTestId("turn-artifacts-error")).toBeNull();
  });

  it("aggregates the version's artifacts into one card with a file count", async () => {
    render(wrap({ version: node("ver-1", 1) }));
    // Default MSW fixture lists art-1 / art-2 for any version.
    const card = await screen.findByTestId("turn-artifact-card");
    expect(card).toHaveTextContent("2 files");
    // Exactly one aggregate card (not one per file).
    expect(screen.getAllByTestId("turn-artifact-card")).toHaveLength(1);
  });

  it("shows a quiet inline error when the artifact read fails", async () => {
    // 404 exercises the same quiet error surface without the retry/backoff
    // (the artifacts query skips retry on 404 by design).
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", () =>
        HttpResponse.json(
          { code: "version_not_found", message: "nope", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap({ version: node("ver-1", 1) }, { retry: false }));
    expect(await screen.findByTestId("turn-artifacts-error")).toBeInTheDocument();
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });

  it("activating the card opens the version's files in the preview panel", async () => {
    useUiStore.setState({ previewCollapsed: true });
    render(wrap({ version: node("ver-1", 1) }));

    // Default MSW fixture lists art-1 / art-2; activating selects the first.
    await userEvent.click(await screen.findByTestId("turn-artifact-open"));

    const s = useUiStore.getState();
    expect(s.selectedVersionId).toBe("ver-1");
    expect(s.selectedArtifactId).toBe("art-1");
    expect(s.previewCollapsed).toBe(false);
  });

  it("Download zip re-mints the archive URL and surfaces a single error on failure", async () => {
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts/archive/presign", () =>
        HttpResponse.json(
          { code: "version_not_found", message: "boom", data: null, trace_id: "t" },
          { status: 404 },
        ),
      ),
    );
    render(wrap({ version: node("ver-1", 1) }));
    await userEvent.click(await screen.findByTestId("turn-artifact-download-zip"));
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(1));
  });

  it("renders a historical turn's execution log inline (no collapse) so v1 stays visible", async () => {
    emptyArtifacts();
    server.use(
      http.get("http://localhost/api/v1/versions/:id/events", () =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            items: [
              {
                id: 1,
                version_id: "ver-1",
                run_id: "r",
                seq: 1,
                kind: "summary",
                payload: { summary: "did the thing" },
                created_at: "2026-05-26T00:00:00Z",
              },
            ],
            next_after_id: 1,
          },
          trace_id: "t",
        }),
      ),
    );
    render(wrap({ version: node("ver-1", 1) })); // isCurrent:false → historical

    // The log renders directly, with no collapse toggle to hide it.
    await screen.findByTestId("event-log");
    expect(screen.getByText("did the thing")).toBeInTheDocument();
    expect(screen.queryByTestId("execution-toggle")).toBeNull();
  });

  it("offers rollback on non-current turns only", async () => {
    emptyArtifacts();
    const { rerender } = render(
      wrap({ version: node("ver-2", 2), onRollback: vi.fn() }),
    );
    expect(screen.getByTestId("rollback-control")).toBeInTheDocument();

    rerender(wrap({ version: node("ver-2", 2), isCurrent: true, onRollback: vi.fn() }));
    expect(screen.queryByTestId("rollback-control")).toBeNull();
  });

  it("disables both rollback actions while the task is active", async () => {
    emptyArtifacts();
    render(wrap({ version: node("ver-2", 2), taskActive: true, onRollback: vi.fn() }));
    await userEvent.click(screen.getByTestId("rollback-button"));
    expect(screen.getByTestId("rollback-switch")).toBeDisabled();
    expect(screen.getByTestId("rollback-branch")).toBeDisabled();
  });

  it("disables switch (only) on an active target version", async () => {
    emptyArtifacts();
    const active = { ...node("ver-2", 2), status: "running", is_active: true };
    render(wrap({ version: active, onRollback: vi.fn() }));
    await userEvent.click(screen.getByTestId("rollback-button"));
    expect(screen.getByTestId("rollback-switch")).toBeDisabled();
    expect(screen.getByTestId("rollback-branch")).toBeEnabled();
  });
});
