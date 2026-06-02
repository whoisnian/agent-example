import type { JSX } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { createQueryClient } from "@/services/query-client";
import { server } from "@/test/mocks/server";
import { useUiStore } from "@/features/ui/store";
import { CostDashboard } from "@/routes/CostDashboard";

function wrap(): JSX.Element {
  return (
    <QueryClientProvider client={createQueryClient()}>
      <CostDashboard />
    </QueryClientProvider>
  );
}

afterEach(() => {
  useUiStore.setState({ toasts: [] });
});

describe("CostDashboard", () => {
  it("defaults to Total: omits group_by AND from/to, hides the window control", async () => {
    let captured: URL | null = null;
    server.use(
      http.get("http://localhost/api/v1/me/cost", ({ request }) => {
        captured = new URL(request.url);
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { total: nonZero("0.42000000") },
          trace_id: "t",
        });
      }),
    );
    render(wrap());

    expect(await screen.findByTestId("token-bar")).toBeInTheDocument();
    const u = captured!;
    expect(u.searchParams.has("group_by")).toBe(false);
    expect(u.searchParams.has("from")).toBe(false);
    expect(u.searchParams.has("to")).toBe(false);
    expect(screen.queryByTestId("cost-window-select")).not.toBeInTheDocument();
    expect(screen.getByTestId("token-bar-amount")).toHaveTextContent("$0.4200");
  });

  it("switching to By day reveals the window and re-queries with group_by + window", async () => {
    const seen: URL[] = [];
    server.use(
      http.get("http://localhost/api/v1/me/cost", ({ request }) => {
        const u = new URL(request.url);
        seen.push(u);
        if (!u.searchParams.get("group_by")) {
          return HttpResponse.json({
            code: 0,
            message: "ok",
            data: { total: nonZero("0.42000000") },
            trace_id: "t",
          });
        }
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            group_by: "day",
            items: [
              { key: "2026-05-29", totals: nonZero("0.10000000") },
              { key: "2026-05-30", totals: nonZero("0.32000000") },
            ],
          },
          trace_id: "t",
        });
      }),
    );
    render(wrap());
    await screen.findByTestId("token-bar");

    await userEvent.selectOptions(screen.getByTestId("cost-group-select"), "day");

    expect(await screen.findByTestId("cost-window-select")).toBeInTheDocument();
    const list = await screen.findByTestId("cost-group-list");
    const rows = within(list).getAllByTestId("cost-group-row");
    // Server order preserved (key ascending), no client re-sort.
    expect(rows.map((r) => r.getAttribute("data-key"))).toEqual(["2026-05-29", "2026-05-30"]);

    const grouped = seen.find((u) => u.searchParams.get("group_by") === "day")!;
    expect(grouped.searchParams.has("from")).toBe(true);
    expect(grouped.searchParams.has("to")).toBe(true);
    // Window total is the exact decimal sum of the items.
    expect(screen.getByTestId("cost-group-total")).toHaveTextContent("$0.4200");
  });

  it("renders the model 'other' bucket as an ordinary row", async () => {
    server.use(
      http.get("http://localhost/api/v1/me/cost", ({ request }) => {
        const groupBy = new URL(request.url).searchParams.get("group_by");
        if (!groupBy) {
          return HttpResponse.json({
            code: 0,
            message: "ok",
            data: { total: nonZero("0.42000000") },
            trace_id: "t",
          });
        }
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: {
            group_by: "model",
            items: [
              { key: "claude-opus-4-7", totals: nonZero("0.30000000") },
              { key: "other", totals: nonZero("0.12000000") },
            ],
          },
          trace_id: "t",
        });
      }),
    );
    render(wrap());
    await screen.findByTestId("token-bar");
    await userEvent.selectOptions(screen.getByTestId("cost-group-select"), "model");

    const list = await screen.findByTestId("cost-group-list");
    const keys = within(list)
      .getAllByTestId("cost-group-row")
      .map((r) => r.getAttribute("data-key"));
    expect(keys).toContain("other");
  });

  it("renders the zero/empty state when there is no spend (not a 404)", async () => {
    server.use(
      http.get("http://localhost/api/v1/me/cost", () =>
        HttpResponse.json({
          code: 0,
          message: "ok",
          data: { total: zero() },
          trace_id: "t",
        }),
      ),
    );
    render(wrap());
    expect(await screen.findByTestId("cost-empty")).toBeInTheDocument();
    expect(screen.getByTestId("token-bar-amount")).toHaveTextContent("$0.0000");
  });

  it("shows a single in-page error on 5xx with no extra toast", async () => {
    server.use(
      http.get("http://localhost/api/v1/me/cost", () =>
        HttpResponse.json(
          { code: "internal_error", message: "boom", data: null, trace_id: "t" },
          { status: 500 },
        ),
      ),
    );
    render(wrap());
    expect(await screen.findByTestId("cost-error")).toBeInTheDocument();
    // Neither the transport toast (toastOnError:false) nor the cache toast
    // (meta.silent) should have fired.
    await waitFor(() => expect(useUiStore.getState().toasts).toHaveLength(0));
  });
});

function nonZero(amount: string): Record<string, unknown> {
  return {
    amount_usd: amount,
    input_tokens: 100,
    output_tokens: 50,
    cached_tokens: 10,
    tool_calls: 1,
    wall_time_ms: 1000,
  };
}

function zero(): Record<string, unknown> {
  return {
    amount_usd: "0.00000000",
    input_tokens: 0,
    output_tokens: 0,
    cached_tokens: 0,
    tool_calls: 0,
    wall_time_ms: 0,
  };
}
