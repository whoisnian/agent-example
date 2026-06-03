import { useAuthStore } from "@/features/auth/store";
import type {
  ConnectionState,
  RealtimeEvent,
  RealtimeClientFrame,
} from "@/types/envelope";

export type Handler = (event: RealtimeEvent) => void;
export type GapCallback = (
  topic: string,
  fromSeq: number,
  toSeq: number,
) => void | Promise<void>;
export type Navigator = (to: string) => void;

export interface RealtimeClientOptions {
  /** Override WS URL. Defaults to `VITE_WS_URL`. */
  url?: string;
  /** Default no-op + warn log. Override to fetch missed events via REST. */
  onGap?: GapCallback;
  /** Injected so the module stays framework-agnostic. */
  navigator?: Navigator;
  /** Override the WebSocket constructor (test injection). */
  webSocketImpl?: typeof WebSocket;
  /** Override Date.now for testability. */
  now?: () => number;
  /** Heartbeat ping interval (ms). Default 25_000. */
  heartbeatIntervalMs?: number;
  /** Inbound timeout after which connection is considered stale. Default 60_000. */
  heartbeatTimeoutMs?: number;
  /** Backoff base / cap (ms). Defaults 1_000 / 30_000. */
  backoffBaseMs?: number;
  backoffCapMs?: number;
  /** Background-idle close threshold while hidden + no subscribers. Default 5min. */
  idleCloseMs?: number;
}

interface TopicState {
  refcount: number;
  handlers: Set<Handler>;
  lastDeliveredSeq: number;
}

const DEFAULT_OPTS = {
  heartbeatIntervalMs: 25_000,
  heartbeatTimeoutMs: 60_000,
  backoffBaseMs: 1_000,
  backoffCapMs: 30_000,
  idleCloseMs: 5 * 60_000,
};

export class RealtimeClient {
  private url: string;
  private wsImpl: typeof WebSocket;
  private nowFn: () => number;
  private onGap: GapCallback;
  private navigator: Navigator | null;

  private heartbeatIntervalMs: number;
  private heartbeatTimeoutMs: number;
  private backoffBaseMs: number;
  private backoffCapMs: number;
  private idleCloseMs: number;

  private socket: WebSocket | null = null;
  private state: ConnectionState = "idle";
  private topics: Map<string, TopicState> = new Map();
  private reconnectAttempts = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private heartbeatDeadlineTimer: ReturnType<typeof setTimeout> | null = null;
  private idleTimer: ReturnType<typeof setTimeout> | null = null;
  private lastInboundAt = 0;
  private hiddenSince: number | null = null;
  private explicitClose = false;
  private visibilityHandler: (() => void) | null = null;

  public constructor(opts: RealtimeClientOptions = {}) {
    const envUrl =
      typeof import.meta !== "undefined"
        ? ((import.meta as ImportMeta).env?.["VITE_WS_URL"] as string | undefined)
        : undefined;
    this.url = opts.url ?? envUrl ?? "ws://localhost:8080/api/v1/ws";
    this.wsImpl = opts.webSocketImpl ?? (globalThis.WebSocket as typeof WebSocket);
    this.nowFn = opts.now ?? (() => Date.now());
    this.onGap =
      opts.onGap ??
      ((topic, fromSeq, toSeq): void => {
         
        console.warn(`[realtime] gap on ${topic}: ${fromSeq}..${toSeq}`);
      });
    this.navigator = opts.navigator ?? null;

    this.heartbeatIntervalMs = opts.heartbeatIntervalMs ?? DEFAULT_OPTS.heartbeatIntervalMs;
    this.heartbeatTimeoutMs = opts.heartbeatTimeoutMs ?? DEFAULT_OPTS.heartbeatTimeoutMs;
    this.backoffBaseMs = opts.backoffBaseMs ?? DEFAULT_OPTS.backoffBaseMs;
    this.backoffCapMs = opts.backoffCapMs ?? DEFAULT_OPTS.backoffCapMs;
    this.idleCloseMs = opts.idleCloseMs ?? DEFAULT_OPTS.idleCloseMs;

    if (typeof document !== "undefined" && typeof document.addEventListener === "function") {
      this.visibilityHandler = (): void => this.onVisibilityChange();
      document.addEventListener("visibilitychange", this.visibilityHandler);
    }
  }

  public getConnectionState(): ConnectionState {
    return this.state;
  }

  public setNavigator(nav: Navigator | null): void {
    this.navigator = nav;
  }

  public setOnGap(cb: GapCallback): void {
    this.onGap = cb;
  }

