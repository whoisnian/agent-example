package messaging

import (
	"encoding/json"
	"testing"
	"time"
)

// TestEncodeBody_IsFlatPayloadNotWrapped locks the API→worker wire contract
// (ARCHITECTURE §5.3 / worker-messaging spec): the published body MUST be the
// flat domain message, exactly as stored in the outbox payload — NOT re-wrapped
// in an {msg_id, idempotency_key, payload, occurred_at} envelope. A regression
// here is what produced the worker `invalid_message_dlx` (missing task_id /
// version_id / run_id / attempt_no / task_type) when the relayer double-wrapped
// an already-complete §5.3 message. The worker's TaskExecuteMessage parses these
// keys at the top level, so they must be at the top level on the wire.
func TestEncodeBody_IsFlatPayloadNotWrapped(t *testing.T) {
	// A representative execute payload as the outbox stores it (json.RawMessage).
	payload := json.RawMessage(`{` +
		`"msg_id":"11111111-1111-1111-1111-111111111111",` +
		`"idempotency_key":"22222222-2222-2222-2222-222222222222",` +
		`"task_id":"33333333-3333-3333-3333-333333333333",` +
		`"version_id":"44444444-4444-4444-4444-444444444444",` +
		`"run_id":"22222222-2222-2222-2222-222222222222",` +
		`"attempt_no":1,"task_type":"research","prompt":"hi","params":{}}`)

	env := Envelope{
		MsgID:      "agg-id-not-in-body",
		Payload:    payload,
		OccurredAt: time.Now(),
	}

	body, err := encodeBody(env)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON object: %v\nbody=%s", err, body)
	}

	// The five fields the worker requires MUST be at the top level.
	for _, k := range []string{"task_id", "version_id", "run_id", "attempt_no", "task_type"} {
		if _, ok := got[k]; !ok {
			t.Errorf("wire body missing top-level %q (worker would DLX it); body=%s", k, body)
		}
	}

	// And it MUST NOT be the relayer-internal envelope wrapper.
	if _, ok := got["payload"]; ok {
		t.Errorf("wire body is wrapped (has top-level \"payload\"); must be the flat §5.3 message; body=%s", body)
	}
	if _, ok := got["occurred_at"]; ok {
		t.Errorf("wire body leaked the envelope \"occurred_at\" (belongs in the AMQP timestamp property); body=%s", body)
	}
}
