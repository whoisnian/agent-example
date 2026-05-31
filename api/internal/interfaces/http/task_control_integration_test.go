//go:build integration

// HTTP-level integration tests for task-control-api. Reuses the suite +
// helpers from tasks_integration_test.go. Each top-level test owns one
// PostgreSQL container; subtests share that container against
// independently-keyed rows.
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// controlBody is the 202 payload shape (mirrors httpapi.controlResponse).
type controlBody struct {
	Accepted  bool      `json:"accepted"`
	Action    string    `json:"action"`
	TaskID    uuid.UUID `json:"task_id"`
	Effective string    `json:"effective"`
}

// readOutbox loads every outbox row for an aggregate_id, ordered by id ASC.
type outboxRow struct {
	ID       int64
	Exchange string
	Topic    string
	Payload  []byte
}

func readOutboxFor(t *testing.T, s *suite, aggregateID uuid.UUID) []outboxRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT id, exchange, topic, payload FROM outbox WHERE aggregate_id = $1 AND aggregate = 'task' ORDER BY id ASC`,
		aggregateID,
	)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.Exchange, &r.Topic, &r.Payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// happy paths
// ---------------------------------------------------------------------------

func TestControlHappyPath(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	t.Run("pause running task -> 202 queued, outbox row written", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "running")
		// Point tasks.current_version at v1 so GetActiveRunIDForTask has a join target
		if _, err := s.pool.Exec(context.Background(),
			`UPDATE tasks SET current_version = $1 WHERE id = $2`, v1, taskID); err != nil {
			t.Fatalf("set current_version: %v", err)
		}
		run := insertControlTaskRun(t, s, v1)

		body := `{"action":"pause","reason":"manual"}`
		status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(body))
		if status != http.StatusAccepted {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got controlBody
		if err := json.Unmarshal(env.Data, &got); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		if got.Effective != "queued" {
			t.Errorf("effective = %q, want queued (active run exists)", got.Effective)
		}
		// Sanity: outbox row points at task.control with the right routing key.
		rows := readOutboxFor(t, s, taskID)
		if len(rows) != 1 {
			t.Fatalf("outbox rows = %d, want 1", len(rows))
		}
		r := rows[0]
		if r.Exchange != "task.control" {
			t.Errorf("exchange = %q, want task.control", r.Exchange)
		}
		if r.Topic != "task."+taskID.String() {
			t.Errorf("topic = %q, want task.<task_id>", r.Topic)
		}
		var p struct {
			TaskID    string `json:"task_id"`
			VersionID string `json:"version_id"`
			RunID     string `json:"run_id"`
			Action    string `json:"action"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.TaskID != taskID.String() || p.Action != "pause" || p.Reason != "manual" {
			t.Errorf("payload mismatch: %+v", p)
		}
		if p.RunID != run.String() {
			t.Errorf("payload.run_id = %q, want %q", p.RunID, run)
		}
	})

	t.Run("cancel pre-claim task -> 202 best_effort, run_id null", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "pending")
		// no version, no run
		status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(`{"action":"cancel"}`))
		if status != http.StatusAccepted {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got controlBody
		if err := json.Unmarshal(env.Data, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Effective != "best_effort" {
			t.Errorf("effective = %q, want best_effort (pre-claim)", got.Effective)
		}
		rows := readOutboxFor(t, s, taskID)
		if len(rows) != 1 {
			t.Fatalf("outbox rows = %d, want 1", len(rows))
		}
		var p struct {
			RunID     *string `json:"run_id"`
			VersionID *string `json:"version_id"`
		}
		if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.RunID != nil {
			t.Errorf("payload.run_id = %v, want null", *p.RunID)
		}
	})
}

// ---------------------------------------------------------------------------
// state-machine 409 matrix
// ---------------------------------------------------------------------------

