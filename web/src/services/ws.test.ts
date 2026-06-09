import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RealtimeClient } from "@/services/ws";

/**
 * In-process fake WebSocket. Manually drives onopen / onmessage / onclose
 * so we can exercise the client deterministically without a real server.
 *
 * Per design D9: msw v2 WS handlers may not fully cover replay/reconnect/idle
 * close scenarios. Falling back to a local fake is the documented option.
 */
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];

  public readyState = 0; // CONNECTING
  public url: string;
  public sent: string[] = [];
  private listeners = new Map<string, Set<EventListener>>();

  public constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  public addEventListener(type: string, listener: EventListener): void {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(listener);
  }

  public removeEventListener(type: string, listener: EventListener): void {
    this.listeners.get(type)?.delete(listener);
  }

  public send(data: string): void {
    this.sent.push(data);
  }

  public close(_code = 1000): void {
    this.readyState = 3; // CLOSED
    this.fire("close", { code: _code });
  }

  // ----- test driver helpers -----
  public openConn(): void {
    this.readyState = 1;
    this.fire("open", {});
  }

  public deliver(event: object): void {
    this.fire("message", { data: JSON.stringify(event) });
  }

  public closeFromServer(code: number): void {
    this.readyState = 3;
    this.fire("close", { code });
  }

  private fire(type: string, payload: object): void {
    const event = { type, ...payload } as Event;
    for (const l of this.listeners.get(type) ?? []) l(event);
  }
}

function lastSocket(): FakeWebSocket {
  const s = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  if (!s) throw new Error("no fake socket constructed");
  return s;
}

let now = 0;

beforeEach(() => {
  FakeWebSocket.instances = [];
  now = 0;
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

function build(): RealtimeClient {
  return new RealtimeClient({
    url: "ws://test/api/v1/ws",
    webSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
    now: () => now,
    heartbeatIntervalMs: 50,
    heartbeatTimeoutMs: 200,
    backoffBaseMs: 10,
    backoffCapMs: 20,
  });
}

describe("realtime client — default endpoint", () => {
  it("defaults to a same-origin ws URL when no url/env override is set", () => {
    // No `url` option and (in the test env) no VITE_WS_URL → derive from origin.
    const c = new RealtimeClient({
      webSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
    });
    c.subscribe("task:t1", vi.fn());
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    expect(lastSocket().url.startsWith(`${proto}//${window.location.host}/api/v1/ws`)).toBe(true);
    c.close();
  });
});

describe("realtime client — connection & subscription", () => {
  it("lazily connects on first subscribe", () => {
    const c = build();
    expect(FakeWebSocket.instances).toHaveLength(0);
    c.subscribe("task:t1", vi.fn());
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(c.getConnectionState()).toBe("connecting");
  });

  it("sends a single subscribe frame after open", () => {
    const c = build();
    c.subscribe("task:t1", vi.fn());
    lastSocket().openConn();
    expect(lastSocket().sent[0]).toBe(JSON.stringify({ op: "subscribe", topics: ["task:t1"] }));
    expect(c.getConnectionState()).toBe("open");
  });

  it("coalesces overlapping subscriptions to the same topic", () => {
    const c = build();
    c.subscribe("task:t1", vi.fn());
    lastSocket().openConn();
    c.subscribe("task:t1", vi.fn()); // second handler — must NOT cause a 2nd subscribe frame
    // Only one subscribe frame ever sent
    expect(lastSocket().sent.filter((s) => s.includes('"op":"subscribe"'))).toHaveLength(1);
  });

  it("unsubscribes via the returned function", () => {
    const c = build();
    const detach = c.subscribe("task:t1", vi.fn());
    lastSocket().openConn();
    detach();
    expect(lastSocket().sent.some((s) => s.includes('"op":"unsubscribe"'))).toBe(true);
  });
});

describe("realtime client — dedup, gap, reconnect", () => {
  it("dedupes by seq", () => {
    const c = build();
    const h = vi.fn();
    c.subscribe("task:t1", h);
    lastSocket().openConn();
    lastSocket().deliver({ topic: "task:t1", kind: "status", seq: 42, ts: "t", payload: {} });
    lastSocket().deliver({ topic: "task:t1", kind: "status", seq: 42, ts: "t", payload: {} });
    expect(h).toHaveBeenCalledTimes(1);
  });

  it("invokes onGap when seq jumps", () => {
    const onGap = vi.fn();
    const c = new RealtimeClient({
      url: "ws://test/api/v1/ws",
      webSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
      onGap,
      heartbeatIntervalMs: 1_000_000, // disable for this test
      heartbeatTimeoutMs: 1_000_000,
    });
    const h = vi.fn();
    c.subscribe("task:t1", h);
    lastSocket().openConn();
    lastSocket().deliver({ topic: "task:t1", kind: "x", seq: 10, ts: "t", payload: {} });
    lastSocket().deliver({ topic: "task:t1", kind: "x", seq: 13, ts: "t", payload: {} });
    expect(onGap).toHaveBeenCalledWith("task:t1", 11, 12);
    expect(h).toHaveBeenCalledTimes(2);
  });

  it("re-sends all subscriptions in one frame on reconnect", async () => {
    const c = build();
    c.subscribe("task:t1", vi.fn());
    c.subscribe("version:v1", vi.fn());
    const first = lastSocket();
    first.openConn();
    // first socket got one frame with [task:t1] and one with [version:v1]
    first.closeFromServer(1006);
    // backoff fires within backoffCapMs=20
    await vi.advanceTimersByTimeAsync(25);
    const second = lastSocket();
    expect(second).not.toBe(first);
    second.openConn();
    const replay = second.sent[0] ?? "";
    expect(replay).toContain('"op":"subscribe"');
    expect(replay).toContain("task:t1");
    expect(replay).toContain("version:v1");
    // Just one frame in one replay
    expect(second.sent.filter((s) => s.includes('"op":"subscribe"'))).toHaveLength(1);
  });
});
