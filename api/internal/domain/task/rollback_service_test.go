package task

import "testing"

func TestIsValidRollbackMode(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"branch", "switch"} {
		if !IsValidRollbackMode(valid) {
			t.Errorf("IsValidRollbackMode(%q) = false, want true", valid)
		}
	}
	for _, invalid := range []string{"", "Branch", "SWITCH", "rollback", "merge", "fork"} {
		if IsValidRollbackMode(invalid) {
			t.Errorf("IsValidRollbackMode(%q) = true, want false", invalid)
		}
	}
}
