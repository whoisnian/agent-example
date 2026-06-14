//go:build integration

// HTTP-level integration tests for add-task-deletion (DELETE /tasks/{id}).
// Reuses the suite + helpers from tasks_integration_test.go and
// task_reads_integration_test.go (insertTaskRow / insertVersionRow /
// insertVersionCost / getJSON). Each top-level test owns one PostgreSQL
// container.
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// deleteJSON issues an authenticated DELETE and returns (status, envelope).
func deleteJSON(t *testing.T, url string) (int, envelope) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, http.NoBody)
	if err != nil {
		t.Fatalf("build DELETE %s: %v", url, err)
	}
	req.Header.Set("Authorization", intgAuthHeader())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return resp.StatusCode, env
}

func deletedAtNull(t *testing.T, s *suite, taskID uuid.UUID) bool {
	t.Helper()
	var isNull bool
	if err := s.pool.QueryRow(context.Background(),
		"SELECT deleted_at IS NULL FROM tasks WHERE id = $1", taskID).Scan(&isNull); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	return isNull
}

// TestDeleteTaskHappy: a non-active task soft-deletes (200), its version/cost
// rows are retained, and it then vanishes from list/detail/versions.
func TestDeleteTaskHappy(t *testing.T) {
	s := newSuite(t)
	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "succeeded")
	versionID := insertVersionRow(t, s, taskID, nil, 1, "succeeded")
	insertVersionCost(t, s, taskID, versionID, "1.50")

	status, env := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String())
	if status != http.StatusOK {
		t.Fatalf("status=%d env=%+v", status, env)
	}
	var data struct {
		Deleted bool      `json:"deleted"`
		TaskID  uuid.UUID `json:"task_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v body=%s", err, env.Data)
	}
	if !data.Deleted || data.TaskID != taskID {
		t.Fatalf("unexpected delete payload: %+v", data)
	}

	// deleted_at set; version + cost rows retained (audit / settlement integrity).
	if deletedAtNull(t, s, taskID) {
		t.Fatal("deleted_at is still NULL after delete")
	}
	ctx := context.Background()
	var versionCount, costCount int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM task_versions WHERE id = $1", versionID).Scan(&versionCount); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM task_costs WHERE version_id = $1", versionID).Scan(&costCount); err != nil {
		t.Fatalf("count costs: %v", err)
	}
	if versionCount != 1 || costCount != 1 {
		t.Fatalf("rows not retained: version=%d cost=%d", versionCount, costCount)
	}

	// list excludes it (items + total)
	st, listEnv := getJSON(t, s.ts.URL+"/api/v1/tasks")
	if st != http.StatusOK {
		t.Fatalf("list status=%d", st)
	}
	var list struct {
		Items []struct {
			ID uuid.UUID `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(listEnv.Data, &list); err != nil {
		t.Fatalf("list unmarshal: %v", err)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("soft-deleted task still listed: total=%d items=%d", list.Total, len(list.Items))
	}

	// detail + versions → 404 task_not_found
	if st, e := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()); st != http.StatusNotFound || e.Code != "task_not_found" {
		t.Fatalf("detail after delete: status=%d code=%v", st, e.Code)
	}
	if st, e := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/versions"); st != http.StatusNotFound || e.Code != "task_not_found" {
		t.Fatalf("versions after delete: status=%d code=%v", st, e.Code)
	}
	// version-by-id reachable through the deleted task → 404 (version_not_found
	// per the ownedVersion contract).
	if st, e := getJSON(t, s.ts.URL+"/api/v1/versions/"+versionID.String()); st != http.StatusNotFound || e.Code != "version_not_found" {
		t.Fatalf("version-by-id after delete: status=%d code=%v", st, e.Code)
	}
}

// TestDeleteActiveTaskRejected: a task with an active version cannot be deleted.
func TestDeleteActiveTaskRejected(t *testing.T) {
	s := newSuite(t)
	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
	activeID := insertVersionRow(t, s, taskID, nil, 1, "running")

	status, env := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String())
	if status != http.StatusConflict || env.Code != "active_version_exists" {
		t.Fatalf("status=%d code=%v", status, env.Code)
	}
	var data struct {
		ActiveVersionID     uuid.UUID `json:"active_version_id"`
		ActiveVersionStatus string    `json:"active_version_status"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v body=%s", err, env.Data)
	}
	if data.ActiveVersionID != activeID || data.ActiveVersionStatus != "running" {
		t.Fatalf("conflict data: %+v (want %s/running)", data, activeID)
	}
	if !deletedAtNull(t, s, taskID) {
		t.Fatal("deleted_at was set despite active-version rejection")
	}
}

// TestDeleteTaskNotFoundAndIdempotent: unknown / unowned / already-deleted all
// 404 task_not_found; a repeat delete leaves the original deleted_at unchanged.
func TestDeleteTaskNotFoundAndIdempotent(t *testing.T) {
	s := newSuite(t)

	// unknown id
	if st, e := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+uuid.Must(uuid.NewV7()).String()); st != http.StatusNotFound || e.Code != "task_not_found" {
		t.Fatalf("unknown: status=%d code=%v", st, e.Code)
	}

	// unowned (different tenant/user) → 404, not 403
	otherTenant := uuid.Must(uuid.NewV7())
	otherUser := uuid.Must(uuid.NewV7())
	unowned := insertTaskRow(t, s, otherTenant, otherUser, "failed")
	if st, e := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+unowned.String()); st != http.StatusNotFound || e.Code != "task_not_found" {
		t.Fatalf("unowned: status=%d code=%v", st, e.Code)
	}

	// first delete OK, second delete → 404 and deleted_at unchanged (idempotent)
	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "failed")
	if st, _ := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()); st != http.StatusOK {
		t.Fatalf("first delete status=%d", st)
	}
	var firstTS string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT deleted_at::text FROM tasks WHERE id = $1", taskID).Scan(&firstTS); err != nil {
		t.Fatalf("read first deleted_at: %v", err)
	}
	if st, e := deleteJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()); st != http.StatusNotFound || e.Code != "task_not_found" {
		t.Fatalf("second delete: status=%d code=%v", st, e.Code)
	}
	var secondTS string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT deleted_at::text FROM tasks WHERE id = $1", taskID).Scan(&secondTS); err != nil {
		t.Fatalf("read second deleted_at: %v", err)
	}
	if firstTS != secondTS {
		t.Fatalf("deleted_at changed on repeat delete: %q -> %q", firstTS, secondTS)
	}
}
