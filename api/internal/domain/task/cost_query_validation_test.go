package task

import (
	"errors"
	"testing"
	"time"
)

func TestParseGroupBy_Valid(t *testing.T) {
	t.Parallel()
	for _, v := range []string{GroupByDay, GroupByTaskType, GroupByModel} {
		got, err := ParseGroupBy(v)
		if err != nil {
			t.Errorf("ParseGroupBy(%q) err = %v", v, err)
		}
		if got != v {
			t.Errorf("ParseGroupBy(%q) = %q", v, got)
		}
	}
}

func TestParseGroupBy_EmptyIsAbsent(t *testing.T) {
	t.Parallel()
	got, err := ParseGroupBy("")
	if err != nil {
		t.Errorf("empty group_by should be no-op, got err = %v", err)
	}
	if got != "" {
		t.Errorf("empty group_by should round-trip empty, got %q", got)
	}
}

func TestParseGroupBy_Unknown(t *testing.T) {
	t.Parallel()
	_, err := ParseGroupBy("hour")
	if !errors.Is(err, ErrInvalidGroupBy) {
		t.Errorf("expected ErrInvalidGroupBy, got %v", err)
	}
}

func TestApplyWindowDefaults_NoGroupByPassthrough(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	f, ttv := ApplyWindowDefaults("", nil, nil, func() time.Time { return now })
	if f != nil || ttv != nil {
		t.Errorf("no group_by + no bounds should stay nil, got (%v, %v)", f, ttv)
	}
}

func TestApplyWindowDefaults_GroupByNoBoundsAppliesDefault(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	f, ttv := ApplyWindowDefaults(GroupByDay, nil, nil, func() time.Time { return now })
	if f == nil || ttv == nil {
		t.Fatalf("expected defaults applied, got (%v, %v)", f, ttv)
	}
	if !ttv.Equal(now) {
		t.Errorf("to default should be now(), got %v", ttv)
	}
	want := now.Add(-defaultOwnerCostLookback)
	if !f.Equal(want) {
		t.Errorf("from default should be now-30d, got %v want %v", f, want)
	}
}

func TestApplyWindowDefaults_GroupByPartialBoundsPassthrough(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	explicit := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Only from supplied; to stays nil (caller wants "everything since X").
	f, ttv := ApplyWindowDefaults(GroupByDay, &explicit, nil, func() time.Time { return now })
	if f == nil || !f.Equal(explicit) {
		t.Errorf("from should round-trip explicit value, got %v", f)
	}
	if ttv != nil {
		t.Errorf("to should stay nil when only from supplied, got %v", ttv)
	}
}

func TestValidateWindow_HappyPath(t *testing.T) {
	t.Parallel()
	f := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	if err := ValidateWindow(GroupByDay, &f, &to); err != nil {
		t.Errorf("30-day window should validate, got %v", err)
	}
}

func TestValidateWindow_FromAfterToRejected(t *testing.T) {
	t.Parallel()
	f := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	err := ValidateWindow("", &f, &to)
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("from >= to should fail, got %v", err)
	}
}

func TestValidateWindow_EqualBoundsRejected(t *testing.T) {
	t.Parallel()
	tt := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	err := ValidateWindow("", &tt, &tt)
	if !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("from == to should fail (Before is strict), got %v", err)
	}
}

func TestValidateWindow_CapEnforcedOnlyWhenGrouped(t *testing.T) {
	t.Parallel()
	f := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC) // > 366d
	// No group_by → cap does NOT apply.
	if err := ValidateWindow("", &f, &to); err != nil {
		t.Errorf("multi-year window without group_by should be allowed, got %v", err)
	}
	// With group_by → cap applies.
	if err := ValidateWindow(GroupByDay, &f, &to); !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("multi-year window with group_by should fail, got %v", err)
	}
}

func TestValidateWindow_366DayBoundaryIncluded(t *testing.T) {
	t.Parallel()
	f := time.Date(2025, 5, 31, 0, 0, 0, 0, time.UTC)
	to := f.Add(maxOwnerCostWindow) // exactly 366 days
	if err := ValidateWindow(GroupByDay, &f, &to); err != nil {
		t.Errorf("exact 366d window should be allowed, got %v", err)
	}
}

func TestValidateWindow_NilBoundsAllowed(t *testing.T) {
	t.Parallel()
	if err := ValidateWindow(GroupByDay, nil, nil); err != nil {
		t.Errorf("nil bounds should not error (passthrough), got %v", err)
	}
}
