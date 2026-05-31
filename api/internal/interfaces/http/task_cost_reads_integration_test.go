//go:build integration

// HTTP-level integration tests for task-cost-api. Reuses the suite +
// helpers from tasks_integration_test.go (same package). Each top-level
// test owns one PostgreSQL container; subtests share that container
// against independently-keyed rows.
package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// JSON shapes mirroring the wire format
// ---------------------------------------------------------------------------

type taskCostDetailJSON struct {
	TaskID    string                       `json:"task_id"`
	Total     costJSON                     `json:"total"`
	ByVersion []taskCostVersionItemJSON    `json:"by_version"`
}

type taskCostVersionItemJSON struct {
	VersionID string   `json:"version_id"`
	VersionNo int32    `json:"version_no"`
	CreatedAt string   `json:"created_at"`
	Cost      costJSON `json:"cost"`
}

type versionCostDetailJSON struct {
	VersionID string   `json:"version_id"`
	TaskID    string   `json:"task_id"`
	VersionNo int32    `json:"version_no"`
	Cost      costJSON `json:"cost"`
	UpdatedAt *string  `json:"updated_at"`
}

type ownerCostTotalJSON struct {
	Total costJSON `json:"total"`
}

type ownerCostGroupedJSON struct {
	GroupBy string                  `json:"group_by"`
	Items   []ownerCostGroupRowJSON `json:"items"`
}

type ownerCostGroupRowJSON struct {
	Key    string   `json:"key"`
	Totals costJSON `json:"totals"`
}

type pricingListJSON struct {
	Items []pricingEntryJSON `json:"items"`
}

type pricingEntryJSON struct {
	ID            string  `json:"id"`
	ResourceKind  string  `json:"resource_kind"`
	ResourceName  string  `json:"resource_name"`
	Unit          string  `json:"unit"`
	UnitPriceUSD  string  `json:"unit_price_usd"`
	EffectiveAt   string  `json:"effective_at"`
	ExpiresAt     *string `json:"expires_at"`
}

// ---------------------------------------------------------------------------
// helpers that piggy-back on the existing suite + inserters
// ---------------------------------------------------------------------------

// insertCostEvent appends a cost_events row used by the /me/cost tests so
// the aggregates have something to add up. Caller supplies enough state
// for the cost-ingest invariants (run_id + seq unique per kind).
func insertCostEvent(t *testing.T, s *suite, taskID, versionID, runID uuid.UUID, seq int64, kind, resourceName, amountUSD string, occurredAt time.Time, inputTokens, outputTokens *int64, calls *int32, durationMs *int64) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO cost_events
		 (task_id, version_id, run_id, seq, kind, resource_name,
		  input_tokens, output_tokens, calls, duration_ms,
		  amount_usd, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::numeric, $12)`,
		taskID, versionID, runID, seq, kind, resourceName,
		inputTokens, outputTokens, calls, durationMs,
		amountUSD, occurredAt); err != nil {
		t.Fatalf("insert cost_event: %v", err)
	}
}

func insertTaskRun(t *testing.T, s *suite, versionID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
		 VALUES ($1, $2, 1, 'running', $3)`,
		id, versionID, "ik-"+id.String()); err != nil {
		t.Fatalf("insert task_run: %v", err)
	}
	return id
}

func mustJSONField(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode data: %v body=%s", err, raw)
	}
}

// ---------------------------------------------------------------------------
// /tasks/{id}/cost
// ---------------------------------------------------------------------------