  /**
   * Subscribe to a topic. Returns an idempotent unsubscribe function.
   * Multiple subscribers to the same topic share one server-side subscription.
   */
  public subscribe(topic: string, handler: Handler): () => void {
    let entry = this.topics.get(topic);
    const isFirst = !entry;
    if (!entry) {
      entry = { refcount: 0, handlers: new Set(), lastDeliveredSeq: -1 };
      this.topics.set(topic, entry);
    }
    entry.refcount += 1;
    entry.handlers.add(handler);

    // Lazy connect / cancel any pending idle-close.
    this.cancelIdleClose();
    if (this.state === "idle" || this.state === "closed") {
      this.connect();
    } else if (this.state === "open" && isFirst) {
      this.sendFrame({ op: "subscribe", topics: [topic] });
    }
    // While "connecting"/"reconnecting": no-op; we'll send the subscription
    // on (re)open via the replay mechanism.

    let detached = false;
    return (): void => {
      if (detached) return;
      detached = true;
      const e = this.topics.get(topic);
      if (!e) return;
      e.handlers.delete(handler);
      e.refcount -= 1;
      if (e.refcount <= 0) {
        this.topics.delete(topic);
        if (this.state === "open") {
          this.sendFrame({ op: "unsubscribe", topics: [topic] });
        }
        this.scheduleIdleCloseIfApplicable();
      }
    };
  }

  /** Explicit close — does NOT reconnect. */
  public close(code = 1000): void {
    this.explicitClose = true;
    this.clearReconnect();
    this.clearHeartbeat();
    this.cancelIdleClose();
    if (this.socket) {
      try {
        this.socket.close(code);
      } catch {
        /* ignore */
      }
    }
    this.socket = null;
    this.state = "closed";
  }

