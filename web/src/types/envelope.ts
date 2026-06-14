/**
 * Unified API response envelope.
 * Mirrors api-bootstrap "Unified Response Envelope": every business endpoint
 * returns this shape. `code === 0` on success; non-zero is a typed error code.
 */
export interface ApiEnvelope<T> {
  code: number | string;
  message: string;
  data: T;
  trace_id?: string;
}

/**
 * Server-pushed realtime frame per ARCHITECTURE §5.2.
 */
export interface RealtimeEvent<TPayload = unknown> {
  topic: string;
  kind: "status" | "log" | "step" | "artifact" | "error" | string;
  seq: number;
  ts: string;
  payload: TPayload;
}

/**
 * Client→server WS control frames.
 */
export type RealtimeClientFrame =
  | { op: "subscribe"; topics: string[] }
  | { op: "unsubscribe"; topics: string[] }
  | { op: "ping" };

export type ConnectionState = "idle" | "connecting" | "open" | "reconnecting" | "closed";

/**
 * The known synthetic error codes emitted by `apiFetch` itself. The union is
 * intentionally open-ended: server-side codes are arbitrary strings.
 */
export type ApiErrorCode =
  | "timeout"
  | "network_error"
  | "unauthenticated"
  | "internal_error"
  | (string & {});
