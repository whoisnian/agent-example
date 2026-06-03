// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/mocks/server";
import { apiFetch, ApiError, setNavigator } from "@/services/http";
import { useAuthStore } from "@/features/auth/store";

const BASE = "http://localhost";

describe("apiFetch — envelope", () => {
  beforeEach(() => {
    useAuthStore.setState({ token: null, user: null });
    setNavigator(null);
  });

  afterEach(() => {
    setNavigator(null);
  });

  it("unwraps the data field on success", async () => {
    const result = await apiFetch<{ ping: number }>(`${BASE}/api/v1/__scaffold/echo`, {
      method: "POST",
      body: JSON.stringify({ ping: 1 }),
    });
    expect(result).toEqual({ ping: 1 });
  });

  it("throws typed ApiError with code/status/traceId for business error", async () => {
    await expect(
      apiFetch(`${BASE}/api/v1/__scaffold/error`, { toastOnError: false }),
    ).rejects.toMatchObject({
      code: "active_version_exists",
      status: 409,
      traceId: "trace-409",
    });
  });

  it("times out via AbortController", async () => {
    await expect(
      apiFetch(`${BASE}/api/v1/__scaffold/slow`, { timeoutMs: 50, toastOnError: false }),
    ).rejects.toMatchObject({ code: "timeout", status: 0 });
  });

  it("injects Authorization header when a token is present", async () => {
    useAuthStore.setState({ token: "token-xyz" });
    const observed: { v: string | null } = { v: null };
    server.use(
      http.get(`${BASE}/api/v1/__scaffold/probe`, ({ request }) => {
        observed.v = request.headers.get("authorization");
        return HttpResponse.json({ code: 0, message: "ok", data: null, trace_id: "t" });
      }),
    );
    await apiFetch(`${BASE}/api/v1/__scaffold/probe`);
    expect(observed.v).toBe("Bearer token-xyz");
  });

  it("injects an X-Request-Id header", async () => {
    const observed: { v: string | null } = { v: null };
    server.use(
      http.get(`${BASE}/api/v1/__scaffold/rid`, ({ request }) => {
        observed.v = request.headers.get("x-request-id");
        return HttpResponse.json({ code: 0, message: "ok", data: null, trace_id: "t" });
      }),
    );
    await apiFetch(`${BASE}/api/v1/__scaffold/rid`);
    expect(observed.v).toBeTruthy();
    expect((observed.v ?? "").length).toBeGreaterThanOrEqual(36);
  });
});

describe("apiFetch — 401 handling", () => {
  afterEach(() => setNavigator(null));

  it("clears the session, navigates to /login, rejects with unauthenticated", async () => {
    useAuthStore.setState({
      token: "stale",
      user: { id: "u", tenant_id: "t", email: "e@x" },
    });
    const nav = vi.fn();
    setNavigator(nav);

    await expect(apiFetch(`${BASE}/api/v1/__scaffold/unauth`)).rejects.toMatchObject({
      code: "unauthenticated",
      status: 401,
    });

    expect(useAuthStore.getState().token).toBeNull();
    expect(useAuthStore.getState().user).toBeNull();
    expect(nav).toHaveBeenCalledWith("/login");
  });

  it("with interceptUnauthorized:false, surfaces the envelope code and touches nothing", async () => {
    useAuthStore.setState({
      token: "keep",
      user: { id: "u", tenant_id: "t", email: "e@x" },
    });
    const nav = vi.fn();
    setNavigator(nav);
    server.use(
      http.post(`${BASE}/api/v1/auth/login`, () =>
        HttpResponse.json(
          { code: "invalid_credentials", message: "bad creds", data: null, trace_id: "t" },
          { status: 401 },
        ),
      ),
    );

    await expect(
      apiFetch(`${BASE}/api/v1/auth/login`, {
        method: "POST",
        body: JSON.stringify({ email: "a", password: "b" }),
        toastOnError: false,
        interceptUnauthorized: false,
      }),
    ).rejects.toMatchObject({ code: "invalid_credentials", status: 401 });

    expect(useAuthStore.getState().token).toBe("keep");
    expect(nav).not.toHaveBeenCalled();
  });
});

describe("apiFetch — network failure", () => {
  it("surfaces ApiError(code:network_error) instead of leaking the raw TypeError", async () => {
    server.use(
      http.get(`${BASE}/api/v1/__scaffold/boom`, () => {
        return HttpResponse.error();
      }),
    );
    await expect(
      apiFetch(`${BASE}/api/v1/__scaffold/boom`, { toastOnError: false }),
    ).rejects.toBeInstanceOf(ApiError);
    await expect(
      apiFetch(`${BASE}/api/v1/__scaffold/boom`, { toastOnError: false }),
    ).rejects.toMatchObject({ code: "network_error", status: 0 });
  });
});
