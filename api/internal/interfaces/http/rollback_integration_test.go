//go:build integration

// HTTP-level integration tests for task-rollback-api (add-task-rollback-api).
// Reuses the `suite` harness + helpers from tasks_integration_test.go.
//
// Run with: make test-integration
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// postRollbackAs posts a rollback body with an arbitrary auth header (for the
// owner-scoping test); mirrors postJSON but lets the caller pick the principal.
func postRollbackAs(t *testing.T, s *suite, taskID, authHeader string, body any) (int, envelope) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/api/v1/tasks/"+taskID+"/rollback", &buf)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do req: %v", err)
	}
	defer resp.Body.Close()
	var env envelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env
}

func countOutbox(t *testing.T, s *suite, taskID uuid.UUID) int {
	t.Helper()
	// Outbox rows for a task's versions: aggregate_id is the version id, so join
	// through task_versions to count all execute rows for the task.
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox o
		   JOIN task_versions v ON v.id = o.aggregate_id
		  WHERE v.task_id = $1`, taskID).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func taskStatus(t *testing.T, s *suite, taskID uuid.UUID) string {
	t.Helper()
	var st string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&st); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	return st
}

func currentVersion(t *testing.T, s *suite, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	var v uuid.UUID
	if err := s.pool.QueryRow(context.Background(),
		`SELECT current_version FROM tasks WHERE id = $1`, taskID).Scan(&v); err != nil {
		t.Fatalf("read current_version: %v", err)
	}
	return v
}

func TestRollbackBranchHappy(t *testing.T) {
	s := newSuite(t)
	taskID, v1ID := createAndTerminate(t, s, "code-gen", "v1 prompt", "succeeded", "artifacts/v1/")

	status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
		"target_version_id": v1ID.String(),
		"mode":              "branch",
	})
	if status != http.StatusCreated {
		t.Fatalf("status=%d env=%+v", status, env)
	}
	var data struct {
		VersionID uuid.UUID `json:"version_id"`
		VersionNo int32     `json:"version_no"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v body=%s", err, env.Data)
	}
	if data.VersionNo != 2 || data.Status != "pending" {
		t.Fatalf("unexpected payload: %+v", data)
	}
	if got := currentVersion(t, s, taskID); got != data.VersionID {
		t.Fatalf("current_version=%s want=%s", got, data.VersionID)
	}

	// The execute outbox row carries v1 as parent + its artifact root.
	var payloadRaw []byte
	if err := s.pool.QueryRow(context.Background(),
		`SELECT payload FROM outbox WHERE aggregate_id = $1`, data.VersionID).Scan(&payloadRaw); err != nil {
		t.Fatalf("outbox payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["parent_version_id"] != v1ID.String() {
		t.Fatalf("parent_version_id=%v want=%s", payload["parent_version_id"], v1ID)
	}
	if payload["parent_artifact_root"] != "artifacts/v1/" {
		t.Fatalf("parent_artifact_root=%v", payload["parent_artifact_root"])
	}
}

func TestRollbackSwitchHappy(t *testing.T) {
	s := newSuite(t)
	// v1 succeeded, then iterate to v2 and terminate it so both are terminal and
	// current_version = v2.
	taskID, v1ID := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "artifacts/v1/")
	st, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate", map[string]any{"prompt": "v2"})
	if st != http.StatusCreated {
		t.Fatalf("iterate status=%d env=%+v", st, env)
	}
	var v2 struct {
		VersionID uuid.UUID `json:"version_id"`
	}
	_ = json.Unmarshal(env.Data, &v2)
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE task_versions SET status='succeeded' WHERE id=$1`, v2.VersionID); err != nil {
		t.Fatalf("terminate v2: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE tasks SET status='succeeded' WHERE id=$1`, taskID); err != nil {
		t.Fatalf("terminate task: %v", err)
	}

	outboxBefore := countOutbox(t, s, taskID)
	statusBefore := taskStatus(t, s, taskID)

	code, renv := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
		"target_version_id": v1ID.String(),
		"mode":              "switch",
	})
	if code != http.StatusOK {
		t.Fatalf("switch status=%d env=%+v", code, renv)
	}
	var data struct {
		CurrentVersionID uuid.UUID `json:"current_version_id"`
		VersionNo        int32     `json:"version_no"`
		Status           string    `json:"status"`
	}
	if err := json.Unmarshal(renv.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v body=%s", err, renv.Data)
	}
	if data.CurrentVersionID != v1ID || data.VersionNo != 1 {
		t.Fatalf("unexpected payload: %+v (want current=%s no=1)", data, v1ID)
	}
	// current_version moved to v1; NO new outbox row; tasks.status unchanged.
	if got := currentVersion(t, s, taskID); got != v1ID {
		t.Fatalf("current_version=%s want=%s", got, v1ID)
	}
	if got := countOutbox(t, s, taskID); got != outboxBefore {
		t.Fatalf("switch wrote outbox rows: before=%d after=%d", outboxBefore, got)
	}
	if got := taskStatus(t, s, taskID); got != statusBefore {
		t.Fatalf("switch changed tasks.status: before=%q after=%q", statusBefore, got)
	}
}

