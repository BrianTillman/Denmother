package hatest

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
)

func TestValidateBaseTrace_Stopped(t *testing.T) {
	trace := &haaudit.AutomationTrace{
		RunID:    "1711234567.123456",
		State:    "stopped",
		LastStep: "action/0/choose/0/sequence/0",
	}
	result := validateBaseTrace(trace, "automation.test", 200*time.Millisecond)

	if result.Error != "" {
		t.Errorf("expected no error, got: %s", result.Error)
	}
	if result.State != "stopped" {
		t.Errorf("expected state 'stopped', got: %s", result.State)
	}
	if result.RunID != "1711234567.123456" {
		t.Errorf("expected run_id, got: %s", result.RunID)
	}
}

func TestValidateBaseTrace_Error(t *testing.T) {
	trace := &haaudit.AutomationTrace{
		RunID: "1711234567.123456",
		State: "stopped",
		Error: "NoneType has no attribute 'state'",
	}
	result := validateBaseTrace(trace, "automation.test", 200*time.Millisecond)

	if result.Error == "" {
		t.Error("expected error, got none")
	}
	if result.Error != "automation trace error: NoneType has no attribute 'state'" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestValidateBaseTrace_Aborted(t *testing.T) {
	trace := &haaudit.AutomationTrace{
		RunID: "1711234567.123456",
		State: "aborted",
	}
	result := validateBaseTrace(trace, "automation.test", 200*time.Millisecond)

	if result.Error == "" {
		t.Error("expected warning for aborted trace")
	}
}

func TestParseFullTrace_Occupied(t *testing.T) {
	data, err := os.ReadFile("testdata/trace_get_occupied.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	var trace FullTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if trace.RunID != "1711234567.123456" {
		t.Errorf("expected run_id '1711234567.123456', got: %s", trace.RunID)
	}
	if trace.State != "stopped" {
		t.Errorf("expected state 'stopped', got: %s", trace.State)
	}
	if trace.Error != "" {
		t.Errorf("expected no error, got: %s", trace.Error)
	}

	triggerNodes, ok := trace.Trace["trigger/0"]
	if !ok || len(triggerNodes) == 0 {
		t.Fatal("expected trigger/0 node in trace")
	}
	triggerMap, ok := triggerNodes[0].Changed["trigger"].(map[string]interface{})
	if !ok {
		t.Fatal("expected trigger map in changed_variables")
	}
	if triggerMap["id"] != "occupied" {
		t.Errorf("expected trigger.id 'occupied', got: %v", triggerMap["id"])
	}

	actionNodes, ok := trace.Trace["action/0/choose/0/sequence/0"]
	if !ok || len(actionNodes) == 0 {
		t.Fatal("expected action node in trace")
	}
	if actionNodes[0].Result == nil {
		t.Fatal("expected result in action node")
	}
	if actionNodes[0].Result.Params["domain"] != "light" {
		t.Errorf("expected domain 'light', got: %v", actionNodes[0].Result.Params["domain"])
	}
}

func TestParseFullTrace_RunningScriptBool(t *testing.T) {
	data := []byte(`{
		"run_id": "1711234567.123456",
		"state": "stopped",
		"timestamp": {"start": "2026-03-20T12:00:05.123456+00:00"},
		"trace": {
			"action/0": [{
				"path": "action/0",
				"result": {
					"params": {"domain": "light", "service": "turn_on"},
					"running_script": false
				}
			}]
		}
	}`)

	var trace FullTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("failed to unmarshal trace with boolean running_script: %v", err)
	}

	nodes := trace.Trace["action/0"]
	if len(nodes) != 1 || nodes[0].Result == nil {
		t.Fatal("expected parsed action result")
	}
	if nodes[0].Result.Params["service"] != "turn_on" {
		t.Errorf("expected service 'turn_on', got: %v", nodes[0].Result.Params["service"])
	}
}

func TestParseFullTrace_Error(t *testing.T) {
	data, err := os.ReadFile("testdata/trace_get_error.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	var trace FullTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if trace.Error != "NoneType has no attribute 'state'" {
		t.Errorf("expected error in trace, got: %s", trace.Error)
	}
}

