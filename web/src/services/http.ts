import { useAuthStore } from "@/features/auth/store";
import { useUiStore } from "@/features/ui/store";
import type { ApiEnvelope, ApiErrorCode } from "@/types/envelope";

/** Injected at app init so `services/http.ts` stays framework-agnostic. */
export type Navigator = (to: string) => void;

let injectedNavigator: Navigator | null = null;
export function setNavigator(nav: Navigator | null): void {
  injectedNavigator = nav;
}

/**
 * Typed error thrown by `apiFetch`. Application code narrows via `instanceof`.
 */
export class ApiError extends Error {
  public readonly code: ApiErrorCode;
  public readonly traceId: string | undefined;
  public readonly status: number;
  /**
   * The error envelope's `data` block, when present (e.g. `invalid_input`'s
   * `{field, reason}`, or the 409 `{active_version_id, active_version_status}`).
   * `undefined` for synthetic client errors (timeout/network). Callers narrow.
   */
  public readonly data: unknown;

  constructor(args: {
    code: ApiErrorCode;
    message: string;
    traceId?: string | undefined;
    status: number;
    data?: unknown;
  }) {
    super(args.message);
    this.name = "ApiError";
    this.code = args.code;
    this.traceId = args.traceId;
    this.status = args.status;
    this.data = args.data;
  }
}

export interface ApiFetchInit extends RequestInit {
  /** Override default 30s timeout (ms). Implemented via AbortController. */
  timeoutMs?: number;
  /** If false, suppress the global error toast for non-401 errors. Default true. */
  toastOnError?: boolean;
  /**
   * If false, a 401 is NOT treated as a session expiry: the session is left
   * untouched, no redirect happens, and the promise rejects with the response
   * envelope's own `code`/`message`/`data` (e.g. `invalid_credentials`) at
   * `status:401`. Default true (clear session + redirect to `/login`). The
   * login flow opts out so credential errors surface inline.
   */
  interceptUnauthorized?: boolean;
}

const DEFAULT_TIMEOUT_MS = 30_000;

function getBaseUrl(): string {
  // import.meta.env is the Vite env. Tests via vitest also expose it.
  const base = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? "";
  return base.replace(/\/+$/, "");
}

function uuidv4(): string {
  // Prefer the platform crypto for real UUIDs; fall back to a manual v4 build
  // when randomUUID isn't available (older jsdom etc.).
  const c =
    typeof globalThis !== "undefined" ? (globalThis.crypto as Crypto | undefined) : undefined;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  const bytes = new Uint8Array(16);
  if (c && typeof c.getRandomValues === "function") {
    c.getRandomValues(bytes);
  } else {
    for (let i = 0; i < 16; i += 1) bytes[i] = Math.floor(Math.random() * 256);
  }
  const b0 = bytes[0] ?? 0;
  const b1 = bytes[1] ?? 0;
  const b2 = bytes[2] ?? 0;
  const b3 = bytes[3] ?? 0;
  const b4 = bytes[4] ?? 0;
  const b5 = bytes[5] ?? 0;
  const b6 = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  const b7 = bytes[7] ?? 0;
  const b8 = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const b9 = bytes[9] ?? 0;
  const b10 = bytes[10] ?? 0;
  const b11 = bytes[11] ?? 0;
  const b12 = bytes[12] ?? 0;
  const b13 = bytes[13] ?? 0;
  const b14 = bytes[14] ?? 0;
  const b15 = bytes[15] ?? 0;
  const hex = (n: number): string => n.toString(16).padStart(2, "0");
  return (
    `${hex(b0)}${hex(b1)}${hex(b2)}${hex(b3)}-` +
    `${hex(b4)}${hex(b5)}-` +
    `${hex(b6)}${hex(b7)}-` +
    `${hex(b8)}${hex(b9)}-` +
    `${hex(b10)}${hex(b11)}${hex(b12)}${hex(b13)}${hex(b14)}${hex(b15)}`
  );
}

/**
 * Resolve an API-relative path (e.g. the artifact presign action's opaque
 * download URL) into an absolute URL using the same base resolution as
 * apiFetch: the configured VITE_API_BASE_URL when set, else the page origin.
 * For raw `fetch` callers that bypass apiFetch (the artifact text preview).
 */
