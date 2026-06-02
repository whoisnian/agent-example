import { http, HttpResponse, delay } from "msw";

/**
 * MSW HTTP handlers used by unit / integration tests.
 *
 * Routes:
 * - GET /healthz                       → `{status:"ok"}` (NO envelope; matches api `/healthz`)
 * - POST /api/v1/__scaffold/echo       → echoes payload inside the standard envelope
 * - GET  /api/v1/__scaffold/slow       → 200, but holds for 5s (used by timeout tests)
 * - GET  /api/v1/__scaffold/error      → 409 with `code:"active_version_exists"` envelope
 * - GET  /api/v1/__scaffold/unauth     → 401
 */
export const handlers = [
  http.get("http://localhost/healthz", () => HttpResponse.json({ status: "ok" })),

  http.post("http://localhost/api/v1/__scaffold/echo", async ({ request }) => {
    const body = (await request.json()) as unknown;
    return HttpResponse.json({
      code: 0,
      message: "ok",
      data: body,
      trace_id: "trace-echo",
    });
  }),

  http.get("http://localhost/api/v1/__scaffold/slow", async () => {
    await delay(5_000);
    return HttpResponse.json({ code: 0, message: "ok", data: null, trace_id: "trace-slow" });
  }),

  http.get("http://localhost/api/v1/__scaffold/error", () =>
    HttpResponse.json(
      {
        code: "active_version_exists",
        message: "task has an active version",
        data: null,
        trace_id: "trace-409",
      },
      { status: 409 },
    ),
  ),

  http.get("http://localhost/api/v1/__scaffold/unauth", () =>
    HttpResponse.json(
      { code: "unauthenticated", message: "expired", data: null, trace_id: "trace-401" },
      { status: 401 },
    ),
  ),

  // --- task-read-api / task-write-api (default happy-path fixtures) ---
  // Tests override specific cases (empty list, 404, 409, 400) via server.use().

  http.get("http://localhost/api/v1/tasks", () =>
    ok({
      items: [taskSummaryFixture("task-1", "First task", "succeeded")],
      page: 1,
      page_size: 20,
      total: 1,
    }),
  ),

  http.get("http://localhost/api/v1/tasks/:id", ({ params }) =>
    ok({
      task: taskInfoFixture(String(params["id"]), "succeeded"),
      current_version: versionNodeFixture("ver-1", null, 1, "succeeded"),
      cost: zeroCost(),
    }),
  ),

  http.get("http://localhost/api/v1/tasks/:id/versions", () =>
    ok({ items: [versionNodeFixture("ver-1", null, 1, "succeeded")] }),
  ),

  http.get("http://localhost/api/v1/versions/:id/events", () =>
    ok({
      items: [
        eventFixture(1, 1, "status", { status: "running" }),
        eventFixture(2, 2, "status", { status: "succeeded" }),
      ],
      next_after_id: 2,
    }),
  ),

  http.post("http://localhost/api/v1/tasks", () =>
    ok({ task_id: "task-new", version_id: "ver-new", version_no: 1, status: "pending" }, 201),
  ),

  http.post("http://localhost/api/v1/tasks/:id/iterate", () =>
    ok({ version_id: "ver-2", version_no: 2, status: "pending" }, 201),
  ),

  // task-control-api default: 202 accepted, effective=queued (echoes the action).
  // Tests server.use() the best_effort / 409 invalid_state / 404 variants.
  http.post("http://localhost/api/v1/tasks/:id/control", async ({ params, request }) => {
    const body = (await request.json()) as { action?: string };
    return ok(
      {
        accepted: true,
        action: body.action ?? "pause",
        task_id: String(params["id"]),
        effective: "queued",
      },
      202,
    );
  }),
];

// ---------------------------------------------------------------------------
// fixture helpers (shaped exactly like the API DTOs; amount_usd is a string)
// ---------------------------------------------------------------------------

function ok(data: unknown, status = 200): ReturnType<typeof HttpResponse.json> {
  return HttpResponse.json({ code: 0, message: "ok", data, trace_id: "trace-test" }, { status });
}

export function zeroCost(): Record<string, unknown> {
  return {
    amount_usd: "0.00000000",
    input_tokens: 0,
    output_tokens: 0,
    cached_tokens: 0,
    tool_calls: 0,
    wall_time_ms: 0,
  };
}

export function taskSummaryFixture(
  id: string,
  title: string,
  status: string,
): Record<string, unknown> {
  return {
    id,
    title,
    task_type: "research",
    status,
    current_version: "ver-1",
    created_at: "2026-05-26T00:00:00Z",
    updated_at: "2026-05-26T00:00:00Z",
    cost: zeroCost(),
  };
}

export function taskInfoFixture(id: string, status: string): Record<string, unknown> {
  return {
    id,
    tenant_id: "00000000-0000-0000-0000-000000000001",
    user_id: "00000000-0000-0000-0000-000000000002",
    title: "First task",
    task_type: "research",
    status,
    current_version: "ver-1",
    created_at: "2026-05-26T00:00:00Z",
    updated_at: "2026-05-26T00:00:00Z",
  };
}

export function versionNodeFixture(
  id: string,
  parentId: string | null,
  versionNo: number,
  status: string,
): Record<string, unknown> {
  return {
    id,
    parent_id: parentId,
    version_no: versionNo,
    status,
    is_active: status === "pending" || status === "running",
    artifact_root: null,
    created_at: "2026-05-26T00:00:00Z",
    cost: zeroCost(),
  };
}

export function eventFixture(
  id: number,
  seq: number,
  kind: string,
  payload: unknown,
): Record<string, unknown> {
  return {
    id,
    version_id: "ver-1",
    run_id: "run-1",
    seq,
    kind,
    payload,
    created_at: "2026-05-26T00:00:00Z",
  };
}
