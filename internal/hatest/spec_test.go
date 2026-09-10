package hatest

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestServiceCallDelayParsesDuration(t *testing.T) {
	var call ServiceCall
	if err := yaml.Unmarshal([]byte("service: mock_entities.set_state\ndelay: 1500ms\n"), &call); err != nil {
		t.Fatalf("unmarshal service call: %v", err)
	}
	if got := time.Duration(call.Delay); got != 1500*time.Millisecond {
		t.Fatalf("delay = %s, want 1.5s", got)
	}
}

func TestTestCaseValidateRequiresAutomationForTraceAssertions(t *testing.T) {
	tests := []struct {
		name       string
		assertions *TraceAssertions
	}{
		{
			name:       "empty trace assertions",
			assertions: &TraceAssertions{},
		},
		{
			name: "whitespace automation with expect no trace",
			assertions: &TraceAssertions{
				Automation:    " \t\n",
				ExpectNoTrace: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCase := validTestCase()
			testCase.TraceAssertions = tt.assertions

			err := testCase.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want missing automation error")
			}
			if !strings.Contains(err.Error(), "trace_assertions.automation must be a non-empty automation entity ID") {
				t.Fatalf("Validate() error = %q, want missing automation error", err)
			}
		})
	}
}

func TestTestCaseValidateRejectsExpectNoTraceWithTraceRunAssertions(t *testing.T) {
	expectCompleted := true
	expectNoErrors := true
	tests := []struct {
		name       string
		assertions TraceAssertions
	}{
		{
			name:       "expect completed",
			assertions: TraceAssertions{ExpectCompleted: &expectCompleted},
		},
		{
			name:       "expect no errors",
			assertions: TraceAssertions{ExpectNoErrors: &expectNoErrors},
		},
		{
			name:       "expect branch",
			assertions: TraceAssertions{ExpectBranch: "occupied"},
		},
		{
			name:       "expect actions",
			assertions: TraceAssertions{ExpectActions: []ExpectedAction{{Service: "light.turn_on"}}},
		},
		{
			name: "expect actions in order",
			assertions: TraceAssertions{
				ExpectActionsInOrder: []ExpectedAction{{Service: "light.turn_on"}},
			},
		},
		{
			name:       "reject actions",
			assertions: TraceAssertions{RejectActions: []ExpectedAction{{Service: "light.turn_off"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCase := validTestCase()
			tt.assertions.Automation = "automation.test"
			tt.assertions.ExpectNoTrace = true
			testCase.TraceAssertions = &tt.assertions

			err := testCase.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want expect_no_trace incompatibility error")
			}
			if !strings.Contains(err.Error(), "trace_assertions.expect_no_trace cannot be combined with trace run assertions") {
				t.Fatalf("Validate() error = %q, want expect_no_trace incompatibility error", err)
			}
		})
	}
}

func validTestCase() TestCase {
	return TestCase{
		Name: "test",
		Trigger: []ServiceCall{
			{Service: "automation.trigger"},
		},
		Assertions: []Assertion{
			{EntityID: "input_boolean.test", State: "on"},
		},
	}
}
