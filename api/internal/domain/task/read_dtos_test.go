package task

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func numeric(t *testing.T, unscaled int64, exp int32) pgtype.Numeric {
	t.Helper()
	return pgtype.Numeric{Int: big.NewInt(unscaled), Exp: exp, Valid: true, InfinityModifier: pgtype.Finite}
}

func TestNumericToDecimalString(t *testing.T) {
	tests := []struct {
		name string
		in   pgtype.Numeric
		want string
	}{
		{"invalid_null", pgtype.Numeric{Valid: false}, "0.00000000"},
		{"nan", pgtype.Numeric{NaN: true, Valid: true}, "0.00000000"},
		{"zero", numeric(t, 0, -8), "0.00000000"},
		{"fractional_8dp", numeric(t, 62000000, -8), "0.62000000"},
		{"fractional_rescaled", numeric(t, 62, -2), "0.62000000"}, // 0.62 with a coarser scale
		{"integer_value", numeric(t, 5, 0), "5.00000000"},
		{"large_18_8", numeric(t, 1234567890000, -8), "12345.67890000"},
		{"negative", numeric(t, -62000000, -8), "-0.62000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := numericToDecimalString(tc.in); got != tc.want {
				t.Fatalf("numericToDecimalString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClampPage(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 1}, {-5, 1}, {1, 1}, {7, 7}} {
		if got := clampPage(tc.in); got != tc.want {
			t.Errorf("clampPage(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClampPageSize(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 1}, {-1, 1}, {20, 20}, {100, 100}, {9999, 100}} {
		if got := clampPageSize(tc.in); got != tc.want {
			t.Errorf("clampPageSize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClampEventLimit(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 1}, {-3, 1}, {200, 200}, {1000, 1000}, {5000, 1000}} {
		if got := clampEventLimit(tc.in); got != tc.want {
			t.Errorf("clampEventLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestIsValidTaskStatus(t *testing.T) {
	valid := []string{"pending", "running", "paused", "cancelled", "succeeded", "failed"}
	for _, s := range valid {
		if !IsValidTaskStatus(s) {
			t.Errorf("IsValidTaskStatus(%q) = false, want true", s)
		}
	}
	// version-only statuses and junk must be rejected.
	for _, s := range []string{"queued", "cancelling", "bogus", ""} {
		if IsValidTaskStatus(s) {
			t.Errorf("IsValidTaskStatus(%q) = true, want false", s)
		}
	}
}

func TestDerefBool(t *testing.T) {
	tru, fls := true, false
	if !derefBool(&tru) {
		t.Error("derefBool(&true) = false")
	}
	if derefBool(&fls) {
		t.Error("derefBool(&false) = true")
	}
	if derefBool(nil) {
		t.Error("derefBool(nil) = true, want false")
	}
}