func TestCostReadTaskDetail(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	t.Run("happy: two versions, totals sum across", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "succeeded")
		v2 := insertVersionRow(t, s, taskID, &v1, 2, "running")
		insertVersionCost(t, s, taskID, v1, "1.10000000")
		insertVersionCost(t, s, taskID, v2, "0.62000000")

		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got taskCostDetailJSON
		mustJSONField(t, env.Data, &got)
		if got.Total.AmountUSD != "1.72000000" {
			t.Errorf("total.amount_usd = %q, want 1.72000000", got.Total.AmountUSD)
		}
		if len(got.ByVersion) != 2 {
			t.Fatalf("by_version len=%d, want 2; got=%+v", len(got.ByVersion), got.ByVersion)
		}
		if got.ByVersion[0].VersionNo != 1 || got.ByVersion[1].VersionNo != 2 {
			t.Errorf("by_version not ordered by version_no asc: %+v", got.ByVersion)
		}
		if got.ByVersion[1].Cost.AmountUSD != "0.62000000" {
			t.Errorf("v2 cost = %q, want 0.62000000", got.ByVersion[1].Cost.AmountUSD)
		}
	})

	t.Run("owned task with no versions: zero total, empty by_version", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "pending")
		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got taskCostDetailJSON
		mustJSONField(t, env.Data, &got)
		if got.Total.AmountUSD != "0.00000000" {
			t.Errorf("total.amount_usd = %q, want 0.00000000", got.Total.AmountUSD)
		}
		// MUST be the empty array, not null. Decoding into []T forces []T{}
		// only if "by_version": [] appears literally; check raw bytes.
		var probe struct {
			ByVersion json.RawMessage `json:"by_version"`
		}
		mustJSONField(t, env.Data, &probe)
		if strings.TrimSpace(string(probe.ByVersion)) == "null" {
			t.Errorf("by_version rendered as null; want []")
		}
	})

	t.Run("version without cost row: zero-fill, version still appears", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "pending")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "pending")
		// no insertVersionCost — so the LEFT JOIN should still yield v1
		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got taskCostDetailJSON
		mustJSONField(t, env.Data, &got)
		if len(got.ByVersion) != 1 || got.ByVersion[0].VersionID != v1.String() {
			t.Errorf("expected v1 in by_version even without cost row, got %+v", got.ByVersion)
		}
		if got.ByVersion[0].Cost.AmountUSD != "0.00000000" {
			t.Errorf("v1 zero-fill cost = %q, want 0.00000000", got.ByVersion[0].Cost.AmountUSD)
		}
	})

	t.Run("unowned task: 404 task_not_found", func(t *testing.T) {
		otherUser := uuid.Must(uuid.NewV7())
		taskID := insertTaskRow(t, s, s.devTenantID, otherUser, "pending")
		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+taskID.String()+"/cost")
		if status != http.StatusNotFound {
			t.Errorf("status=%d, want 404 env=%+v", status, env)
		}
		if env.Code != "task_not_found" {
			t.Errorf("code=%v, want task_not_found", env.Code)
		}
	})

	t.Run("unknown task: 404 task_not_found", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())
		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/"+unknown.String()+"/cost")
		if status != http.StatusNotFound {
			t.Errorf("status=%d, want 404 env=%+v", status, env)
		}
		if env.Code != "task_not_found" {
			t.Errorf("code=%v, want task_not_found", env.Code)
		}
	})

	t.Run("malformed UUID: 400 invalid_input", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/tasks/not-a-uuid/cost")
		if status != http.StatusBadRequest {
			t.Errorf("status=%d, want 400 env=%+v", status, env)
		}
	})
}

// ---------------------------------------------------------------------------
// /versions/{id}/cost
// ---------------------------------------------------------------------------