  /** Test-only escape hatch. Gated by env at module export. */
  public _resetForTests(): void {
    this.explicitClose = true;
    this.clearReconnect();
    this.clearHeartbeat();
    this.cancelIdleClose();
    if (this.socket) {
      try {
        this.socket.close();
      } catch {
        /* ignore */
      }
    }
    this.socket = null;
    this.state = "idle";
    this.topics.clear();
    this.reconnectAttempts = 0;
    this.explicitClose = false;
    this.hiddenSince = null;
    if (this.visibilityHandler && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.visibilityHandler);
      this.visibilityHandler = null;
    }
  }

  // ---------- internals ----------

  private buildUrl(): string {
    const token = useAuthStore.getState().token;
    if (!token) return this.url;
    const sep = this.url.includes("?") ? "&" : "?";
    return `${this.url}${sep}token=${encodeURIComponent(token)}`;
  }

  private connect(): void {
    this.explicitClose = false;
    this.clearReconnect();
    this.state = this.reconnectAttempts === 0 ? "connecting" : "reconnecting";
    let socket: WebSocket;
    try {
      socket = new this.wsImpl(this.buildUrl());
    } catch (e) {
       
      console.error("[realtime] WebSocket constructor threw", e);
      this.scheduleReconnect();
      return;
    }
    this.socket = socket;

    socket.addEventListener("open", () => this.onOpen());
    socket.addEventListener("message", (ev) => this.onMessage(ev as MessageEvent));
    socket.addEventListener("close", (ev) => this.onClose(ev as CloseEvent));
    socket.addEventListener("error", () => {
      // The 'close' event always follows; reconnect logic lives there.
    });
  }

  private onOpen(): void {
    this.state = "open";
    this.reconnectAttempts = 0;
    this.lastInboundAt = this.nowFn();

    // Replay all active subscriptions in a single frame per spec.
    const topics = Array.from(this.topics.keys());
    if (topics.length > 0) this.sendFrame({ op: "subscribe", topics });

    this.startHeartbeat();
  }

  private onMessage(ev: MessageEvent): void {
    this.lastInboundAt = this.nowFn();
    const raw = ev.data;
    if (typeof raw !== "string") return;
    let frame: unknown;
    try {
      frame = JSON.parse(raw);
    } catch {
       
      console.warn("[realtime] invalid JSON frame", raw);
      return;
    }
    if (!isRealtimeEvent(frame)) {
      // Server may push non-event frames (e.g. pong). Ignore.
      return;
    }
    const entry = this.topics.get(frame.topic);
    if (!entry) return; // event for a topic we don't subscribe to (race)
    const seq = frame.seq;
    if (seq <= entry.lastDeliveredSeq) {
      // Duplicate / replay; drop.
      return;
    }
    if (entry.lastDeliveredSeq >= 0 && seq > entry.lastDeliveredSeq + 1) {
      // Gap: invoke callback synchronously, then deliver.
      try {
        void this.onGap(frame.topic, entry.lastDeliveredSeq + 1, seq - 1);
      } catch (e) {
         
        console.error("[realtime] onGap threw", e);
      }
    }
    entry.lastDeliveredSeq = seq;
    for (const h of entry.handlers) {
      try {
        h(frame);
      } catch (e) {
         
        console.error("[realtime] handler threw", e);
      }
    }
  }

  private onClose(ev: CloseEvent): void {
    this.clearHeartbeat();
    this.socket = null;
    if (ev.code === 4001) {
      // Auth expired. Clear and stop. Don't reconnect.
      useAuthStore.getState().logout();
      if (this.navigator) this.navigator("/login");
      this.state = "closed";
      return;
    }
    if (this.explicitClose) {
      this.state = "closed";
      return;
    }
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    this.state = "reconnecting";
    this.reconnectAttempts += 1;
    const expo = Math.min(
      this.backoffCapMs,
      this.backoffBaseMs * Math.pow(2, this.reconnectAttempts - 1),
    );
    // Full jitter: random in [0, expo).
    const delay = Math.floor(Math.random() * expo);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private clearReconnect(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private sendFrame(frame: RealtimeClientFrame): void {
    if (!this.socket || this.socket.readyState !== 1 /* OPEN */) return;
    try {
      this.socket.send(JSON.stringify(frame));
    } catch (e) {
       
      console.warn("[realtime] send failed", e);
    }
  }

  private startHeartbeat(): void {
    this.clearHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      this.sendFrame({ op: "ping" });
    }, this.heartbeatIntervalMs);
    this.heartbeatDeadlineTimer = setInterval(() => {
      if (this.nowFn() - this.lastInboundAt > this.heartbeatTimeoutMs) {
        // Stale connection — close + let reconnect path take over.
        try {
          this.socket?.close(1000);
        } catch {
          /* ignore */
        }
      }
    }, Math.min(this.heartbeatIntervalMs, 5_000));
  }

  private clearHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
    if (this.heartbeatDeadlineTimer) {
      clearInterval(this.heartbeatDeadlineTimer);
      this.heartbeatDeadlineTimer = null;
    }
  }

  private onVisibilityChange(): void {
    if (typeof document === "undefined") return;
    if (document.visibilityState === "hidden") {
      this.hiddenSince = this.nowFn();
      this.scheduleIdleCloseIfApplicable();
    } else {
      this.hiddenSince = null;
      this.cancelIdleClose();
      // If we closed while hidden and there are pending subscriptions, reconnect.
      if (this.state === "closed" && this.topics.size > 0) {
        this.explicitClose = false;
        this.reconnectAttempts = 0;
        this.connect();
      }
    }
  }

  private scheduleIdleCloseIfApplicable(): void {
    if (this.topics.size > 0) return;
    if (this.hiddenSince === null) return;
    this.cancelIdleClose();
    this.idleTimer = setTimeout(() => {
      this.idleTimer = null;
      if (this.topics.size === 0 && this.hiddenSince !== null) {
        this.explicitClose = true;
        if (this.socket) {
          try {
            this.socket.close(1000);
          } catch {
            /* ignore */
          }
        }
        this.socket = null;
        this.state = "closed";
      }
    }, this.idleCloseMs);
  }

  private cancelIdleClose(): void {
    if (this.idleTimer) {
      clearTimeout(this.idleTimer);
      this.idleTimer = null;
    }
  }
}

function isRealtimeEvent(x: unknown): x is RealtimeEvent {
  if (typeof x !== "object" || x === null) return false;
  const o = x as Record<string, unknown>;
  return (
    typeof o["topic"] === "string" &&
    typeof o["kind"] === "string" &&
    typeof o["seq"] === "number" &&
    typeof o["ts"] === "string"
  );
}

// ---------- Module-level singleton ----------

let _client: RealtimeClient | null = null;

export function getRealtimeClient(): RealtimeClient {
  if (!_client) _client = new RealtimeClient();
  return _client;
}

export function setRealtimeNavigator(nav: Navigator | null): void {
  getRealtimeClient().setNavigator(nav);
}

export function setRealtimeOnGap(cb: GapCallback): void {
  getRealtimeClient().setOnGap(cb);
}

const isProd =
  typeof import.meta !== "undefined" && (import.meta as ImportMeta).env?.MODE === "production";

/** Reset the module-level singleton; only available outside prod builds. */
export const __resetRealtimeForTests: (() => void) | undefined = isProd
  ? undefined
  : (): void => {
      if (_client) _client._resetForTests();
      _client = null;
    };

/** Convenience re-export so consumers needn't reach into the class. */
export function subscribe(topic: string, handler: Handler): () => void {
  return getRealtimeClient().subscribe(topic, handler);
}
