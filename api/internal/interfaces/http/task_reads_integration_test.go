//go:build integration

// HTTP-level integration tests for task-read-api. Reuses the suite / postJSON
// helpers from tasks_integration_test.go (same package). Each top-level test
// owns one PostgreSQL container and exercises several read scenarios via
// t.Run subtests against independently-keyed rows.
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func getJSON(t *testing.T, url string) (int, envelope) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return resp.StatusCode, env
}

type costJSON struct {
	AmountUSD    string `json:"amount_usd"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	ToolCalls    int32  `json:"tool_calls"`
	WallTimeMs   int64  `json:"wall_time_ms"`
}

func insertTaskRow(t *testing.T, s *suite, tenant, user uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status)
		 VALUES ($1, $2, $3, 't', 'code-gen', $4)`,
		id, tenant, user, status); err != nil {
		t.Fatalf("insert task row: %v", err)
	}
	return id
}

func insertVersionRow(t *testing.T, s *suite, taskID uuid.UUID, parentID *uuid.UUID, versionNo int32, status string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO task_versions (id, task_id, parent_id, version_no, prompt, status)
		 VALUES ($1, $2, $3, $4, 'p', $5)`,
		id, taskID, parentID, versionNo, status); err != nil {
		t.Fatalf("insert version row: %v", err)
	}
	return id
}

func insertVersionCost(t *testing.T, s *suite, taskID, versionID uuid.UUID, amount string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO task_costs (version_id, task_id, amount_usd) VALUES ($1, $2, $3::numeric)`,
		versionID, taskID, amount); err != nil {
		t.Fatalf("insert version cost: %v", err)
	}
}

func insertEventRow(t *testing.T, s *suite, taskID, versionID uuid.UUID, runID *uuid.UUID, seq int64, kind string) int64 {
	t.Helper()
	var id int64
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO task_events (task_id, version_id, run_id, seq, kind, payload)
		 VALUES ($1, $2, $3, $4, $5, '{"k":"v"}'::jsonb) RETURNING id`,
		taskID, versionID, runID, seq, kind).Scan(&id); err != nil {
		t.Fatalf("insert event row: %v", err)
	}
	return id
}

func iterate(t *testing.T, s *suite, taskID uuid.UUID, prompt string) uuid.UUID {
	t.Helper()
	st, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate", map[string]any{"prompt": prompt})
	if st != http.StatusCreated {
		t.Fatalf("iterate status=%d env=%+v", st, env)
	}
	var data struct {
		VersionID uuid.UUID `json:"version_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("iterate data: %v", err)
	}
	return data.VersionID
}

// ---------------------------------------------------------------------------
// GET /tasks
// ---------------------------------------------------------------------------

