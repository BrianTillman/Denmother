package hatest

import (
	"encoding/json"
	"os"
	"testing"
)

func loadFixtureTrace(t *testing.T, path string) *FullTrace {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	var trace FullTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("failed to unmarshal %s: %v", path, err)
	}
	return &trace
}

func TestMatchBranch_Success(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	matched, actualID, err := matchBranch(trace, "occupied")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Errorf("expected branch 'occupied' to match, got trigger.id=%q", actualID)
	}
	if actualID != "occupied" {
		t.Errorf("expected actualID 'occupied', got %q", actualID)
	}
}

func TestMatchBranch_Mismatch(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	matched, actualID, err := matchBranch(trace, "unoccupied")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("expected mismatch")
	}
	if actualID != "occupied" {
		t.Errorf("expected actualID 'occupied', got %q", actualID)
	}
}

func TestMatchBranch_NoTriggerID(t *testing.T) {
	trace := &FullTrace{
		Trace: map[string][]TraceNode{
			"action/0": {{Path: "action/0"}},
		},
	}

	_, _, err := matchBranch(trace, "occupied")
	if err == nil {
		t.Error("expected error for missing trigger.id")
	}
}

func TestMatchActions_Found(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	expected := []ExpectedAction{
		{
			Service: "light.turn_on",
			Target:  "light.office_lights_all",
			Data:    map[string]interface{}{"brightness_pct": 50},
		},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected action match to pass: %s", results[0].Detail)
	}
}

func TestMatchActions_WrongService(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	expected := []ExpectedAction{
		{Service: "light.turn_off"},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("expected action match to fail for wrong service")
	}
}

func TestMatchActions_WrongTarget(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	expected := []ExpectedAction{
		{
			Service: "light.turn_on",
			Target:  "light.wrong_entity",
		},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("expected action match to fail for wrong target")
	}
}

func TestMatchActions_PartialData(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	// Only check brightness_pct, ignoring entity_id in service_data
	expected := []ExpectedAction{
		{
			Service: "light.turn_on",
			Data:    map[string]interface{}{"brightness_pct": 50},
		},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected partial data match to pass: %s", results[0].Detail)
	}
}

func TestMatchActions_MultiEntityTarget(t *testing.T) {
	trace := &FullTrace{
		Trace: map[string][]TraceNode{
			"action/0": {{
				Path: "action/0",
				Result: &TraceActionResult{
					Params: map[string]interface{}{
						"domain":  "light",
						"service": "turn_on",
						"service_data": map[string]interface{}{
							"entity_id": []interface{}{
								"light.office_lounge_south",
								"light.office_lounge_overhead_lights_north",
							},
							"brightness_pct": float64(50),
						},
					},
				},
			}},
		},
	}

	expected := []ExpectedAction{
		{
			Service: "light.turn_on",
			Target:  "light.office_lounge_overhead_lights_north",
			Data:    map[string]interface{}{"brightness_pct": 50},
		},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected action match to pass for second target: %s", results[0].Detail)
	}
}

func TestMatchActions_WrongData(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	expected := []ExpectedAction{
		{
			Service: "light.turn_on",
			Data:    map[string]interface{}{"brightness_pct": 100},
		},
	}

	results := matchActions(trace, expected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("expected action match to fail for wrong brightness")
	}
}

func TestRejectActions_FoundFails(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	rejected := []ExpectedAction{{
		Service: "light.turn_on",
		Target:  "light.office_lights_all",
	}}

	results := rejectActions(trace, rejected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("expected forbidden action found in trace to fail")
	}
}

func TestRejectActions_MissingPasses(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	rejected := []ExpectedAction{{Service: "vacuum.start"}}

	results := rejectActions(trace, rejected)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Errorf("expected absent forbidden action to pass: %s", results[0].Detail)
	}
}

func TestMatchActionsInOrder(t *testing.T) {
	trace := &FullTrace{Trace: map[string][]TraceNode{
		"action/0": {{Timestamp: "2026-01-01T00:00:00Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "logbook", "service": "log",
		}}}},
		"action/1": {{Timestamp: "2026-01-01T00:00:01Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "light", "service": "turn_on",
			"target": map[string]interface{}{"entity_id": "light.office_lights_all"},
		}}}},
	}}
	expected := []ExpectedAction{
		{Service: "logbook.log"},
		{Service: "light.turn_on", Target: "light.office_lights_all"},
	}

	results := matchActionsInOrder(trace, expected)
	if len(results) != 2 || !results[0].Passed || !results[1].Passed {
		t.Fatalf("expected ordered actions to pass, got %#v", results)
	}

	reversed := matchActionsInOrder(trace, []ExpectedAction{expected[1], expected[0]})
	if len(reversed) != 2 || reversed[1].Passed {
		t.Fatalf("expected reversed second action to fail, got %#v", reversed)
	}
}