func TestRollbackActiveRejected(t *testing.T) {
	s := newSuite(t)
	// Fresh create leaves v1 active (pending).
	taskID, v1ID := createTask(t, s, "code-gen", "v1")
	for _, mode := range []string{"branch", "switch"} {
		code, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
			"target_version_id": v1ID.String(),
			"mode":              mode,
		})
		if code != http.StatusConflict {
			t.Fatalf("mode=%s status=%d env=%+v, want 409", mode, code, env)
		}
		if env.Code != "active_version_exists" {
			t.Errorf("mode=%s code=%v, want active_version_exists", mode, env.Code)
		}
		var data struct {
			ActiveVersionID     uuid.UUID `json:"active_version_id"`
			ActiveVersionStatus string    `json:"active_version_status"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("mode=%s data unmarshal: %v", mode, err)
		}
		if data.ActiveVersionID != v1ID {
			t.Errorf("mode=%s active_version_id=%s want=%s", mode, data.ActiveVersionID, v1ID)
		}
	}
}

func TestRollbackVersionNotFound(t *testing.T) {
	s := newSuite(t)
	taskID, _ := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "")
	code, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
		"target_version_id": uuid.NewString(), // not a version of this task
		"mode":              "branch",
	})
	if code != http.StatusNotFound || env.Code != "version_not_found" {
		t.Fatalf("status=%d code=%v, want 404 version_not_found", code, env.Code)
	}
}

func TestRollbackUnownedTask404(t *testing.T) {
	s := newSuite(t)
	taskID, v1ID := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "")
	other := intgAuthHeaderFor(uuid.New(), uuid.New())
	code, env := postRollbackAs(t, s, taskID.String(), other, map[string]any{
		"target_version_id": v1ID.String(),
		"mode":              "switch",
	})
	if code != http.StatusNotFound || env.Code != "task_not_found" {
		t.Fatalf("status=%d code=%v, want 404 task_not_found", code, env.Code)
	}
}

func TestRollbackSwitchToNonTerminal409(t *testing.T) {
	s := newSuite(t)
	// Construct the (normally-impossible) desynced state: task non-active
	// (succeeded), current_version = v1 (terminal), plus a directly-inserted
	// active v2. The non-active precondition passes, so the defensive
	// target-terminal assertion must fire.
	taskID, _ := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "")
	v2 := uuid.New()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 2, 'v2', 'pending')`, v2, taskID); err != nil {
		t.Fatalf("insert active v2: %v", err)
	}
	code, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
		"target_version_id": v2.String(),
		"mode":              "switch",
	})
	if code != http.StatusConflict || env.Code != "invalid_state" {
		t.Fatalf("status=%d code=%v, want 409 invalid_state", code, env.Code)
	}
}

// ---------------------------------------------------------------------------
// task-conversation-history — branch history excludes the abandoned branch
// ---------------------------------------------------------------------------

func TestRollbackBranchHistoryExcludesAbandonedBranch(t *testing.T) {
	s := newSuite(t)
	// Chain v1 ← v2 ← v3 (all terminal), then branch from v2: history must be
	// [v1, v2] and must not contain a v3 turn.
	taskID, v1ID := createAndTerminate(t, s, "code-gen", "v1 prompt", "succeeded", "")
	terminateVersion(t, s, taskID, v1ID, "succeeded", "v1 summary")

	iterate := func(prompt string) uuid.UUID {
		t.Helper()
		status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate",
			map[string]any{"prompt": prompt})
		if status != http.StatusCreated {
			t.Fatalf("iterate %q: status=%d env=%+v", prompt, status, env)
		}
		var data struct {
			VersionID uuid.UUID `json:"version_id"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("iterate %q unmarshal: %v", prompt, err)
		}
		return data.VersionID
	}
	v2ID := iterate("v2 prompt")
	terminateVersion(t, s, taskID, v2ID, "succeeded", "v2 summary")
	v3ID := iterate("v3 prompt")
	terminateVersion(t, s, taskID, v3ID, "succeeded", "v3 summary")

	status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/rollback", map[string]any{
		"target_version_id": v2ID.String(),
		"mode":              "branch",
		"prompt":            "branch from v2",
	})
	if status != http.StatusCreated {
		t.Fatalf("branch: status=%d env=%+v", status, env)
	}
	var data struct {
		VersionID uuid.UUID `json:"version_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("branch unmarshal: %v", err)
	}

	var payloadRaw []byte
	if err := s.pool.QueryRow(context.Background(),
		`SELECT payload FROM outbox WHERE aggregate_id = $1`, data.VersionID).Scan(&payloadRaw); err != nil {
		t.Fatalf("outbox payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	history, ok := payload["history"].([]any)
	if !ok || len(history) != 2 {
		t.Fatalf("history = %v, want exactly [v1, v2]", payload["history"])
	}
	first := history[0].(map[string]any)
	second := history[1].(map[string]any)
	if first["version_no"].(float64) != 1 || second["version_no"].(float64) != 2 {
		t.Fatalf("history order/content: %v", history)
	}
	if second["summary"] != "v2 summary" {
		t.Fatalf("v2 turn summary = %v", second["summary"])
	}
}
