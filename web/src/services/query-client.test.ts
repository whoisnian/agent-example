// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/mocks/server";
import { createQueryClient } from "@/services/query-client";
import { useUiStore } from "@/features/ui/store";

const BASE = "http://localhost";

describe("queryClient defaults", () => {
  beforeEach(() => {
    useUiStore.setState({ toasts: [] });
  });
  afterEach(() => {
    useUiStore.setState({ toasts: [] });
  });

  it("does not retry on 409", async () => {
    let calls = 0;
    server.use(
      http.get(`${BASE}/api/v1/__scaffold/conflict`, () => {
        calls += 1;
        return HttpResponse.json(
          { code: "active_version_exists", message: "x", data: null, trace_id: "t" },
          { status: 409 },
        );
      }),
    );
    const qc = createQueryClient();
    await qc
      .fetchQuery({
        queryKey: ["conflict"],
        queryFn: async () => {
          const r = await fetch(`${BASE}/api/v1/__scaffold/conflict`);
          if (r.status === 409) {
            const body = (await r.json()) as { code: string; message: string; trace_id: string };
            const { ApiError } = await import("@/services/http");
            throw new ApiError({
              code: body.code,
              message: body.message,
              traceId: body.trace_id,
              status: 409,
            });
          }
          return r.json();
        },
      })
      .catch(() => undefined);
    expect(calls).toBe(1); // No retry on 409.
  });

  it("silent queries do not emit a toast", async () => {
    server.use(
      http.get(`${BASE}/api/v1/__scaffold/silentfail`, () =>
        HttpResponse.json(
          { code: "boom", message: "should not toast", data: null, trace_id: "t" },
          { status: 500 },
        ),
      ),
    );
    const qc = createQueryClient();
    await qc
      .fetchQuery({
        queryKey: ["silent"],
        meta: { silent: true },
        queryFn: async () => {
          const { apiFetch } = await import("@/services/http");
          return apiFetch(`${BASE}/api/v1/__scaffold/silentfail`, { toastOnError: false });
        },
      })
      .catch(() => undefined);
    expect(useUiStore.getState().toasts).toHaveLength(0);
  });
});
