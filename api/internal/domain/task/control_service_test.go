package task

import (
	"errors"
	"testing"
)

func TestIsValidControlAction(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"pause", "resume", "cancel"} {
		if !IsValidControlAction(valid) {
			t.Errorf("IsValidControlAction(%q) = false, want true", valid)
		}
	}
	for _, invalid := range []string{"", "kill", "PAUSE", "stop", "Pause"} {
		if IsValidControlAction(invalid) {
			t.Errorf("IsValidControlAction(%q) = true, want false", invalid)
		}
	}
}

func TestCheckControlPrecondition_Pause(t *testing.T) {
	t.Parallel()
	allow := []string{"pending", "running"}
	deny := []string{"paused", "cancelled", "succeeded", "failed"}

	for _, s := range allow {
		if err := checkControlPrecondition(ControlPause, s); err != nil {
			t.Errorf("pause from %q rejected: %v", s, err)
		}
	}
	for _, s := range deny {
		err := checkControlPrecondition(ControlPause, s)
		if !errors.Is(err, ErrInvalidState) {
			t.Errorf("pause from %q: want ErrInvalidState, got %v", s, err)
		}
		// Reviewer S8 / D13: error message includes current status verbatim.
		if err != nil && !contains(err.Error(), s) {
			t.Errorf("pause-from-%q error missing status in message: %q", s, err.Error())
		}
	}
}

func TestCheckControlPrecondition_Resume(t *testing.T) {
	t.Parallel()
	// resume only from paused
	if err := checkControlPrecondition(ControlResume, "paused"); err != nil {
		t.Errorf("resume from paused rejected: %v", err)
	}
	for _, s := range []string{"pending", "running", "cancelled", "succeeded", "failed"} {
		if err := checkControlPrecondition(ControlResume, s); !errors.Is(err, ErrInvalidState) {
			t.Errorf("resume from %q: want ErrInvalidState, got %v", s, err)
		}
	}
}

func TestCheckControlPrecondition_Cancel(t *testing.T) {
	t.Parallel()
	// cancel from any non-terminal (including paused)
	allow := []string{"pending", "running", "paused"}
	deny := []string{"cancelled", "succeeded", "failed"}

	for _, s := range allow {
		if err := checkControlPrecondition(ControlCancel, s); err != nil {
			t.Errorf("cancel from %q rejected: %v", s, err)
		}
	}
	for _, s := range deny {
		err := checkControlPrecondition(ControlCancel, s)
		if !errors.Is(err, ErrInvalidState) {
			t.Errorf("cancel from %q: want ErrInvalidState, got %v", s, err)
		}
	}
}

func TestCheckControlPrecondition_UnknownAction(t *testing.T) {
	t.Parallel()
	// Apply re-validates before this point, but the function itself should
	// fail closed when called with garbage.
	err := checkControlPrecondition(ControlAction("kill"), "running")
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("unknown action: want ErrInvalidState, got %v", err)
	}
}

// contains is a tiny substring helper to avoid pulling in strings just for tests.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
