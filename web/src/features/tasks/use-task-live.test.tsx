import type { JSX } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { __resetRealtimeForTests, getRealtimeClient } from "@/services/ws";
import { useTaskLive, liveRefetchInterval } from "@/features/tasks/use-task-live";
import { taskKeys } from "@/features/tasks/queries";

/** Minimal in-process fake socket; the singleton picks it up via globalThis. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  public readyState = 0;
  private listeners = new Map<string, Set<EventListener>>();
  public sent: string[] = [];
  public constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  addEventListener(t: string, l: EventListener): void {
    (this.listeners.get(t) ?? this.listeners.set(t, new Set()).get(t)!).add(l);
  }
  removeEventListener(t: string, l: EventListener): void {
    this.listeners.get(t)?.delete(l);
  }
  send(d: string): void {
    this.sent.push(d);
  }
  close(): void {
    this.readyState = 3;
    this.fire("close", { code: 1000 });
  }
  openConn(): void {
    this.readyState = 1;
    this.fire("open", {});
  }
  deliver(event: object): void {
    this.fire("message", { data: JSON.stringify(event) });
  }
  private fire(type: string, payload: object): void {
    for (const l of this.listeners.get(type) ?? []) l({ type, ...payload } as Event);
  }
}

function Harness({
  taskId,
  versionId,
  qc,
}: {
  taskId: string;
  versionId: string | null;
  qc: QueryClient;
}): JSX.Element {
  useTaskLive(taskId, versionId, qc);
  return <div data-testid="harness" />;
}

afterEach(() => {
  __resetRealtimeForTests?.();
  vi.unstubAllGlobals();
  FakeWebSocket.instances = [];
});

describe("liveRefetchInterval", () => {
  it("is false when inactive", () => {
    __resetRealtimeForTests?.();
    expect(liveRefetchInterval(false)()).toBe(false);
  });

  it("polls when active and the WS is not open", () => {
    __resetRealtimeForTests?.();
    // singleton starts idle (not "open")
    expect(getRealtimeClient().getConnectionState()).not.toBe("open");
    expect(liveRefetchInterval(true)()).toBe(3000);
  });
});

describe("useTaskLive", () => {
  it("invalidates the task caches on a task frame", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    __resetRealtimeForTests?.();

    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");

    render(
      <QueryClientProvider client={qc}>
        <Harness taskId="t1" versionId="v1" qc={qc} />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(FakeWebSocket.instances.length).toBe(1));
    const sock = FakeWebSocket.instances[0]!;
    sock.openConn();
    sock.deliver({ topic: "task:t1", kind: "status", seq: 1, ts: "2026-05-26T00:00:00Z", payload: {} });

    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith({ queryKey: taskKeys.detail("t1") }),
    );
  });

  it("unsubscribes on unmount", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    __resetRealtimeForTests?.();

    const qc = new QueryClient();
    const { unmount } = render(
      <QueryClientProvider client={qc}>
        <Harness taskId="t1" versionId={null} qc={qc} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(FakeWebSocket.instances.length).toBe(1));
    const sock = FakeWebSocket.instances[0]!;
    sock.openConn();
    unmount();
    await waitFor(() => expect(sock.sent.some((f) => f.includes("unsubscribe"))).toBe(true));
  });
});
