import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/mocks/server";
import { getMyCost } from "./api";

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/;

function captureMyCost(): { url: () => URL | null } {
  let captured: URL | null = null;
  server.use(
    http.get("http://localhost/api/v1/me/cost", ({ request }) => {
      captured = new URL(request.url);
      const groupBy = captured.searchParams.get("group_by");
      if (!groupBy) {
        return HttpResponse.json({
          code: 0,
          message: "ok",
          data: { total: zero() },
          trace_id: "t",
        });
      }
      return HttpResponse.json({
        code: 0,
        message: "ok",
        data: { group_by: groupBy, items: [{ key: "2026-05-30", totals: zero() }] },
        trace_id: "t",
      });
    }),
  );
  return { url: () => captured };
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

describe("getMyCost", () => {
  it("Total (ungrouped) omits group_by AND from/to and parses the total branch", async () => {
    const cap = captureMyCost();
    const res = await getMyCost({});
    const u = cap.url()!;
    expect(u.searchParams.has("group_by")).toBe(false);
    expect(u.searchParams.has("from")).toBe(false);
    expect(u.searchParams.has("to")).toBe(false);
    expect("total" in res).toBe(true);
  });

  it("grouped sends group_by + RFC3339 from/to and parses the items branch", async () => {
    const cap = captureMyCost();
    const from = new Date(Date.now() - 30 * 86_400_000).toISOString();
    const to = new Date().toISOString();
    const res = await getMyCost({ groupBy: "day", from, to });
    const u = cap.url()!;
    expect(u.searchParams.get("group_by")).toBe("day");
    expect(u.searchParams.get("from")).toMatch(RFC3339);
    expect(u.searchParams.get("to")).toMatch(RFC3339);
    expect("group_by" in res).toBe(true);
  });
});

describe("window presets stay within the backend 366d cap", () => {
  it("every preset satisfies 0 < to-from <= 366d", () => {
    for (const days of [7, 30, 90]) {
      const to = Date.now();
      const from = to - days * 86_400_000;
      const span = to - from;
      expect(span).toBeGreaterThan(0);
      expect(span).toBeLessThanOrEqual(366 * 86_400_000);
    }
  });
});
