package task

import "testing"

func TestVersionTargetStatus(t *testing.T) {
	tests := []struct {
		name          string
		kind          string
		payloadStatus string
		want          Status
		wantOK        bool
	}{
		{"error always fails", "error", "", StatusFailed, true},
		{"error ignores payload status", "error", "running", StatusFailed, true},
		{"status running", "status", "running", StatusRunning, true},
		{"status succeeded", "status", "succeeded", StatusSucceeded, true},
		{"status failed", "status", "failed", StatusFailed, true},
		{"status queued is a valid version status", "status", "queued", StatusQueued, true},
		{"status cancelling is a valid version status", "status", "cancelling", StatusCancelling, true},
		{"status unknown → no transition", "status", "bogus", "", false},
		{"status empty → no transition", "status", "", "", false},
		{"plan kind → persist only", "plan", "", "", false},
		{"step kind → persist only", "step", "running", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := versionTargetStatus(tt.kind, tt.payloadStatus)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("versionTargetStatus(%q,%q) = (%q,%v); want (%q,%v)",
					tt.kind, tt.payloadStatus, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestTaskStatusFromVersion(t *testing.T) {
	tests := []struct {
		version Status
		want    Status
		wantOK  bool
	}{
		{StatusQueued, StatusPending, true},     // version-only → task pending
		{StatusCancelling, "", false},           // no task equivalent → skip update
		{StatusPending, StatusPending, true},    //
		{StatusRunning, StatusRunning, true},     //
		{StatusPaused, StatusPaused, true},       //
		{StatusCancelled, StatusCancelled, true}, //
		{StatusSucceeded, StatusSucceeded, true}, //
		{StatusFailed, StatusFailed, true},       //
		{Status("bogus"), "", false},             //
	}
	for _, tt := range tests {
		t.Run(string(tt.version), func(t *testing.T) {
			got, ok := taskStatusFromVersion(tt.version)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("taskStatusFromVersion(%q) = (%q,%v); want (%q,%v)",
					tt.version, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
