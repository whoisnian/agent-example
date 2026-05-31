package cost

// Increments is the per-event resolved set of task_costs column deltas.
// Maps 1:1 to UpsertVersionCostParams's aggregate fields (ToolCalls /
// WallTimeMs / ComputeSeconds / *Tokens). The settler computes this once
// per event per the spec's "Aggregate Increment Mapping Per Kind" table
// and binds the result to the UPSERT.
type Increments struct {
	InputTokens    int64
	OutputTokens   int64
	CachedTokens   int64
	ToolCalls      int32
	WallTimeMs     int64
	ComputeSeconds int64
}

// ResolveIncrements applies the per-kind mapping table from
// task-cost-ingest §"Aggregate Increment Mapping Per Kind". Worker-supplied
// NULLs are coerced to 0; cross-kind columns are gated to 0 (an llm event
// never increments tool_calls/compute_seconds, etc.). compute_seconds is
// floor(duration_ms / 1000) for compute events only — sub-second
// durations contribute 0 to the integer aggregate while still contributing
// exact amount_usd via ComputeAmount.
func ResolveIncrements(ev *CostEventInput) Increments {
	var inc Increments
	switch ev.Kind {
	case "llm":
		inc.InputTokens = derefInt64(ev.InputTokens)
		inc.OutputTokens = derefInt64(ev.OutputTokens)
		inc.CachedTokens = derefInt64(ev.CachedTokens)
		inc.WallTimeMs = derefInt64(ev.DurationMs)
	case "tool":
		// tool_calls counts invocations regardless of whether a per_call
		// price matched (per spec: "the aggregate stays a faithful
		// invocation counter even when pricing is missing"). The
		// worker's emit_tool always sends calls=1; if the field is NULL
		// we default to 1 as well.
		c := int32(1)
		if ev.Calls != nil {
			c = *ev.Calls
		}
		inc.ToolCalls = c
		inc.WallTimeMs = derefInt64(ev.DurationMs)
	case "compute":
		inc.ComputeSeconds = derefInt64(ev.DurationMs) / 1000
	}
	return inc
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