func TestCostReadVersionDetail(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	t.Run("happy: version cost + owning task_id", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "succeeded")
		insertVersionCost(t, s, taskID, v1, "0.06750000")

		status, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got versionCostDetailJSON
		mustJSONField(t, env.Data, &got)
		if got.TaskID != taskID.String() {
			t.Errorf("task_id = %q, want %q", got.TaskID, taskID)
		}
		if got.Cost.AmountUSD != "0.06750000" {
			t.Errorf("cost.amount_usd = %q, want 0.06750000", got.Cost.AmountUSD)
		}
		if got.UpdatedAt == nil {
			t.Errorf("updated_at should not be nil after a cost insert")
		}
	})

	t.Run("version with no cost row: zero-fill, updated_at null", func(t *testing.T) {
		taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "pending")
		status, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got versionCostDetailJSON
		mustJSONField(t, env.Data, &got)
		if got.Cost.AmountUSD != "0.00000000" {
			t.Errorf("cost.amount_usd = %q, want 0.00000000", got.Cost.AmountUSD)
		}
		if got.UpdatedAt != nil {
			t.Errorf("updated_at = %v, want nil for empty cost row", *got.UpdatedAt)
		}
	})

	t.Run("unknown version: 404 version_not_found", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())
		status, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+unknown.String()+"/cost")
		if status != http.StatusNotFound {
			t.Errorf("status=%d, want 404 env=%+v", status, env)
		}
		if env.Code != "version_not_found" {
			t.Errorf("code=%v, want version_not_found", env.Code)
		}
	})

	t.Run("unowned version: 404 version_not_found", func(t *testing.T) {
		otherUser := uuid.Must(uuid.NewV7())
		taskID := insertTaskRow(t, s, s.devTenantID, otherUser, "running")
		v1 := insertVersionRow(t, s, taskID, nil, 1, "running")
		status, env := getJSON(t, s.ts.URL+"/api/v1/versions/"+v1.String()+"/cost")
		if status != http.StatusNotFound {
			t.Errorf("status=%d, want 404 env=%+v", status, env)
		}
		if env.Code != "version_not_found" {
			t.Errorf("code=%v, want version_not_found", env.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// /me/cost
// ---------------------------------------------------------------------------

func TestCostReadOwnerCost(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	taskID := insertTaskRow(t, s, s.devTenantID, s.devUserID, "running")
	v1 := insertVersionRow(t, s, taskID, nil, 1, "running")
	run := insertTaskRun(t, s, v1)
	day1 := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 30, 23, 30, 0, 0, time.UTC) // late UTC — would flip days in non-UTC sessions
	tok := func(v int64) *int64 { x := v; return &x }
	insertCostEvent(t, s, taskID, v1, run, 1, "llm", "claude-opus-4-7", "1.00000000", day1, tok(1000), tok(200), nil, tok(4200))
	insertCostEvent(t, s, taskID, v1, run, 1, "tool", "oss_fs", "0.50000000", day2, nil, nil, ptrInt32(3), tok(150))

	t.Run("no group_by: totals add up", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostTotalJSON
		mustJSONField(t, env.Data, &got)
		if got.Total.AmountUSD != "1.50000000" {
			t.Errorf("total.amount_usd = %q, want 1.50000000", got.Total.AmountUSD)
		}
		if got.Total.ToolCalls != 3 {
			t.Errorf("tool_calls = %d, want 3", got.Total.ToolCalls)
		}
		// wall_time_ms accumulates both llm + tool durations.
		if got.Total.WallTimeMs != 4350 {
			t.Errorf("wall_time_ms = %d, want 4350", got.Total.WallTimeMs)
		}
	})

	t.Run("group_by=day: UTC boundary lands correctly", func(t *testing.T) {
		q := url.Values{}
		q.Set("group_by", "day")
		q.Set("from", "2026-05-29T00:00:00Z")
		q.Set("to", "2026-05-31T00:00:00Z")
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?"+q.Encode())
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostGroupedJSON
		mustJSONField(t, env.Data, &got)
		if got.GroupBy != "day" {
			t.Errorf("group_by echo = %q", got.GroupBy)
		}
		if len(got.Items) != 2 {
			t.Fatalf("items len = %d, want 2 (one per day)", len(got.Items))
		}
		if got.Items[0].Key != "2026-05-29" || got.Items[1].Key != "2026-05-30" {
			t.Errorf("day keys = %q + %q, want 2026-05-29 + 2026-05-30 (UTC-pinned)", got.Items[0].Key, got.Items[1].Key)
		}
	})

	t.Run("group_by=model: collapses non-llm into 'other'", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?group_by=model")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostGroupedJSON
		mustJSONField(t, env.Data, &got)
		keys := map[string]bool{}
		for _, it := range got.Items {
			keys[it.Key] = true
		}
		if !keys["claude-opus-4-7"] || !keys["other"] {
			t.Errorf("model buckets missing expected keys: %+v", keys)
		}
	})

	t.Run("group_by=task_type: collapses to task_type", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?group_by=task_type")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostGroupedJSON
		mustJSONField(t, env.Data, &got)
		if len(got.Items) != 1 || got.Items[0].Key != "code-gen" {
			t.Errorf("expected single code-gen bucket, got %+v", got.Items)
		}
	})

	t.Run("right-exclusive `to`: same instant excludes", func(t *testing.T) {
		// to = day2 exactly → day2's event should be excluded
		q := url.Values{}
		q.Set("from", "2026-05-29T00:00:00Z")
		q.Set("to", day2.Format(time.RFC3339))
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?"+q.Encode())
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostTotalJSON
		mustJSONField(t, env.Data, &got)
		// only day1 contributes
		if got.Total.AmountUSD != "1.00000000" {
			t.Errorf("right-exclusive total = %q, want 1.00000000", got.Total.AmountUSD)
		}
	})

	t.Run("invalid group_by: 400", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?group_by=hour")
		if status != http.StatusBadRequest {
			t.Errorf("status=%d, want 400 env=%+v", status, env)
		}
	})

	t.Run("from >= to: 400", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?from=2026-05-30T00:00:00Z&to=2026-05-29T00:00:00Z")
		if status != http.StatusBadRequest {
			t.Errorf("status=%d, want 400 env=%+v", status, env)
		}
	})

	t.Run("window cap exceeded for grouped: 400", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost?group_by=day&from=2024-01-01T00:00:00Z&to=2026-05-31T00:00:00Z")
		if status != http.StatusBadRequest {
			t.Errorf("status=%d, want 400 env=%+v", status, env)
		}
	})

	t.Run("cross-owner isolation: another user's events do not contribute", func(t *testing.T) {
		// Add a competing user's event with an enormous amount.
		otherUser := uuid.Must(uuid.NewV7())
		oTask := insertTaskRow(t, s, s.devTenantID, otherUser, "running")
		oVer := insertVersionRow(t, s, oTask, nil, 1, "running")
		oRun := insertTaskRun(t, s, oVer)
		insertCostEvent(t, s, oTask, oVer, oRun, 1, "llm", "claude-opus-4-7", "1000.00000000", day1, tok(99999), tok(99999), nil, tok(0))

		status, env := getJSON(t, s.ts.URL+"/api/v1/me/cost")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got ownerCostTotalJSON
		mustJSONField(t, env.Data, &got)
		if got.Total.AmountUSD != "1.50000000" {
			t.Errorf("other user leaked through: amount=%q", got.Total.AmountUSD)
		}
	})
}

