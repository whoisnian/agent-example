package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// closeAuthMissing is the client's "auth expired" signal (web-realtime-client).
// The same code 4001 is used for EVERY auth failure reason (missing / malformed
// / bad-signature / expired) so the client's single "4001 → re-login" handler
// applies regardless of why. Token validity is necessary but not the full
// boundary — the real per-topic access control is the subscribe-time ownership
// check, which now resolves against the connection's token-derived principal.
const closeAuthMissing websocket.StatusCode = 4001

// ownershipProbeTimeout bounds a single subscribe-time ownership DB probe so a
// slow DB can't pin a reader goroutine.
const ownershipProbeTimeout = 5 * time.Second

// OwnershipChecker is the subscribe-time authorization port (satisfied by
// *apptask.OwnershipChecker). It is declared here as a consumer-side interface
// so the gateway depends ONLY on the application port — never on domain/task
// (AGENTS.md §4.1) — and is unit-testable with a fake.
type OwnershipChecker interface {
	OwnsTask(ctx context.Context, tenantID, userID, id uuid.UUID) error
	OwnsVersion(ctx context.Context, tenantID, userID, id uuid.UUID) error
}

// Config holds the operator-tunable gateway limits (defaults applied in
// NewGateway). AllowedOrigins is the CSWSH allowlist (design D9); empty means
// same-origin only.
type Config struct {
	AllowedOrigins     []string
	SendBuffer         int
	ReadDeadline       time.Duration
	ReadLimit          int64
	MaxTopicsPerConn   int
	MaxSubscribeTopics int
}

// Gateway is the /ws endpoint. It handles the handshake (origin + token),
// per-connection read loop, and subscribe authorization, wiring accepted
// connections into the Hub for fan-out. The connection identity is resolved
// from the validated `?token=<jwt>` claims, like every REST handler.
type Gateway struct {
	hub       *Hub
	ownership OwnershipChecker
	cfg       Config
	logger    *slog.Logger
	metrics   *observability.Metrics
	verifier  *auth.Verifier
}

// NewGateway builds the gateway, filling in sane defaults for any unset limit.
func NewGateway(
	hub *Hub,
	ownership OwnershipChecker,
	cfg Config,
	logger *slog.Logger,
	metrics *observability.Metrics,
	verifier *auth.Verifier,
) *Gateway {
	if cfg.SendBuffer <= 0 {
		cfg.SendBuffer = 128
	}
	if cfg.ReadDeadline <= 0 {
		cfg.ReadDeadline = 60 * time.Second
	}
	if cfg.ReadLimit <= 0 {
		cfg.ReadLimit = 32768
	}
	if cfg.MaxTopicsPerConn <= 0 {
		cfg.MaxTopicsPerConn = 64
	}
	if cfg.MaxSubscribeTopics <= 0 {
		cfg.MaxSubscribeTopics = 32
	}
	return &Gateway{
		hub:       hub,
		ownership: ownership,
		cfg:       cfg,
		logger:    logger,
		metrics:   metrics,
		verifier:  verifier,
	}
}

// Register mounts GET /ws on the v1 group. The upgrade response is raw (no REST
// envelope); WS frames have their own shape.
func (g *Gateway) Register(r *gin.RouterGroup) {
	r.GET("/ws", g.handle)
}

// Shutdown closes every live connection with 1001 (going away) so clients
// reconnect against the next instance (design D7).
func (g *Gateway) Shutdown() {
	g.hub.closeAll(websocket.StatusGoingAway, "server going away")
}

// handle upgrades the connection (origin allowlist first, then token presence),
// registers it, and runs the read loop until close. The token / RawQuery are
// NEVER logged.
func (g *Gateway) handle(c *gin.Context) {
	traceID := observability.TraceIDFromContext(c.Request.Context())

	// Origin allowlist enforced at upgrade (design D9). Accept writes the 403
	// response itself on a disallowed origin.
	wsConn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		OriginPatterns: g.cfg.AllowedOrigins,
	})
	if err != nil {
		g.logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "ws_handshake_rejected",
			slog.String("trace_id", traceID),
			slog.String("err", err.Error()),
		)
		return
	}

	// Token validation (4001 on any failure: missing/malformed/bad-sig/expired).
	// Read via c.Query so the token never enters a log field; RawQuery is
	// likewise never logged. On success the principal becomes the connection
	// identity that the subscribe-time ownership check resolves against.
	principal, err := g.verifier.Parse(c.Query("token"))
	if err != nil {
		_ = wsConn.Close(closeAuthMissing, "invalid token")
		g.logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "ws_auth_rejected",
			slog.String("trace_id", traceID),
		)
		return
	}

	wsConn.SetReadLimit(g.cfg.ReadLimit)
	conn := newConn(wsConn, principal.TenantID, principal.UserID, g.cfg.SendBuffer)
	g.hub.register(conn)
	g.logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "ws_connected", slog.String("trace_id", traceID))

	// connCtx is detached from the request context (which is unsafe to use after
	// Accept hijacks) and bounds the writer goroutine to the connection lifetime.
	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go conn.writeLoop(connCtx)

	g.readLoop(connCtx, conn, traceID)

	conn.close(websocket.StatusNormalClosure, "")
	g.hub.unregister(conn)
	g.logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "ws_disconnected", slog.String("trace_id", traceID))
}