func TestControlInvalidState(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	cases := []struct {
		name      string
		taskStatus string
		action    string
	}{
		{"pause when paused", "paused", "pause"},
		{"pause when succeeded", "succeeded", "pause"},
		{"resume when running", "running", "resume"},
		{"resume when pending", "pending", "resume"},
		{"cancel when succeeded", "succeeded", "cancel"},
		{"cancel when failed", "failed", "cancel"},
		{"cancel when cancelled", "cancelled", "cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, tc.taskStatus)
			body, _ := json.Marshal(map[string]string{"action": tc.action})
			status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(body))
			if status != http.StatusConflict {
				t.Fatalf("status=%d env=%+v", status, env)
			}
			if env.Code != "invalid_state" {
				t.Errorf("code = %v, want invalid_state", env.Code)
			}
			if env.Message == "" || !containsStr(env.Message, tc.taskStatus) {
				t.Errorf("message missing current status %q: %q", tc.taskStatus, env.Message)
			}
			// No outbox row written.
			if rows := readOutboxFor(t, s, taskID); len(rows) != 0 {
				t.Errorf("expected no outbox rows, got %d", len(rows))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ownership 404
// ---------------------------------------------------------------------------

func TestControlOwnerIsolation(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	t.Run("unknown task -> 404 task_not_found", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())
		status, env := postJSON(t, s.ts, "/api/v1/tasks/"+unknown.String()+"/control", json.RawMessage(`{"action":"cancel"}`))
		if status != http.StatusNotFound || env.Code != "task_not_found" {
			t.Errorf("status=%d code=%v want 404/task_not_found", status, env.Code)
		}
	})

	t.Run("unowned task -> 404 task_not_found", func(t *testing.T) {
		otherUser := uuid.Must(uuid.NewV7())
		taskID := insertTaskRow(t, s, s.devTenantID, otherUser, "running")
		status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(`{"action":"cancel"}`))
		if status != http.StatusNotFound || env.Code != "task_not_found" {
			t.Errorf("status=%d code=%v want 404/task_not_found", status, env.Code)
		}
		if rows := readOutboxFor(t, s, taskID); len(rows) != 0 {
			t.Errorf("expected no outbox rows for unowned task, got %d", len(rows))
		}
	})
}

// ---------------------------------------------------------------------------
// duplicate-control + concurrent-serialize
// ---------------------------------------------------------------------------

func TestControlDuplicateProducesTwoRows(t *testing.T) {
	t.Parallel()
	s := newSuite(t)
	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")

	for i := 0; i < 2; i++ {
		status, _ := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(`{"action":"pause"}`))
		if status != http.StatusAccepted {
			t.Fatalf("request %d status=%d, want 202", i, status)
		}
	}
	rows := readOutboxFor(t, s, taskID)
	if len(rows) != 2 {
		t.Errorf("outbox rows = %d, want 2 (API does NOT dedupe; worker is responsible)", len(rows))
	}
}

func TestControlConcurrentCancelsSerialise(t *testing.T) {
	t.Parallel()
	s := newSuite(t)
	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
	v1 := insertVersionRow(t, s, taskID, nil, 1, "running")
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE tasks SET current_version = $1 WHERE id = $2`, v1, taskID); err != nil {
		t.Fatalf("set current_version: %v", err)
	}
	run := insertControlTaskRun(t, s, v1)

	const N = 2
	var wg sync.WaitGroup
	statuses := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, _ := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/control", json.RawMessage(`{"action":"cancel"}`))
			statuses[idx] = s
		}(i)
	}
	wg.Wait()

	for i, st := range statuses {
		if st != http.StatusAccepted {
			t.Errorf("request %d status=%d, want 202", i, st)
		}
	}
	rows := readOutboxFor(t, s, taskID)
	if len(rows) != N {
		t.Fatalf("outbox rows = %d, want %d", len(rows), N)
	}
	// Both rows MUST carry identical run_id (no race in resolving the
	// current run thanks to FOR UPDATE).
	for i, r := range rows {
		var p struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("decode payload %d: %v", i, err)
		}
		if p.RunID != run.String() {
			t.Errorf("row %d run_id = %q, want %q", i, p.RunID, run)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// insertControlTaskRun inserts a fresh task_runs row keyed on version_id.
// Mirrors the helper in cost_settler_integration_test.go but local-named so
// suite shows two callers cleanly.
func insertControlTaskRun(t *testing.T, s *suite, versionID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
		 VALUES ($1, $2, 1, 'running', $3)`,
		id, versionID, "ik-ctl-"+id.String()); err != nil {
		t.Fatalf("insert task_run: %v", err)
	}
	return id
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
