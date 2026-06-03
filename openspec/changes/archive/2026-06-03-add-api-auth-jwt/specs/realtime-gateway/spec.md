## MODIFIED Requirements

### Requirement: WebSocket Endpoint and Connection Lifecycle

The API SHALL expose `GET /api/v1/ws` as a WebSocket endpoint matching the wire contract the `web-realtime-client` capability already targets. The connection URL carries the auth token as a `?token=<jwt>` query parameter. The handshake MUST validate the token as a real JWT — verifying its signature against the configured secret and rejecting an expired token — and resolve the connection's `Principal{tenant_id, user_id}` from the token claims. A connection opened with a missing, empty, malformed, wrong-signature, or expired `token` MUST be closed with WebSocket close code `4001` (the client's "auth expired" signal) and MUST NOT be added to the connection set.

Token validation is necessary but NOT the full access boundary: the actual per-topic access control is the subscribe-time ownership check (see "Owner-Scoped Subscriptions"): a connection only ever receives events for topics it both owns and subscribed to. That ownership check now resolves against the connection's token-derived principal, so per-user isolation is real — two connections opened with tokens for different principals resolve to different identities.

The close code MUST remain `4001` for every authentication failure reason (missing, empty, malformed, bad-signature, or expired) — this change does NOT introduce any new close code — so the `web-realtime-client`'s single "treat 4001 as auth-expired → clear token + redirect" handling continues to apply unchanged regardless of why the token was rejected.

The endpoint participates in the standard request-id / tracing middleware but does NOT use the `{code, message, data, trace_id}` REST envelope — WS frames have their own shape. The token MUST be read from the query string only and MUST NOT be logged.

#### Scenario: Connect without token is rejected
- **WHEN** a client opens `GET /api/v1/ws` with no `token` query parameter (or an empty one)
- **THEN** the server MUST close the WebSocket with code `4001` and MUST NOT register the connection or deliver any event

#### Scenario: Connect with an invalid or expired token is rejected
- **WHEN** a client opens `GET /api/v1/ws?token=<token>` where the token has a bad signature or a past expiry
- **THEN** the server MUST close the WebSocket with code `4001` and MUST NOT register the connection or deliver any event

#### Scenario: Connect with a valid token registers the connection
- **WHEN** a client opens `GET /api/v1/ws?token=<valid-jwt>` from an allowed origin
- **THEN** the WebSocket handshake MUST succeed and the connection MUST be tracked with the principal resolved from the token claims, with zero topic subscriptions until the client sends a `subscribe` frame
