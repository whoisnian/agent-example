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
 * - POST /api/v1/auth/login            → 200 for the dev creds, else 401 invalid_credentials
 */

/** Dev credentials the default login handler accepts (tests type these). */
export const DEV_LOGIN = { email: "dev@example.com", password: "dev-password" } as const;

export function loginUserFixture(): Record<string, unknown> {
  return {
    id: "00000000-0000-0000-0000-000000000002",
    tenant_id: "00000000-0000-0000-0000-000000000001",
    email: DEV_LOGIN.email,
  };
}

export const handlers = [
  http.get("http://localhost/healthz", () => HttpResponse.json({ status: "ok" })),

  // api-auth login. Default: 200 for the configured dev creds, else 401
  // `invalid_credentials`. Tests server.use() the invalid_input (400) variant.
  http.post("http://localhost/api/v1/auth/login", async ({ request }) => {
    const body = (await request.json()) as { email?: string; password?: string };
    if (body.email === DEV_LOGIN.email && body.password === DEV_LOGIN.password) {
      return ok({
        token: "test-jwt-token",
        expires_at: "2026-06-05T00:00:00Z",
        user: loginUserFixture(),
      });
    }
    return HttpResponse.json(
      {
        code: "invalid_credentials",
        message: "invalid credentials",
        data: null,
        trace_id: "trace-login",
      },
      { status: 401 },
    );
  }),

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

  http.get("http://localhost/api/v1/versions/:id", ({ params }) =>
    ok(versionDetailFixture(String(params["id"]), 1, "succeeded")),
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

  // --- task-cost-api (default happy-path fixtures) ---
  // `/me/cost` discriminates on `group_by`: absent → {total}; present →
  // {group_by, items}. The `model` fixture includes an "other" bucket.
  http.get("http://localhost/api/v1/me/cost", ({ request }) => {
    const groupBy = new URL(request.url).searchParams.get("group_by");
    if (!groupBy) return ok({ total: costFixture("0.42000000") });
    if (groupBy === "model") {
      return ok({
        group_by: "model",
        items: [
          { key: "claude-opus-4-7", totals: costFixture("0.30000000") },
          { key: "other", totals: costFixture("0.12000000") },
        ],
      });
    }
    return ok({
      group_by: groupBy,
      items: [
        { key: "2026-05-29", totals: costFixture("0.10000000") },
        { key: "2026-05-30", totals: costFixture("0.32000000") },
      ],
    });
  }),

  http.get("http://localhost/api/v1/tasks/:id/cost", ({ params }) =>
    ok({
      task_id: String(params["id"]),
      total: costFixture("1.72000000"),
      by_version: [
        {
          version_id: "ver-1",
          version_no: 1,
          created_at: "2026-05-26T00:00:00Z",
          cost: zeroCost(),
        },
      ],
    }),
  ),

  // --- artifacts-api (default happy-path fixtures) ---
  // List: two artifacts in created_at ASC, id ASC order; the second has all-null
  // metadata to exercise the placeholder path. Tests server.use() the empty-list
  // and version_not_found (404) variants.
  http.get("http://localhost/api/v1/versions/:id/artifacts", ({ params }) =>
    ok({
      version_id: String(params["id"]),
      artifacts: [
        artifactFixture("art-1", { mime: "text/markdown", bytes: 12_288 }),
        artifactFixture("art-2", { mime: null, bytes: null, sha256: null }),
      ],
    }),
  ),

  // Presign: an API-relative signed download URL + advisory expiry + echoed
  // metadata (add-artifact-download-proxy — `url` is an opaque relative path
  // served by the API download proxy, never an OSS origin).
  // Tests server.use() the artifact_not_found (404) and internal_error (500) variants.
  http.get("http://localhost/api/v1/artifacts/:id/presign", ({ params }) =>
    ok({
      url: `/api/v1/artifacts/${String(params["id"])}/download?token=stub-token`,
      expires_at: "2026-05-26T00:05:00Z",
      bytes: 12_288,
      mime: "text/markdown",
      sha256: "a".repeat(64),
    }),
  ),

  // Download proxy: canned bytes with a real Content-Type so text previews can
  // res.text() the body. Tests server.use() failure variants per case.
  http.get(
    "http://localhost/api/v1/artifacts/:id/download",
    () =>
      new HttpResponse("# Mock artifact\n\nstub bytes", {
        status: 200,
        headers: {
          "Content-Type": "text/markdown",
          "Content-Security-Policy": "sandbox allow-scripts",
          "X-Content-Type-Options": "nosniff",
        },
      }),
  ),

  // Version zip-archive presign (improve-artifact-conversation-ux): a relative
  // archive download URL + advisory expiry.
  http.get("http://localhost/api/v1/versions/:id/artifacts/archive/presign", ({ params }) =>
    ok({
      url: `/api/v1/versions/${String(params["id"])}/artifacts/archive?token=stub-archive-token`,
      expires_at: "2026-05-26T00:05:00Z",
    }),
  ),

  // Version preview mint: a tokenized base URL under which relative assets load.
  http.get("http://localhost/api/v1/versions/:id/preview", ({ params }) =>
    ok({
      base_url: `/api/v1/versions/${String(params["id"])}/preview/stub-preview-token`,
      expires_at: "2026-05-26T00:05:00Z",
    }),
  ),
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

/** A non-zero CostSummary with the given decimal-string amount. */
export function costFixture(amountUsd: string): Record<string, unknown> {
  return {
    amount_usd: amountUsd,
    input_tokens: 1200,
    output_tokens: 340,
    cached_tokens: 80,
    tool_calls: 3,
    wall_time_ms: 4500,
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

/** A VersionDetail-shaped fixture (the `GET /versions/{id}` read): full
 *  version row incl. `prompt`, plus runs + cost. Conversation turns read the
 *  prompt from here. */
export function versionDetailFixture(
  id: string,
  versionNo: number,
  status: string,
): Record<string, unknown> {
  return {
    version: {
      id,
      task_id: "task-1",
      parent_id: null,
      version_no: versionNo,
      prompt: `Prompt for ${id}`,
      params: null,
      status,
      is_active: status === "pending" || status === "running",
      artifact_root: null,
      summary: status === "succeeded" ? `Summary for ${id}` : null,
      created_at: "2026-05-26T00:00:00Z",
    },
    runs: [],
    cost: zeroCost(),
  };
}

/** An ArtifactMeta-shaped fixture. `mime`/`bytes`/`sha256` are present-and-
 *  nullable; override per case (e.g. `{mime:null, bytes:null}` for the
 *  placeholder path). */
export function artifactFixture(
  id: string,
  overrides: Partial<{
    kind: string;
    path: string | null;
    mime: string | null;
    bytes: number | null;
    sha256: string | null;
    created_at: string;
  }> = {},
): Record<string, unknown> {
  return {
    id,
    kind: "file",
    path: `${id}.md`,
    mime: "text/markdown",
    bytes: 1024,
    sha256: "f".repeat(64),
    created_at: "2026-05-26T00:00:00Z",
    ...overrides,
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