// readLoop reads client frames under a per-read deadline (> the client's 25s
// ping) so a half-open connection is reaped (spec "Server-Side Read Deadline").
// It returns on any read error, deadline, or shutdown close.
func (g *Gateway) readLoop(ctx context.Context, c *conn, traceID string) {
	for {
		readCtx, readCancel := context.WithTimeout(ctx, g.cfg.ReadDeadline)
		_, data, err := c.ws.Read(readCtx)
		deadlineHit := readCtx.Err() == context.DeadlineExceeded
		readCancel()
		if err != nil {
			if deadlineHit && ctx.Err() == nil {
				g.metrics.WSClientDroppedTotal.WithLabelValues("read_deadline").Inc()
				c.close(websocket.StatusGoingAway, "idle read deadline")
				g.logger.LogAttrs(ctx, slog.LevelInfo, "ws_read_deadline_reaped", slog.String("trace_id", traceID))
			}
			return
		}

		var f clientFrame
		if json.Unmarshal(data, &f) != nil {
			g.sendError(c, "", "malformed frame")
			g.logger.LogAttrs(ctx, slog.LevelInfo, "ws_frame_malformed", slog.String("trace_id", traceID))
			continue
		}
		switch f.Op {
		case "subscribe":
			g.handleSubscribe(ctx, c, f.Topics, traceID)
		case "unsubscribe":
			for _, t := range f.Topics {
				g.hub.unsubscribe(c, t)
			}
		case "ping":
			g.sendControl(c, "pong")
		default:
			g.sendError(c, "", "unknown op")
			g.logger.LogAttrs(ctx, slog.LevelInfo, "ws_unknown_op",
				slog.String("trace_id", traceID),
				slog.String("op", f.Op),
			)
		}
	}
}

// handleSubscribe authorizes and adds each topic (design D5). The per-frame
// topics array is capped first so one frame can't trigger unbounded ownership
// probes; each topic is then probed once. Malformed / unauthorized / unknown
// topics get an error frame and are not added — not-found ≡ not-owned, so the
// frame never reveals whether the resource exists.
func (g *Gateway) handleSubscribe(ctx context.Context, c *conn, topics []string, traceID string) {
	if len(topics) > g.cfg.MaxSubscribeTopics {
		g.sendError(c, "", "too many topics")
		g.logger.LogAttrs(ctx, slog.LevelWarn, "ws_subscribe_oversized",
			slog.String("trace_id", traceID),
			slog.Int("count", len(topics)),
		)
		return
	}
	for _, topic := range topics {
		kind, id, ok := parseTopic(topic)
		if !ok {
			g.sendError(c, topic, "malformed topic")
			g.logger.LogAttrs(ctx, slog.LevelInfo, "ws_topic_malformed",
				slog.String("trace_id", traceID),
				slog.String("topic", topic),
			)
			continue
		}

		probeCtx, probeCancel := context.WithTimeout(ctx, ownershipProbeTimeout)
		var oerr error
		switch kind {
		case "task":
			oerr = g.ownership.OwnsTask(probeCtx, c.tenantID, c.userID, id)
		case "version":
			oerr = g.ownership.OwnsVersion(probeCtx, c.tenantID, c.userID, id)
		}
		probeCancel()
		if oerr != nil {
			g.sendError(c, topic, "not found")
			level := slog.LevelInfo
			if !errors.Is(oerr, apptask.ErrTaskNotFound) && !errors.Is(oerr, apptask.ErrVersionNotFound) {
				level = slog.LevelWarn // a genuine DB error, not just not-owned
			}
			g.logger.LogAttrs(ctx, level, "ws_subscribe_denied",
				slog.String("trace_id", traceID),
				slog.String("topic", topic),
			)
			continue
		}

		_, capReached := g.hub.subscribe(c, topic, g.cfg.MaxTopicsPerConn)
		if capReached {
			g.sendError(c, topic, "topic limit reached")
			g.logger.LogAttrs(ctx, slog.LevelWarn, "ws_topic_cap_reached",
				slog.String("trace_id", traceID),
				slog.String("topic", topic),
			)
			continue
		}
		g.logger.LogAttrs(ctx, slog.LevelInfo, "ws_subscribed",
			slog.String("trace_id", traceID),
			slog.String("topic", topic),
		)
	}
}

// sendError enqueues an error-kind frame (best effort). A full buffer evicts the
// connection — the same backpressure rule as fan-out.
func (g *Gateway) sendError(c *conn, topic, message string) {
	payload, _ := json.Marshal(map[string]string{"message": message})
	b, _ := json.Marshal(serverFrame{
		Topic:   topic,
		Kind:    "error",
		Ts:      time.Now().UTC().Format(time.RFC3339Nano),
		Payload: payload,
	})
	g.enqueueOrEvict(c, b)
}

// sendControl enqueues an app-level control frame ({op:"pong"}).
func (g *Gateway) sendControl(c *conn, op string) {
	b, _ := json.Marshal(controlFrame{Op: op})
	g.enqueueOrEvict(c, b)
}

func (g *Gateway) enqueueOrEvict(c *conn, b []byte) {
	if c.enqueue(b) {
		return
	}
	c.close(websocket.StatusTryAgainLater, "slow consumer")
	g.metrics.WSClientDroppedTotal.WithLabelValues("slow").Inc()
}

// parseTopic validates a `task:<uuid>` / `version:<uuid>` topic. It returns the
// kind ("task"/"version"), the parsed id, and ok=false for anything malformed.
func parseTopic(topic string) (kind string, id uuid.UUID, ok bool) {
	prefix, rest, found := strings.Cut(topic, ":")
	if !found || (prefix != "task" && prefix != "version") {
		return "", uuid.Nil, false
	}
	parsed, err := uuid.Parse(rest)
	if err != nil {
		return "", uuid.Nil, false
	}
	return prefix, parsed, true
}
