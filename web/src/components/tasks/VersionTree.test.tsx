import type { JSX } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { useUiStore } from "@/features/ui/store";
import type { CostSummary, VersionNode } from "@/features/tasks/types";
import { VersionTree } from "./VersionTree";

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

const versions = [node("ver-1", 1), node("ver-2", 2)];

function wrap(): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <VersionTree versions={versions} currentVersionId="ver-1" />
    </QueryClientProvider>
  );
}

afterEach(() => {
  useUiStore.setState({ toasts: [] });
});

describe("VersionTree artifact expansion", () => {
  it("keeps existing badges/current-marker and issues NO artifact request while collapsed", async () => {
    const requested: string[] = [];
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) => {
        requested.push(String(params["id"]));
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { version_id: String(params["id"]), artifacts: [] },
          trace_id: "t",
        });
      }),
    );
    render(wrap());

    expect(screen.getAllByTestId("version-node")).toHaveLength(2);
    expect(screen.getByTestId("current-marker")).toBeInTheDocument();
    // Nothing expanded → no list query fired.
    await waitFor(() => expect(requested).toHaveLength(0));
    expect(screen.queryByTestId("artifact-list")).not.toBeInTheDocument();
  });

  it("offers a rollback control on non-current rows only when onRollback is given", () => {
    render(
      <QueryClientProvider client={createQueryClient()}>
        <VersionTree versions={versions} currentVersionId="ver-1" onRollback={vi.fn()} />
      </QueryClientProvider>,
    );
    const rows = screen.getAllByTestId("version-node");
    // ver-1 is current → no rollback; ver-2 is non-current → rollback offered.
    expect(within(rows[0]!).queryByTestId("rollback-control")).not.toBeInTheDocument();
    expect(within(rows[1]!).getByTestId("rollback-control")).toBeInTheDocument();
  });

  it("renders no rollback control when onRollback is omitted", () => {
    render(wrap());
    expect(screen.queryByTestId("rollback-control")).not.toBeInTheDocument();
  });

  it("disables switch (only) on an active target row", async () => {
    const activeNode = { ...node("ver-2", 2), status: "running", is_active: true };
    render(
      <QueryClientProvider client={createQueryClient()}>
        <VersionTree
          versions={[node("ver-1", 1), activeNode]}
          currentVersionId="ver-1"
          onRollback={vi.fn()}
        />
      </QueryClientProvider>,
    );
    const row = screen.getAllByTestId("version-node")[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    // Task not active, but the target version is active → switch off, branch on.
    expect(within(row).getByTestId("rollback-switch")).toBeDisabled();
    expect(within(row).getByTestId("rollback-branch")).toBeEnabled();
  });

  it("disables both rollback actions while the task is active", async () => {
    render(
      <QueryClientProvider client={createQueryClient()}>
        <VersionTree
          versions={[node("ver-1", 1), node("ver-2", 2)]}
          currentVersionId="ver-1"
          taskActive
          onRollback={vi.fn()}
        />
      </QueryClientProvider>,
    );
    const row = screen.getAllByTestId("version-node")[1]!;
    await userEvent.click(within(row).getByTestId("rollback-button"));
    expect(within(row).getByTestId("rollback-switch")).toBeDisabled();
    expect(within(row).getByTestId("rollback-branch")).toBeDisabled();
  });

  it("expanding one row fires exactly one request for that version, siblings untouched", async () => {
    const requested: string[] = [];
    server.use(
      http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) => {
        requested.push(String(params["id"]));
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { version_id: String(params["id"]), artifacts: [] },
          trace_id: "t",
        });
      }),
    );
    render(wrap());

    const firstRow = screen.getAllByTestId("version-node")[0]!;
    await userEvent.click(within(firstRow).getByTestId("version-expand-toggle"));

    // The expanded row resolves to its (empty) list; only ver-1 was requested.
    expect(await within(firstRow).findByTestId("artifact-list-empty")).toBeInTheDocument();
    await waitFor(() => expect(requested).toEqual(["ver-1"]));
  });
});