export function resolveApiUrl(path: string): string {
  if (path.startsWith("http")) return path;
  const base = getBaseUrl();
  return base ? `${base}${path}` : new URL(path, window.location.origin).toString();
}

/**
 * Handle a 401 the way the spec requires: clear the auth token, navigate to
 * `/login`, and let the caller see `code:"unauthenticated"`.
 */
function handleUnauthorized(): void {
  useAuthStore.getState().logout();
  if (injectedNavigator) injectedNavigator("/login");
}

function emitErrorToast(err: ApiError): void {
  useUiStore.getState().pushToast({ level: "error", message: err.message });
}

/**
 * Thin transport wrapper. Responsibilities are deliberately narrow:
 *   - URL prefix
 *   - Auth header injection
 *   - X-Request-Id
 *   - AbortController-based timeout
 *   - Envelope parsing
 *   - 401 interception
 *
 * Higher-level concerns (retry, dedupe, refetch) belong to React Query.
 */
export async function apiFetch<T>(path: string, init: ApiFetchInit = {}): Promise<T> {
  const {
    timeoutMs = DEFAULT_TIMEOUT_MS,
    toastOnError = true,
    interceptUnauthorized = true,
    signal: callerSignal,
    ...rest
  } = init;

  const url = path.startsWith("http") ? path : `${getBaseUrl()}${path}`;
  const headers = new Headers(rest.headers);

  const token = useAuthStore.getState().token;
  if (token && !headers.has("Authorization")) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  if (!headers.has("X-Request-Id")) headers.set("X-Request-Id", uuidv4());
  if (rest.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (!headers.has("Accept")) headers.set("Accept", "application/json");

  // Compose AbortSignals: timeout + caller signal.
  const controller = new AbortController();
  const onCallerAbort = (): void => controller.abort(callerSignal?.reason);
  if (callerSignal) {
    if (callerSignal.aborted) {
      controller.abort(callerSignal.reason);
    } else {
      callerSignal.addEventListener("abort", onCallerAbort, { once: true });
    }
  }
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);

  let response: Response;
  try {
    response = await fetch(url, { ...rest, headers, signal: controller.signal });
  } catch (e) {
    clearTimeout(timer);
    if (callerSignal) callerSignal.removeEventListener("abort", onCallerAbort);
    if (timedOut) {
      const err = new ApiError({ code: "timeout", message: "request timed out", status: 0 });
      if (toastOnError) emitErrorToast(err);
      throw err;
    }
    // Caller-initiated abort propagates as-is to preserve cancellation semantics.
    if (callerSignal?.aborted) throw e;
    const err = new ApiError({
      code: "network_error",
      message: e instanceof Error ? e.message : "network error",
      status: 0,
    });
    if (toastOnError) emitErrorToast(err);
    throw err;
  }
  clearTimeout(timer);
  if (callerSignal) callerSignal.removeEventListener("abort", onCallerAbort);

  // A 401 is a session expiry by default (clear + redirect). When the caller
  // opts out (login), fall through to the generic envelope parse below so the
  // real code (e.g. `invalid_credentials`) surfaces at status 401, untouched.
  if (response.status === 401 && interceptUnauthorized) {
    handleUnauthorized();
    // Still try to read envelope for trace id, but tolerate empty bodies.
    let traceId: string | undefined;
    try {
      const body = (await response.json()) as Partial<ApiEnvelope<unknown>>;
      traceId = body.trace_id;
    } catch {
      /* ignore */
    }
    const err = new ApiError({
      code: "unauthenticated",
      message: "session expired or invalid",
      traceId,
      status: 401,
    });
    // 401 is handled centrally — never toast it.
    throw err;
  }

  let envelope: ApiEnvelope<T>;
  try {
    envelope = (await response.json()) as ApiEnvelope<T>;
  } catch {
    const err = new ApiError({
      code: "internal_error",
      message: `invalid JSON in response (HTTP ${response.status})`,
      status: response.status,
    });
    if (toastOnError) emitErrorToast(err);
    throw err;
  }

  if (envelope.code !== 0 && envelope.code !== "0") {
    const err = new ApiError({
      code: envelope.code as ApiErrorCode,
      message: envelope.message ?? "request failed",
      traceId: envelope.trace_id,
      status: response.status,
      data: envelope.data,
    });
    if (toastOnError) emitErrorToast(err);
    throw err;
  }

  return envelope.data;
}