// ---------------------------------------------------------------------------
// /pricing
// ---------------------------------------------------------------------------

func TestCostReadPricing(t *testing.T) {
	t.Parallel()
	s := newSuite(t)

	t.Run("returns seeded rows, sorted, decimal-string", func(t *testing.T) {
		status, env := getJSON(t, s.ts.URL+"/api/v1/pricing")
		if status != http.StatusOK {
			t.Fatalf("status=%d env=%+v", status, env)
		}
		var got pricingListJSON
		mustJSONField(t, env.Data, &got)
		if len(got.Items) == 0 {
			t.Fatalf("pricing list empty — expected seed rows from 0005 migration")
		}

		// Verify sort by (resource_kind, resource_name, unit) ascending.
		for i := 1; i < len(got.Items); i++ {
			a, b := got.Items[i-1], got.Items[i]
			ka := a.ResourceKind + "\x00" + a.ResourceName + "\x00" + a.Unit
			kb := b.ResourceKind + "\x00" + b.ResourceName + "\x00" + b.Unit
			if ka > kb {
				t.Errorf("pricing not sorted at index %d: %q > %q", i, ka, kb)
			}
		}

		// At least one row has the opus input-tokens shape with scale-8 string.
		found := false
		for _, it := range got.Items {
			if it.ResourceKind == "llm" && it.ResourceName == "claude-opus-4-7" && it.Unit == "per_1k_input_tokens" {
				if it.UnitPriceUSD != "0.01500000" {
					t.Errorf("opus input price = %q, want 0.01500000", it.UnitPriceUSD)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("expected opus per_1k_input_tokens row in pricing list")
		}
	})

	// S15: pricing is owner-agnostic — every caller sees the same body. The
	// dev-mode middleware in this suite always injects the same identity, so
	// we can't truly fork callers without restructuring the harness. We
	// approximate the spec assertion by issuing two consecutive requests and
	// confirming the bodies are byte-identical.
	t.Run("identical bodies across requests (owner-agnostic)", func(t *testing.T) {
		s1, e1 := getJSON(t, s.ts.URL+"/api/v1/pricing")
		s2, e2 := getJSON(t, s.ts.URL+"/api/v1/pricing")
		if s1 != http.StatusOK || s2 != http.StatusOK {
			t.Fatalf("status=%d/%d", s1, s2)
		}
		// Compare the data bytes — trace_id varies per request so we ignore
		// the envelope and assert on the data payload only.
		if string(e1.Data) != string(e2.Data) {
			t.Errorf("pricing response bodies differ across requests; want byte-identical data\nfirst:  %s\nsecond: %s", e1.Data, e2.Data)
		}
	})
}

// ---------------------------------------------------------------------------
// silence unused-import warnings for symbols the helpers reference
// transitively
// ---------------------------------------------------------------------------

var _ = strconv.Itoa
var _ = fmt.Sprintf

func ptrInt32(v int32) *int32 { return &v }
