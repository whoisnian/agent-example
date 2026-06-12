# realtime-gateway Specification (Delta)

## MODIFIED Requirements

### Requirement: Subscription Protocol

The gateway SHALL accept client→server text frames `{op:"subscribe", topics:[...]}`, `{op:"unsubscribe", topics:[...]}`, and `{op:"ping"}`. Topics are strings of the form `task:<uuid>` or `version:<uuid>`. A `subscribe` adds each authorized topic to the connection's subscription set (a per-connection set keyed by topic — subscribing to an already-subscribed topic is idempotent and MUST NOT double-count subscriptions or double-deliver). `unsubscribe` removes them. `ping` is a liveness signal that MUST NOT alter subscriptions.

In response to `{op:"ping"}` the gateway MUST send an **application-level** `{op:"pong"}` text frame (not merely a protocol-level pong). The browser `WebSocket` API does not surface protocol pongs to `onmessage`, so the client's inbound-liveness timer would not reset on an idle connection and the client would needlessly reconnect; an app-level text frame keeps idle connections alive.

The server→client event frame shape MUST be exactly `{topic, kind, seq, ts, payload}` where `kind` is the worker event's `kind` forwarded verbatim — the currently emitted kinds are `status`, `log`, `plan`, `step`, `artifact`, `error`, `title`, and `summary`, and the gateway MUST NOT reject or rewrite a kind it does not recognise — `seq` is the event's monotonic sequence, `ts` is an RFC3339 UTC timestamp, and `payload` is the event body verbatim.

An unrecognized `op`, or a topic that is not a well-formed `task:<uuid>` / `version:<uuid>`, elicits an `error`-kind frame and MUST NOT change the subscription set or drop the connection. Error frames are diagnostic and primarily surface in server logs/metrics: the current web client ignores any frame that is not a well-formed event (it requires `topic`+`kind`+`seq`+`ts`), so a malformed-topic error is not rendered in the UI — it MUST still be logged/counted server-side.

#### Scenario: Subscribe then receive matching events
- **GIVEN** a connection that has sent `{op:"subscribe", topics:["task:T1"]}` for an owned task `T1`
- **WHEN** an event for `T1` arrives on the fan-out consumer
- **THEN** the connection MUST receive one `{topic:"task:T1", kind, seq, ts, payload}` frame, and a connection NOT subscribed to `task:T1` MUST NOT receive it

#### Scenario: Title events are forwarded verbatim
- **GIVEN** a connection subscribed to `task:T1`
- **WHEN** a worker event with `kind="title"` for `T1` arrives on the fan-out consumer
- **THEN** the connection MUST receive a frame with `kind="title"` and the event's `payload` unchanged

#### Scenario: Summary events are forwarded verbatim
- **GIVEN** a connection subscribed to `version:V1`
- **WHEN** a worker event with `kind="summary"` for `V1` arrives on the fan-out consumer
- **THEN** the connection MUST receive a frame with `kind="summary"` and the event's `payload` unchanged

#### Scenario: Duplicate subscribe is idempotent
- **GIVEN** a connection already subscribed to `task:T1`
- **WHEN** it sends `{op:"subscribe", topics:["task:T1"]}` again (e.g. the client re-sends its full topic set after reconnect)
- **THEN** the subscription set and the active-subscriptions gauge MUST NOT double-count, and each `T1` event MUST still be delivered exactly once for the `task:T1` topic

#### Scenario: Unsubscribe stops delivery
- **GIVEN** a connection subscribed to `task:T1`
- **WHEN** it sends `{op:"unsubscribe", topics:["task:T1"]}` and a later `T1` event arrives
- **THEN** the connection MUST NOT receive that event

#### Scenario: Ping elicits an app-level pong and changes nothing
- **WHEN** a connection sends `{op:"ping"}`
- **THEN** the server MUST send an `{op:"pong"}` text frame and the connection's subscription set MUST be unchanged

#### Scenario: Malformed topic or op is a soft, logged error
- **WHEN** a connection sends `{op:"subscribe", topics:["garbage"]}` or `{op:"bogus"}`
- **THEN** the server MUST send an `error`-kind frame, MUST NOT add any subscription, MUST keep the connection open, and MUST log/count the rejection server-side
