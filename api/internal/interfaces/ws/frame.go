// Package ws implements the realtime gateway: the GET /api/v1/ws endpoint, the
// subscribe/unsubscribe/ping protocol, owner-scoped topic subscriptions, and
// the per-instance fan-out hub that pushes worker task events to subscribed
// connections. It is read-only with respect to state (the DB ingest path lives
// elsewhere); the gateway only consumes task.events and writes to sockets.
package ws

import "encoding/json"

// serverFrame is the EXACT event-frame shape the web-realtime-client asserts
// (ARCHITECTURE §5.2): {topic, kind, seq, ts, payload}. kind ∈ {status, log,
// step, artifact, error}. An `error`-kind frame reuses this shape with a
// {"message": ...} payload; the client ignores frames it can't parse as events,
// so errors surface server-side (logs/metrics), not in the UI.
type serverFrame struct {
	Topic   string          `json:"topic"`
	Kind    string          `json:"kind"`
	Seq     int64           `json:"seq"`
	Ts      string          `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// controlFrame is the app-level control reply — currently only {op:"pong"} in
// response to a client {op:"ping"} (design D8: a protocol-level pong is invisible
// to the browser onmessage, so it wouldn't reset the client's liveness timer).
type controlFrame struct {
	Op string `json:"op"`
}

// clientFrame is an inbound client message: {op:"subscribe"|"unsubscribe"|"ping",
// topics:[...]}. topics is absent/ignored for ping.
type clientFrame struct {
	Op     string   `json:"op"`
	Topics []string `json:"topics"`
}
