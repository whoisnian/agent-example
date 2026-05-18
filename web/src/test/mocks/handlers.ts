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
];