func TestReadListTasks(t *testing.T) {
	s := newSuite(t)

	t.Run("pagination_and_owner_isolation", func(t *testing.T) {
		createTask(t, s, "code-gen", "a")
		createTask(t, s, "code-gen", "b")
		createTask(t, s, "code-gen", "c")
		// Foreign rows that MUST NOT appear: same tenant/different user, and
		// a different tenant entirely.
		insertTaskRow(t, s, s.devTenantID, uuid.Must(uuid.NewV7()), "pending")
		insertTaskRow(t, s, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "pending")

		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks?page=1&page_size=2")
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		var data struct {
			Items    []json.RawMessage `json:"items"`
			Page     int               `json:"page"`
			PageSize int               `json:"page_size"`
			Total    int64             `json:"total"`
		}
		mustJSON(t, env.Data, &data)
		if data.Total != 3 {
			t.Fatalf("total = %d, want 3 (foreign tasks must be excluded)", data.Total)
		}
		if len(data.Items) != 2 || data.Page != 1 || data.PageSize != 2 {
			t.Fatalf("page=%d page_size=%d items=%d", data.Page, data.PageSize, len(data.Items))
		}
	})

	t.Run("status_filter", func(t *testing.T) {
		createAndTerminate(t, s, "research", "done", "succeeded", "")
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks?status=succeeded")
		if st != http.StatusOK {
			t.Fatalf("status=%d", st)
		}
		var data struct {
			Items []struct {
				Status string `json:"status"`
			} `json:"items"`
		}
		mustJSON(t, env.Data, &data)
		for _, it := range data.Items {
			if it.Status != "succeeded" {
				t.Fatalf("item status=%q, want succeeded", it.Status)
			}
		}
		if len(data.Items) == 0 {
			t.Fatal("expected at least one succeeded task")
		}
	})

	t.Run("page_below_one_clamped", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks?page=0")
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		var data struct {
			Page int `json:"page"`
		}
		mustJSON(t, env.Data, &data)
		if data.Page != 1 {
			t.Fatalf("page = %d, want clamped to 1", data.Page)
		}
	})

	t.Run("invalid_page_size_400", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks?page_size=abc")
		if st != http.StatusBadRequest || env.Code != "invalid_input" {
			t.Fatalf("status=%d code=%v, want 400 invalid_input", st, env.Code)
		}
	})

	t.Run("invalid_status_400", func(t *testing.T) {
		// queued is a version-only status; must be rejected, not silently empty.
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks?status=queued")
		if st != http.StatusBadRequest || env.Code != "invalid_input" {
			t.Fatalf("status=%d code=%v, want 400 invalid_input", st, env.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// GET /tasks/{id}
// ---------------------------------------------------------------------------

func TestReadTaskDetail(t *testing.T) {
	s := newSuite(t)

	t.Run("happy_with_current_version_and_zero_cost", func(t *testing.T) {
		taskID, versionID := createTask(t, s, "code-gen", "x")
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String())
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		var data struct {
			Task struct {
				ID uuid.UUID `json:"id"`
			} `json:"task"`
			CurrentVersion *struct {
				ID uuid.UUID `json:"id"`
			} `json:"current_version"`
			Cost costJSON `json:"cost"`
		}
		mustJSON(t, env.Data, &data)
		if data.Task.ID != taskID {
			t.Fatalf("task.id = %s, want %s", data.Task.ID, taskID)
		}
		if data.CurrentVersion == nil || data.CurrentVersion.ID != versionID {
			t.Fatalf("current_version = %+v, want id %s", data.CurrentVersion, versionID)
		}
		if data.Cost.AmountUSD != "0.00000000" {
			t.Fatalf("cost.amount_usd = %q, want \"0.00000000\"", data.Cost.AmountUSD)
		}
	})

	t.Run("same_tenant_different_user_404", func(t *testing.T) {
		foreign := insertTaskRow(t, s, s.devTenantID, uuid.Must(uuid.NewV7()), "pending")
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+foreign.String())
		if st != http.StatusNotFound || env.Code != "task_not_found" {
			t.Fatalf("status=%d code=%v, want 404 task_not_found", st, env.Code)
		}
	})

	t.Run("unknown_task_404", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+uuid.Must(uuid.NewV7()).String())
		if st != http.StatusNotFound || env.Code != "task_not_found" {
			t.Fatalf("status=%d code=%v", st, env.Code)
		}
	})

	t.Run("malformed_id_400", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/tasks/not-a-uuid")
		if st != http.StatusBadRequest || env.Code != "invalid_input" {
			t.Fatalf("status=%d code=%v", st, env.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// GET /tasks/{id}/versions
// ---------------------------------------------------------------------------

func TestReadVersionTree(t *testing.T) {
	s := newSuite(t)
	taskID, v1 := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "oss://b/v1/")
	v2 := iterate(t, s, taskID, "v2")
	insertVersionCost(t, s, taskID, v1, "0.62")

	st, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/versions")
	if st != http.StatusOK {
		t.Fatalf("status=%d env=%+v", st, env)
	}
	var data struct {
		Items []struct {
			ID        uuid.UUID  `json:"id"`
			ParentID  *uuid.UUID `json:"parent_id"`
			VersionNo int32      `json:"version_no"`
			IsActive  bool       `json:"is_active"`
			Cost      costJSON   `json:"cost"`
		} `json:"items"`
	}
	mustJSON(t, env.Data, &data)
	if len(data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(data.Items))
	}
	// version_no ascending; v1 root (parent null), v2 child of v1.
	if data.Items[0].VersionNo != 1 || data.Items[0].ParentID != nil {
		t.Fatalf("node0 = %+v, want version_no 1 / parent null", data.Items[0])
	}
	if data.Items[1].VersionNo != 2 || data.Items[1].ParentID == nil || *data.Items[1].ParentID != v1 {
		t.Fatalf("node1 = %+v, want version_no 2 / parent %s", data.Items[1], v1)
	}
	// batched cost mapped onto the right node.
	if data.Items[0].Cost.AmountUSD != "0.62000000" {
		t.Fatalf("v1 cost = %q, want \"0.62000000\"", data.Items[0].Cost.AmountUSD)
	}
	if data.Items[1].Cost.AmountUSD != "0.00000000" {
		t.Fatalf("v2 cost = %q, want zero", data.Items[1].Cost.AmountUSD)
	}
	_ = v2
}

// ---------------------------------------------------------------------------
// GET /versions/{id}
// ---------------------------------------------------------------------------

func TestReadVersionDetail(t *testing.T) {
	s := newSuite(t)

	t.Run("runs_prompt_params_and_cost", func(t *testing.T) {
		taskID, v1 := createTask(t, s, "code-gen", "the-prompt")
		insertVersionCost(t, s, taskID, v1, "0.62")
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String())
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		var data struct {
			Version struct {
				ID     uuid.UUID       `json:"id"`
				Prompt string          `json:"prompt"`
				Params json.RawMessage `json:"params"`
			} `json:"version"`
			Runs []struct {
				AttemptNo int32           `json:"attempt_no"`
				Status    string          `json:"status"`
				Error     json.RawMessage `json:"error"`
			} `json:"runs"`
			Cost costJSON `json:"cost"`
		}
		mustJSON(t, env.Data, &data)
		if data.Version.ID != v1 || data.Version.Prompt != "the-prompt" {
			t.Fatalf("version = %+v", data.Version)
		}
		if string(data.Version.Params) != "{}" {
			t.Fatalf("params raw = %s, want {}", data.Version.Params)
		}
		if len(data.Runs) != 1 || data.Runs[0].AttemptNo != 1 || data.Runs[0].Status != "queued" {
			t.Fatalf("runs = %+v, want one queued attempt 1", data.Runs)
		}
		if string(data.Runs[0].Error) != "null" {
			t.Fatalf("run error raw = %s, want null", data.Runs[0].Error)
		}
		if data.Cost.AmountUSD != "0.62000000" {
			t.Fatalf("cost = %q, want \"0.62000000\"", data.Cost.AmountUSD)
		}
	})

	t.Run("empty_runs_is_array_not_null", func(t *testing.T) {
		taskID, _ := createTask(t, s, "code-gen", "p")
		// a sibling terminal version with no runs.
		v2 := insertVersionRow(t, s, taskID, nil, 2, "succeeded")
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v2.String())
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		if !strings.Contains(string(env.Data), `"runs":[]`) {
			t.Fatalf("runs must be [] not null; data=%s", env.Data)
		}
	})

	t.Run("unknown_version_404", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+uuid.Must(uuid.NewV7()).String())
		if st != http.StatusNotFound || env.Code != "version_not_found" {
			t.Fatalf("status=%d code=%v", st, env.Code)
		}
	})

	t.Run("version_under_foreign_task_404", func(t *testing.T) {
		foreignTask := insertTaskRow(t, s, s.devTenantID, uuid.Must(uuid.NewV7()), "pending")
		foreignVer := insertVersionRow(t, s, foreignTask, nil, 1, "succeeded")
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+foreignVer.String())
		if st != http.StatusNotFound || env.Code != "version_not_found" {
			t.Fatalf("status=%d code=%v, want 404 version_not_found (not 403)", st, env.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// GET /versions/{id}/events
// ---------------------------------------------------------------------------

func TestReadVersionEvents(t *testing.T) {
	s := newSuite(t)
	taskID, v1 := createTask(t, s, "code-gen", "p")
	// a second version under the same task, to prove scoping.
	v2 := insertVersionRow(t, s, taskID, nil, 2, "succeeded")

	run := uuid.Must(uuid.NewV7())
	otherRun := uuid.Must(uuid.NewV7())
	// events on v1 (with a run), plus one with NULL run_id, plus one on v2.
	// (run_id, seq) is globally unique, so the v2 event uses its own run.
	e1 := insertEventRow(t, s, taskID, v1, &run, 1, "status")
	e2 := insertEventRow(t, s, taskID, v1, &run, 2, "log")
	e3 := insertEventRow(t, s, taskID, v1, nil, 3, "log")    // null run_id
	insertEventRow(t, s, taskID, v2, &otherRun, 1, "status") // other version

	t.Run("backfill_after_cursor_and_scoping", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/events?after_id="+itoa(e1))
		if st != http.StatusOK {
			t.Fatalf("status=%d env=%+v", st, env)
		}
		var data struct {
			Items []struct {
				ID        int64      `json:"id"`
				VersionID uuid.UUID  `json:"version_id"`
				RunID     *uuid.UUID `json:"run_id"`
				Seq       int64      `json:"seq"`
			} `json:"items"`
			NextAfterID int64 `json:"next_after_id"`
		}
		mustJSON(t, env.Data, &data)
		if len(data.Items) != 2 {
			t.Fatalf("items = %d, want 2 (e2,e3 after e1)", len(data.Items))
		}
		for _, it := range data.Items {
			if it.VersionID != v1 {
				t.Fatalf("event version_id = %s, want %s (scoping)", it.VersionID, v1)
			}
		}
		if data.NextAfterID != e3 {
			t.Fatalf("next_after_id = %d, want %d", data.NextAfterID, e3)
		}
		// e3 has a null run_id.
		if data.Items[1].ID != e3 || data.Items[1].RunID != nil {
			t.Fatalf("e3 = %+v, want id %d / run_id null", data.Items[1], e3)
		}
		_ = e2
	})

	t.Run("no_new_events_echoes_cursor", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/events?after_id="+itoa(e3))
		if st != http.StatusOK {
			t.Fatalf("status=%d", st)
		}
		var data struct {
			Items       []json.RawMessage `json:"items"`
			NextAfterID int64             `json:"next_after_id"`
		}
		mustJSON(t, env.Data, &data)
		if len(data.Items) != 0 || data.NextAfterID != e3 {
			t.Fatalf("items=%d next=%d, want 0 / %d", len(data.Items), data.NextAfterID, e3)
		}
	})

	t.Run("invalid_after_id_400", func(t *testing.T) {
		st, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/events?after_id=abc")
		if st != http.StatusBadRequest || env.Code != "invalid_input" {
			t.Fatalf("status=%d code=%v", st, env.Code)
		}
	})
}

// mustJSON unmarshals or fails the test.
func mustJSON(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
