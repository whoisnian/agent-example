package cost

import "testing"

func TestResolveIncrements_LLM(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{
		Kind:         "llm",
		InputTokens:  i64(1000),
		OutputTokens: i64(200),
		CachedTokens: nil, // worker may send NULL
		DurationMs:   i64(4200),
	}
	inc := ResolveIncrements(ev)

	want := Increments{
		InputTokens:  1000,
		OutputTokens: 200,
		CachedTokens: 0, // NULL → 0
		ToolCalls:    0, // cross-kind: llm never increments tool_calls
		WallTimeMs:   4200,
		// ComputeSeconds stays 0 — only compute events feed it.
	}
	if inc != want {
		t.Errorf("LLM increments = %+v, want %+v", inc, want)
	}
}

func TestResolveIncrements_Tool(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{
		Kind:        "tool",
		Calls:       i32(3),
		DurationMs:  i64(150),
		InputTokens: i64(999), // cross-kind: must be ignored
	}
	inc := ResolveIncrements(ev)
	want := Increments{
		ToolCalls:    3,
		WallTimeMs:   150,
		InputTokens:  0, // gated to 0 for tool
		OutputTokens: 0,
	}
	if inc != want {
		t.Errorf("Tool increments = %+v, want %+v", inc, want)
	}
}

func TestResolveIncrements_Tool_NullCallsDefaultsToOne(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "tool"} // Calls nil
	inc := ResolveIncrements(ev)
	if inc.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1 (default when worker sends NULL)", inc.ToolCalls)
	}
}

func TestResolveIncrements_Compute_SubSecondTruncatesToZero(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "compute", DurationMs: i64(800)}
	inc := ResolveIncrements(ev)
	if inc.ComputeSeconds != 0 {
		t.Errorf("ComputeSeconds = %d, want 0 (floor(800/1000))", inc.ComputeSeconds)
	}
	if inc.WallTimeMs != 0 {
		t.Errorf("WallTimeMs = %d, want 0 (compute events don't feed wall_time_ms)", inc.WallTimeMs)
	}
}

func TestResolveIncrements_Compute_WholeSecondsFloors(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "compute", DurationMs: i64(1500)}
	inc := ResolveIncrements(ev)
	if inc.ComputeSeconds != 1 {
		t.Errorf("ComputeSeconds = %d, want 1 (floor(1500/1000))", inc.ComputeSeconds)
	}
}

func TestResolveIncrements_LLM_DoesNotTouchComputeSeconds(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "llm", DurationMs: i64(10000)}
	inc := ResolveIncrements(ev)
	if inc.ComputeSeconds != 0 {
		t.Errorf("ComputeSeconds = %d, want 0 (llm events route duration to wall_time_ms only)", inc.ComputeSeconds)
	}
	if inc.WallTimeMs != 10000 {
		t.Errorf("WallTimeMs = %d, want 10000", inc.WallTimeMs)
	}
}

func TestResolveIncrements_UnknownKindAllZero(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "bogus", InputTokens: i64(999), DurationMs: i64(999)}
	if (ResolveIncrements(ev)) != (Increments{}) {
		t.Errorf("expected zero Increments for unknown kind")
	}
}
