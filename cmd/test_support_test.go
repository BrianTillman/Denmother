package cmd

import (
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
)

func TestTestSummaryDescribeCountsIncludesLoadAndRunFailures(t *testing.T) {
	t.Parallel()

	summary := &testSummary{
		Passed:       2,
		Failed:       1,
		Skipped:      3,
		LoadFailures: 1,
		RunFailures:  2,
	}

	got := summary.describeCounts()
	want := "2 passed, 1 failed, 3 skipped, 1 load failures, 2 execution errors"
	if got != want {
		t.Fatalf("describeCounts() = %q, want %q", got, want)
	}
}

func TestTestSummaryStepUsesWarningWhenNothingRan(t *testing.T) {
	t.Parallel()

	step := (&testSummary{}).Step("run-tests", "Run automation tests", nil)
	if step.Status != operator.StatusWarning {
		t.Fatalf("step.Status = %q, want %q", step.Status, operator.StatusWarning)
	}
	if step.Summary != "no tests matched the selected files and tags" {
		t.Fatalf("step.Summary = %q", step.Summary)
	}
}

func TestParseAssertionFailureExtractsEntityExpectedAndActual(t *testing.T) {
	t.Parallel()

	message := "Assertion failed: timeout waiting for input_boolean.evening to match \"on\"\n  Current: \"off\"\n  Attributes: map[friendly_name:Evening]"

	entityID, expected, actual := parseAssertionFailure(message)
	if entityID != "input_boolean.evening" {
		t.Fatalf("entityID = %q", entityID)
	}
	if expected != "on" {
		t.Fatalf("expected = %q", expected)
	}
	if actual != "off" {
		t.Fatalf("actual = %q", actual)
	}
}

func TestTestSummaryStepIncludesStructuredFailures(t *testing.T) {
	t.Parallel()

	summary := &testSummary{
		FilesSelected: 1,
		Failed:        1,
		Failures: []testFailure{
			{
				File:        "ha-config/tests/time/evening_test.yaml",
				Suite:       "Evening Mode Automation",
				Test:        "Evening time trigger enables boolean and sets relax vibe",
				Error:       "Assertion failed",
				EntityID:    "input_boolean.evening",
				Expected:    "on",
				Actual:      "off",
				NextCommand: "./dm test ha-config/tests/time/evening_test.yaml -v",
			},
		},
	}

	step := summary.Step("run-tests", "Run automation tests", []string{"fast"})
	if step.Status != operator.StatusFailure {
		t.Fatalf("step.Status = %q, want failure", step.Status)
	}
	if _, ok := step.Details["failures"]; !ok {
		t.Fatal("expected failures in step details")
	}
	if _, ok := step.Details["next_commands"]; !ok {
		t.Fatal("expected next_commands in step details")
	}
	if _, ok := step.Details["artifacts"]; !ok {
		t.Fatal("expected artifacts in step details")
	}
}

func TestAllSkippedCasesAreNotAPass(t *testing.T) {
	summary := &testSummary{FilesSelected: 1, Skipped: 3}
	if summary.Status() != operator.StatusWarning {
		t.Fatal("all-skipped suite reported success")
	}
}