func TestParseTraceListResponse(t *testing.T) {
	data, err := os.ReadFile("testdata/trace_list_response.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	var items []struct {
		RunID     string                 `json:"run_id"`
		Timestamp haaudit.TraceTimestamp `json:"timestamp"`
		State     string                 `json:"state"`
		LastStep  string                 `json:"last_step"`
		Error     string                 `json:"error"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].RunID != "1711234567.123456" {
		t.Errorf("expected first run_id, got: %s", items[0].RunID)
	}
	if items[0].State != "stopped" {
		t.Errorf("expected state 'stopped', got: %s", items[0].State)
	}
	if items[0].Timestamp.Start == "" {
		t.Error("expected non-empty timestamp start")
	}
}

func TestShortRunID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1711234567.123456", "1711234567.12345"},
		{"short", "short"},
		{"exactly16chars!!", "exactly16chars!!"},
	}
	for _, c := range cases {
		got := shortRunID(c.in)
		if got != c.want {
			t.Errorf("shortRunID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHasStructuredAssertions(t *testing.T) {
	boolTrue := true

	cases := []struct {
		name string
		ta   *TraceAssertions
		want bool
	}{
		{"nil fields", &TraceAssertions{Automation: "automation.test"}, false},
		{"expect_completed", &TraceAssertions{ExpectCompleted: &boolTrue}, true},
		{"expect_no_errors", &TraceAssertions{ExpectNoErrors: &boolTrue}, true},
		{"expect_branch", &TraceAssertions{ExpectBranch: "occupied"}, true},
		{"expect_actions", &TraceAssertions{ExpectActions: []ExpectedAction{{Service: "light.turn_on"}}}, true},
		{"expect_actions_in_order", &TraceAssertions{ExpectActionsInOrder: []ExpectedAction{{Service: "light.turn_on"}}}, true},
		{"reject_actions", &TraceAssertions{RejectActions: []ExpectedAction{{Service: "vacuum.start"}}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasStructuredAssertions(c.ta)
			if got != c.want {
				t.Errorf("hasStructuredAssertions() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUnexpectedTraceNotInBaselineIgnoresPrecedingTestTrace(t *testing.T) {
	triggerTime := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	preceding := haaudit.AutomationTrace{
		RunID:     "preceding-test",
		State:     "stopped",
		Timestamp: triggerTime.Add(time.Second),
	}
	baseline := traceRunIDSet([]haaudit.AutomationTrace{preceding})

	if got := unexpectedTraceNotInBaseline([]haaudit.AutomationTrace{preceding}, baseline); got != nil {
		t.Fatalf("expected baseline trace to be ignored, got %#v", got)
	}
}

func TestUnexpectedTraceNotInBaselineDetectsClockSkewedNewTrace(t *testing.T) {
	triggerTime := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	preceding := haaudit.AutomationTrace{
		RunID:     "preceding-test",
		State:     "stopped",
		Timestamp: triggerTime.Add(-time.Minute),
	}
	baseline := traceRunIDSet([]haaudit.AutomationTrace{preceding})

	for _, state := range []string{"running", "stopped"} {
		t.Run(state, func(t *testing.T) {
			clockSkewedNewTrace := haaudit.AutomationTrace{
				RunID:     "new-" + state,
				State:     state,
				Timestamp: triggerTime.Add(-time.Second),
			}
			got := unexpectedTraceNotInBaseline(
				[]haaudit.AutomationTrace{clockSkewedNewTrace, preceding},
				baseline,
			)
			if got == nil || got.RunID != clockSkewedNewTrace.RunID {
				t.Fatalf("expected new %s trace despite earlier server timestamp, got %#v", state, got)
			}
		})
	}
}

func TestSelectTraceCandidatePrefersTraceMatchingStructuredAssertions(t *testing.T) {
	cutoff := time.Date(2026, 6, 29, 22, 0, 0, 0, time.UTC)
	traces := []haaudit.AutomationTrace{
		{
			RunID:     "newer-no-action",
			State:     "stopped",
			Timestamp: cutoff.Add(3 * time.Second),
			LastStep:  "action/0/conditions/0",
		},
		{
			RunID:     "older-action",
			State:     "stopped",
			Timestamp: cutoff.Add(2 * time.Second),
			LastStep:  "action/0/sequence/0",
		},
	}
	assertions := &TraceAssertions{
		ExpectActions: []ExpectedAction{{
			Service: "light.turn_off",
			Target:  "light.office_lounge_south",
		}},
	}
	fullTraces := map[string]*FullTrace{
		"newer-no-action": {
			State: "stopped",
			Trace: map[string][]TraceNode{
				"action/0/conditions/0": {{
					Path:   "action/0/conditions/0",
					Result: &TraceActionResult{Params: map[string]interface{}{"result": false}},
				}},
			},
		},
		"older-action": {
			State: "stopped",
			Trace: map[string][]TraceNode{
				"action/0/sequence/0": {{
					Path: "action/0/sequence/0",
					Result: &TraceActionResult{Params: map[string]interface{}{
						"domain":  "light",
						"service": "turn_off",
						"target": map[string]interface{}{
							"entity_id": []interface{}{
								"light.office_lounge_north",
								"light.office_lounge_south",
							},
						},
					}},
				}},
			},
		},
	}

	selected, fullTrace, err := selectTraceCandidate(traces, cutoff, nil, assertions, func(runID string) (*FullTrace, error) {
		return fullTraces[runID], nil
	})
	if err != nil {
		t.Fatalf("selectTraceCandidate returned error: %v", err)
	}
	if selected == nil {
		t.Fatal("expected selected trace")
	}
	if selected.RunID != "older-action" {
		t.Fatalf("expected older matching action trace, got %q", selected.RunID)
	}
	if fullTrace != fullTraces["older-action"] {
		t.Fatal("expected selected full trace to be returned for reuse")
	}
}

func TestSelectTraceCandidateWithoutStructuredAssertionsUsesNewestTrace(t *testing.T) {
	cutoff := time.Date(2026, 6, 29, 22, 0, 0, 0, time.UTC)
	traces := []haaudit.AutomationTrace{
		{
			RunID:     "newer-no-action",
			State:     "stopped",
			Timestamp: cutoff.Add(3 * time.Second),
		},
		{
			RunID:     "older-action",
			State:     "stopped",
			Timestamp: cutoff.Add(2 * time.Second),
		},
	}

	selected, fullTrace, err := selectTraceCandidate(traces, cutoff, nil, nil, nil)
	if err != nil {
		t.Fatalf("selectTraceCandidate returned error: %v", err)
	}
	if selected == nil {
		t.Fatal("expected selected trace")
	}
	if selected.RunID != "newer-no-action" {
		t.Fatalf("expected newest trace without structured assertions, got %q", selected.RunID)
	}
	if fullTrace != nil {
		t.Fatal("did not expect full trace without structured assertions")
	}
}

func TestSelectTraceCandidateDoesNotUseRejectedActionToHideNewestTrace(t *testing.T) {
	cutoff := time.Date(2026, 6, 29, 22, 0, 0, 0, time.UTC)
	traces := []haaudit.AutomationTrace{
		{RunID: "newer-forbidden-action", State: "stopped", Timestamp: cutoff.Add(3 * time.Second)},
		{RunID: "older-safe", State: "stopped", Timestamp: cutoff.Add(2 * time.Second)},
	}
	assertions := &TraceAssertions{
		RejectActions: []ExpectedAction{{Service: "vacuum.start"}},
	}
	fullTraces := map[string]*FullTrace{
		"newer-forbidden-action": {
			State: "stopped",
			Trace: map[string][]TraceNode{
				"action/0": {{Result: &TraceActionResult{Params: map[string]interface{}{
					"domain": "vacuum", "service": "start",
				}}}},
			},
		},
		"older-safe": {State: "stopped"},
	}

	selected, fullTrace, err := selectTraceCandidate(traces, cutoff, nil, assertions, func(runID string) (*FullTrace, error) {
		return fullTraces[runID], nil
	})
	if err != nil {
		t.Fatalf("selectTraceCandidate returned error: %v", err)
	}
	if selected == nil || selected.RunID != "newer-forbidden-action" {
		t.Fatalf("expected newest trace for negative assertion, got %#v", selected)
	}
	if fullTrace != fullTraces["newer-forbidden-action"] {
		t.Fatal("expected newest full trace to be returned for rejection evaluation")
	}
}

func TestSelectTraceCandidateDoesNotUseOrderedActionsToHideNewestTrace(t *testing.T) {
	cutoff := time.Date(2026, 6, 29, 22, 0, 0, 0, time.UTC)
	traces := []haaudit.AutomationTrace{
		{RunID: "newer-reversed", State: "stopped", Timestamp: cutoff.Add(3 * time.Second)},
		{RunID: "older-correct", State: "stopped", Timestamp: cutoff.Add(2 * time.Second)},
	}
	assertions := &TraceAssertions{
		ExpectActionsInOrder: []ExpectedAction{
			{Service: "climate.set_hvac_mode"},
			{Service: "input_datetime.set_datetime"},
		},
	}
	fullTraces := map[string]*FullTrace{
		"newer-reversed": {
			State: "stopped",
			Trace: map[string][]TraceNode{
				"action/0": {{Result: &TraceActionResult{Params: map[string]interface{}{
					"domain": "input_datetime", "service": "set_datetime",
				}}}},
				"action/1": {{Result: &TraceActionResult{Params: map[string]interface{}{
					"domain": "climate", "service": "set_hvac_mode",
				}}}},
			},
		},
		"older-correct": {
			State: "stopped",
			Trace: map[string][]TraceNode{
				"action/0": {{Result: &TraceActionResult{Params: map[string]interface{}{
					"domain": "climate", "service": "set_hvac_mode",
				}}}},
				"action/1": {{Result: &TraceActionResult{Params: map[string]interface{}{
					"domain": "input_datetime", "service": "set_datetime",
				}}}},
			},
		},
	}

	selected, fullTrace, err := selectTraceCandidate(traces, cutoff, nil, assertions, func(runID string) (*FullTrace, error) {
		return fullTraces[runID], nil
	})
	if err != nil {
		t.Fatalf("selectTraceCandidate returned error: %v", err)
	}
	if selected == nil || selected.RunID != "newer-reversed" {
		t.Fatalf("expected newest trace for ordered assertion, got %#v", selected)
	}
	if fullTrace != fullTraces["newer-reversed"] {
		t.Fatal("expected newest full trace to be returned for ordered assertion evaluation")
	}
}

func TestEvaluateTraceAssertions_Completed(t *testing.T) {
	boolTrue := true
	boolFalse := false

	trace := &FullTrace{State: "stopped"}

	// expect_completed: true with stopped trace → pass
	results := evaluateTraceAssertions(trace, &TraceAssertions{ExpectCompleted: &boolTrue})
	if len(results) != 1 || !results[0].Passed {
		t.Errorf("expected pass for completed=true with stopped state")
	}

	// expect_completed: false with stopped trace → fail
	results = evaluateTraceAssertions(trace, &TraceAssertions{ExpectCompleted: &boolFalse})
	if len(results) != 1 || results[0].Passed {
		t.Errorf("expected fail for completed=false with stopped state")
	}
}

func TestEvaluateTraceAssertions_NoErrors(t *testing.T) {
	boolTrue := true

	// No error → pass
	trace := &FullTrace{Error: ""}
	results := evaluateTraceAssertions(trace, &TraceAssertions{ExpectNoErrors: &boolTrue})
	if len(results) != 1 || !results[0].Passed {
		t.Errorf("expected pass for no_errors=true with no error")
	}

	// Has error → fail
	trace = &FullTrace{Error: "something broke"}
	results = evaluateTraceAssertions(trace, &TraceAssertions{ExpectNoErrors: &boolTrue})
	if len(results) != 1 || results[0].Passed {
		t.Errorf("expected fail for no_errors=true with error")
	}
}

func TestSelectTraceCandidateExcludesBaseline(t *testing.T) {
	now := time.Now()
	old := haaudit.AutomationTrace{RunID: "old", State: "stopped", Timestamp: now.Add(-500 * time.Millisecond)}
	fresh := haaudit.AutomationTrace{RunID: "fresh", State: "stopped", Timestamp: now.Add(200 * time.Millisecond)}
	baseline := map[string]struct{}{"old": {}}
	selected, _, err := selectTraceCandidate([]haaudit.AutomationTrace{old, fresh}, now.Add(-time.Second), baseline, nil, nil)
	if err != nil {
		t.Fatalf("selectTraceCandidate: %v", err)
	}
	if selected == nil || selected.RunID != "fresh" {
		t.Fatalf("selected=%v want fresh", selected)
	}
	selected, _, err = selectTraceCandidate([]haaudit.AutomationTrace{old}, now.Add(-time.Second), baseline, nil, nil)
	if err != nil {
		t.Fatalf("selectTraceCandidate empty: %v", err)
	}
	if selected != nil {
		t.Fatalf("expected no candidate when only baseline remains, got %v", selected)
	}
}
