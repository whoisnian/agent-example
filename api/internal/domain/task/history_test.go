package task

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateHistoryField(t *testing.T) {
	if got := truncateHistoryField("short"); got != "short" {
		t.Errorf("short input changed: %q", got)
	}
	exact := strings.Repeat("a", maxHistoryFieldBytes)
	if got := truncateHistoryField(exact); got != exact {
		t.Errorf("at-limit input changed")
	}
	long := strings.Repeat("汉", 600) // 1800 bytes > 1024
	got := truncateHistoryField(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("want ellipsis suffix")
	}
	if len(got) > maxHistoryFieldBytes {
		t.Errorf("bytes = %d, want <= %d", len(got), maxHistoryFieldBytes)
	}
	if !utf8.ValidString(got) {
		t.Error("must cut on a rune boundary")
	}
}

func TestTruncateHistorySummaryNil(t *testing.T) {
	if got := truncateHistorySummary(nil); got != nil {
		t.Errorf("nil must stay nil, got %v", got)
	}
	v := "ok"
	if got := truncateHistorySummary(&v); got == nil || *got != "ok" {
		t.Errorf("non-nil mangled: %v", got)
	}
}

// turnsNewestFirst builds n turns as the chain walk yields them
// (newest→oldest), with version_no n..1.
func turnsNewestFirst(n int, prompt string, summary *string) []HistoryTurn {
	out := make([]HistoryTurn, 0, n)
	for i := n; i >= 1; i-- {
		out = append(out, HistoryTurn{
			VersionNo: int32(i), Prompt: prompt, Summary: summary, Status: "succeeded",
		})
	}
	return out
}

func TestBoundHistoryTurnsOrdering(t *testing.T) {
	turns, stats, err := boundHistoryTurns(turnsNewestFirst(3, "p", nil))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Turns != 3 || stats.DroppedSize != 0 || stats.DroppedDepth != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	for i, want := range []int32{1, 2, 3} {
		if turns[i].VersionNo != want {
			t.Fatalf("order: turns[%d].VersionNo = %d, want %d (oldest→newest)", i, turns[i].VersionNo, want)
		}
	}
}

func TestBoundHistoryTurnsDepthClipMarked(t *testing.T) {
	_, stats, err := boundHistoryTurns(turnsNewestFirst(maxHistoryTurns, "p", nil))
	if err != nil {
		t.Fatal(err)
	}
	if stats.DroppedDepth != 1 {
		t.Errorf("a full-depth walk must mark the chain as clipped, stats = %+v", stats)
	}
}

func TestBoundHistoryTurnsSizeCapDropsOldest(t *testing.T) {
	// Maximally-sized turns: ~2 KiB each post-truncation, 20 of them ≈ 41 KiB
	// serialized — the 16 KiB cap must drop oldest turns and keep newest.
	big := truncateHistoryField(strings.Repeat("x", 5000))
	turns, stats, err := boundHistoryTurns(turnsNewestFirst(maxHistoryTurns, big, &big))
	if err != nil {
		t.Fatal(err)
	}
	if stats.DroppedSize == 0 {
		t.Fatal("expected size-driven drops")
	}
	b, _ := json.Marshal(turns)
	if len(b) > maxHistoryBytes {
		t.Fatalf("serialized = %d bytes, want <= %d", len(b), maxHistoryBytes)
	}
	if turns[len(turns)-1].VersionNo != int32(maxHistoryTurns) {
		t.Errorf("newest turn must survive, last = v%d", turns[len(turns)-1].VersionNo)
	}
	if turns[0].VersionNo != int32(maxHistoryTurns-stats.Turns+1) {
		t.Errorf("drops must come off the oldest end: first = v%d, kept %d", turns[0].VersionNo, stats.Turns)
	}
}

func TestHistoryTurnJSONShape(t *testing.T) {
	sum := "did it"
	b, err := json.Marshal([]HistoryTurn{
		{VersionNo: 1, Prompt: "build", Summary: &sum, Status: "succeeded"},
		{VersionNo: 2, Prompt: "fix", Summary: nil, Status: "failed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"version_no":1,"prompt":"build","summary":"did it","status":"succeeded"},` +
		`{"version_no":2,"prompt":"fix","summary":null,"status":"failed"}]`
	if string(b) != want {
		t.Errorf("wire shape drifted:\n got %s\nwant %s", b, want)
	}
}
