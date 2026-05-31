package task

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidGroupBy / ErrInvalidWindow are domain-layer validation errors
// surfaced to the HTTP layer as 400 invalid_input with the field name. They
// live next to the helpers because the helpers are the only producers.
var (
	// ErrInvalidGroupBy signals the `group_by` value is not one of the three
	// accepted names. Empty is NOT an error — the caller treats empty as
	// "absent" (matches the task_reads.go convention for `status`).
	ErrInvalidGroupBy = errors.New("invalid group_by")
	// ErrInvalidWindow signals from >= to, or — when groupBy is present —
	// to - from > maxOwnerCostWindow.
	ErrInvalidWindow = errors.New("invalid time window")
)

// maxOwnerCostWindow is the cardinality cap for /me/cost grouped queries
// (S7). 366 days because the default-window default of 30 days leaves
// massive headroom; nobody has a legitimate "show me 5 years of buckets"
// use case in MVP.
const maxOwnerCostWindow = 366 * 24 * time.Hour

// defaultOwnerCostLookback is the implicit `from = to - 30d` when a grouped
// /me/cost request supplies neither `from` nor `to`. The choice tracks
// typical "recent dashboard" UX rather than any DB-side limit.
const defaultOwnerCostLookback = 30 * 24 * time.Hour

// ParseGroupBy validates the `group_by` query value. An empty string MUST
// be treated as "absent": the caller distinguishes the no-group_by branch
// by checking `present := raw != ""` BEFORE calling this helper. Any
// non-empty value outside {day, task_type, model} is ErrInvalidGroupBy.
func ParseGroupBy(raw string) (string, error) {
	switch raw {
	case GroupByDay, GroupByTaskType, GroupByModel:
		return raw, nil
	case "":
		return "", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidGroupBy, raw)
	}
}

// ApplyWindowDefaults applies the /me/cost defaulting rules:
//   - When groupBy is absent (empty), from/to are passthroughs (NULL means
//     unbounded — the SQL handles it).
//   - When groupBy is present AND both from and to are nil, default
//     to = now() and from = to - 30d (so a no-arg ?group_by=day returns ≤ 31
//     buckets, not the entire history).
//   - When groupBy is present AND only one of from/to is nil, the other one
//     stays nil — we don't second-guess the caller's intent (they may
//     legitimately want "everything before / after a date").
//
// nowFn is the time source (production passes time.Now, tests pass a
// fixed clock). Returns the resolved (from, to) — pointer values are
// preserved as nil when no default applied.
func ApplyWindowDefaults(groupBy string, from, to *time.Time, nowFn func() time.Time) (resolvedFrom, resolvedTo *time.Time) {
	if groupBy == "" {
		return from, to
	}
	if from != nil || to != nil {
		return from, to
	}
	t := nowFn().UTC()
	f := t.Add(-defaultOwnerCostLookback)
	return &f, &t
}

// ValidateWindow enforces:
//   - from < to when both are present.
//   - to - from <= 366d when groupBy is present.
//
// `field` in the returned error is "to" so the HTTP layer can name the
// failing query param. groupBy="" means "no group_by" — the cap doesn't
// apply.
func ValidateWindow(groupBy string, from, to *time.Time) error {
	if from != nil && to != nil {
		if !from.Before(*to) {
			return fmt.Errorf("%w: from >= to", ErrInvalidWindow)
		}
		if groupBy != "" && to.Sub(*from) > maxOwnerCostWindow {
			return fmt.Errorf("%w: to - from > %s (grouped query cap)", ErrInvalidWindow, maxOwnerCostWindow)
		}
	}
	return nil
}
