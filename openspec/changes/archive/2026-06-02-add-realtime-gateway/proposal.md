## Why

The web realtime client (`web/src/services/ws.ts`, capability `web-realtime-client`) is fully built and waiting for a server: it connects to `VITE_WS_URL`, sends `{op:subscribe}` frames, and self-silences its polling fallback once `getConnectionState() === "open"`. But the API exposes no WebSocket — so TaskDetail today only sees state via REST polling, and the `status:paused|running|cancelling` events that `add-worker-control-handling` now emits never reach the user live. This change adds the server half of the realtime channel (`docs/ARCHITECTURE.md §5.2 / §6.2`), closing the "submit → execute → **observe live** → retrieve" loop with a real-time view instead of a poll.

## What Changes

- Add `GET /api/v1/ws` — a WebSocket endpoint. Connect carries `?token=<jwt>`; a missing token closes with **4001** (the web client's "auth expired" code). Identity resolves through the existing auth stub (`DevTenantID`/`DevUserID`) until real JWT extraction lands, mirroring every REST handler today.
- Implement the documented subscription protocol: client→server text frames `{op:"subscribe"|"unsubscribe", topics:[...]}` and `{op:"ping"}`; topics are `task:<id>` / `version:<id>`. Server→client frames are exactly `{topic, kind, seq, ts, payload}` (the shape `web-realtime-client` asserts).
- Add a **per-instance fan-out consumer**: each gateway process binds its own exclusive, auto-delete, non-durable queue to the existing `task.events` topic exchange (`event.#`), so every instance receives every event independently of the shared `q.task.events` work queue (which keeps doing DB ingest). Each delivery is decoded once and fanned out to the local connections subscribed to `task:<task_id>` or `version:<version_id>` (ARCHITECTURE §6.2 — no cross-instance forwarding).
- **Owner-scoped subscriptions**: a `subscribe` to a `task:`/`version:` topic the caller does not own is rejected (the connection only ever receives events for its own tasks). Resolution goes through a new **application-layer ownership port** (`OwnsTask`/`OwnsVersion`) — the gateway never imports `domain/task` directly (§4.1); the domain gains a package-level `ownedTask` seam mirroring the existing package-level `ownedVersion`. Unauthorized/unknown/malformed topics get an `error`-kind frame and are not added to the set — `not found` and `not owned` are indistinguishable (no existence leak). This subscribe-time check is the real access boundary; the `4001` token check only gates token *presence*.
- **`ts`** is forwarded from the worker, which stamps an authoritative `ts` on every event (`TaskEvent.ts`, ARCHITECTURE §5.3). The API's *existing* ingest decoder happens to drop the field; the gateway's decoder includes it. Fall back to the gateway's receive time only if a delivery has no parseable `ts`. The client orders by `seq`, so `ts` is display-only.
- **Origin allowlist (CSWSH)**: because the token is a query param, the handshake validates `Origin` against a configurable allowlist (the SPA origin) to close the cross-site-WebSocket-hijacking vector.
- **Resource limits + read deadline**: an explicit socket read limit, a topics-per-connection cap, and a bound on a single `subscribe`'s `topics` array (each topic = one ownership DB probe); plus a server-side read deadline (> the client's 25s ping) to reap half-open connections.
- **`ping`** elicits an **application-level** `{op:"pong"}` text frame (a protocol-level pong is invisible to the browser `onmessage`, so it wouldn't reset the client's inbound-liveness timer → needless 60s reconnects).
- **Backpressure**: each connection has a bounded send buffer; a client that can't keep up is closed (server-initiated close), not allowed to stall the fan-out. The web client reconnects and gap-fills missing `seq` via `GET /versions/{id}/events?after_id=` (the existing `task-read-api` cursor) — so a dropped slow client recovers without the gateway buffering unboundedly.
- Graceful shutdown: on API shutdown the gateway stops the fan-out consumer and closes live connections with **1001 (going away)** within the drain window.
- New dependency: a WebSocket library for `api/` (`github.com/coder/websocket` — context-aware, net/http-native upgrade; decided in design).
- New metrics: `ws_connections_active` (gauge), `ws_subscriptions_active` (gauge), `ws_events_fanned_total{outcome}`, `ws_client_dropped_total{reason}` (AGENTS.md §7).

## Capabilities

### New Capabilities

- `realtime-gateway`: the `/ws` endpoint — connection/auth lifecycle, subscribe/unsubscribe/ping protocol, owner-scoped topic subscriptions, per-instance event fan-out from `task.events`, backpressure/slow-client eviction, and graceful shutdown.

### Modified Capabilities

(none.) Two things this touches but deliberately does not delta:
- **`api-messaging`** — declared topology is unchanged: the fan-out queue is an ephemeral, server-named exclusive queue created at runtime, not a durable entry in `DeclareTopology`; the existing `q.task.events` ingest consumer is untouched.
- **`api-bootstrap`** — the new `/ws` route, the `ServerDeps` field, and the new fan-out-consumer shutdown step are *additive wiring*, consistent with how `add-cost-service` (a new consumer + shutdown step) and `add-task-control-api`/`add-task-cost-api`/`add-artifacts-api` (new routes via `ServerDeps`) landed without an `api-bootstrap` delta. The gateway simply joins the existing ordered-shutdown sequence (it stops with the other consumers, before the MQ connection closes) — it does not change the documented ordering contract.

## Impact

- New code: `api/internal/interfaces/ws/` (or `interfaces/http` sibling) for the WS handler + connection hub; `api/internal/infrastructure/messaging/` gains the per-instance fan-out consumer (exclusive queue bind to `task.events`).
- Touches `cmd/api/main.go` (construct hub + fan-out consumer, register `/ws`, wire into ordered shutdown) and `interfaces/http/server.go` (`ServerDeps` gains the gateway, route registered on the v1 group). Auth identity via existing `DevTenantID`/`DevUserID`.
- New dependency: `github.com/coder/websocket`.
- No DB migration. No change to the worker, to the outbox, or to `DeclareTopology`'s durable topology.
- Reuses: owner-scoped probes from `task-read-api`; the `task.events` exchange + worker event envelope from `add-event-ingest-status-sync`; the unified slog discipline.
- Unblocks the **status** live view in `add-web-control-bar` (status/log/step/artifact events stream instead of poll); the web client needs no change (it already targets this exact contract). Cost deltas stay REST-polled this round — the Cost Service publishes to `cost.exchange` (`cost.<kind>`), not `task.events`, so a live-cost frame needs a separate future change (a cost topic on the fan-out, or a §5.3-⑤ republish onto `task.events`).
