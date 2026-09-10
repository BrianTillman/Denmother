package haaudit

import (
	"testing"
	"time"
)

func TestAutomationItemID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"automation.office_occupied", "office_occupied"},
		{"automation.garage_occupancy_lighting", "garage_occupancy_lighting"},
		{"office_occupied", "office_occupied"}, // bare pass-through
		{"", ""},
	}
	for _, c := range cases {
		got := AutomationItemID(c.in)
		if got != c.want {
			t.Errorf("automationItemID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFindClosestTrace_Match(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	traces := []AutomationTrace{
		{RunID: "a", Timestamp: base.Add(-30 * time.Second)},
		{RunID: "b", Timestamp: base.Add(5 * time.Second)},  // closest
		{RunID: "c", Timestamp: base.Add(90 * time.Second)}, // outside window
	}
	got := findClosestTrace(traces, base)
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.RunID != "b" {
		t.Errorf("expected run_id b (closest), got %q", got.RunID)
	}
}

func TestFindClosestTrace_NoMatch(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	traces := []AutomationTrace{
		{RunID: "x", Timestamp: base.Add(-90 * time.Second)},
		{RunID: "y", Timestamp: base.Add(90 * time.Second)},
	}
	got := findClosestTrace(traces, base)
	if got != nil {
		t.Errorf("expected nil, got %q", got.RunID)
	}
}

func TestFindClosestTrace_Empty(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	got := findClosestTrace(nil, base)
	if got != nil {
		t.Errorf("expected nil for empty traces, got %v", got)
	}
}