func TestMatchActionsInOrderUsesExecutionTimestamps(t *testing.T) {
	trace := &FullTrace{Trace: map[string][]TraceNode{
		"action/10": {{Timestamp: "2026-01-01T00:00:01Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "light", "service": "turn_off",
		}}}},
		"action/2": {{Timestamp: "2026-01-01T00:00:00Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "light", "service": "turn_on",
		}}}},
		"action/11/choose/0/sequence/10": {{Timestamp: "2026-01-01T00:00:03Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "switch", "service": "turn_off",
		}}}},
		"action/11/choose/0/sequence/9": {{Timestamp: "2026-01-01T00:00:02Z", Result: &TraceActionResult{Params: map[string]interface{}{
			"domain": "switch", "service": "turn_on",
		}}}},
	}}
	expected := []ExpectedAction{
		{Service: "light.turn_on"},
		{Service: "light.turn_off"},
		{Service: "switch.turn_on"},
		{Service: "switch.turn_off"},
	}

	results := matchActionsInOrder(trace, expected)
	for i, result := range results {
		if !result.Passed {
			t.Fatalf("expected ordered action %d to pass with numeric trace path sorting, got %#v", i, results)
		}
	}
}

func TestCollectServiceCalls(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")
	calls := collectServiceCalls(trace)

	if len(calls) == 0 {
		t.Fatal("expected at least one service call")
	}

	found := false
	for _, call := range calls {
		if call.Service == "light.turn_on" {
			found = true
			if call.Target != "light.office_lights_all" {
				t.Errorf("expected target 'light.office_lights_all', got %q", call.Target)
			}
			break
		}
	}
	if !found {
		t.Error("expected to find light.turn_on service call")
	}
}

func TestExtractTriggerID(t *testing.T) {
	trace := loadFixtureTrace(t, "testdata/trace_get_occupied.json")

	id, found := extractTriggerID(trace)
	if !found {
		t.Error("expected to find trigger ID")
	}
	if id != "occupied" {
		t.Errorf("expected trigger ID 'occupied', got %q", id)
	}
}

func TestExtractTriggerID_NotFound(t *testing.T) {
	trace := &FullTrace{
		Trace: map[string][]TraceNode{
			"action/0": {{Path: "action/0"}},
		},
	}

	_, found := extractTriggerID(trace)
	if found {
		t.Error("expected trigger ID not found")
	}
}

func TestMatchServiceCall(t *testing.T) {
	call := traceServiceCall{
		Service: "light.turn_on",
		Target:  "light.office_lights_all",
		Targets: []string{"light.office_lights_all"},
		Data:    map[string]interface{}{"brightness_pct": float64(50), "entity_id": "light.office_lights_all"},
	}

	cases := []struct {
		name string
		exp  ExpectedAction
		want bool
	}{
		{"exact match", ExpectedAction{Service: "light.turn_on", Target: "light.office_lights_all", Data: map[string]interface{}{"brightness_pct": 50}}, true},
		{"service only", ExpectedAction{Service: "light.turn_on"}, true},
		{"wrong service", ExpectedAction{Service: "light.turn_off"}, false},
		{"wrong target", ExpectedAction{Service: "light.turn_on", Target: "light.wrong"}, false},
		{"wrong data", ExpectedAction{Service: "light.turn_on", Data: map[string]interface{}{"brightness_pct": 100}}, false},
		{"missing data key", ExpectedAction{Service: "light.turn_on", Data: map[string]interface{}{"nonexistent": 1}}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchServiceCall(call, c.exp)
			if got != c.want {
				t.Errorf("matchServiceCall() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOrderedActionsRejectAmbiguousOrReversedTime(t *testing.T) {
	for _, timestamps := range [][2]string{
		{"2026-01-01T00:00:02Z", "2026-01-01T00:00:01Z"},
		{"2026-01-01T00:00:01Z", "2026-01-01T00:00:01Z"},
		{"", "2026-01-01T00:00:01Z"},
	} {
		trace := &FullTrace{Trace: map[string][]TraceNode{
			"action/0/parallel/0": {{Timestamp: timestamps[0], Result: &TraceActionResult{Params: map[string]interface{}{"domain": "light", "service": "turn_on"}}}},
			"action/0/parallel/1": {{Timestamp: timestamps[1], Result: &TraceActionResult{Params: map[string]interface{}{"domain": "timer", "service": "start"}}}},
		}}
		got := matchActionsInOrder(trace, []ExpectedAction{{Service: "light.turn_on"}, {Service: "timer.start"}})
		if got[0].Passed && got[1].Passed {
			t.Fatalf("ambiguous/reversed timestamps %v passed: %+v", timestamps, got)
		}
	}
}
