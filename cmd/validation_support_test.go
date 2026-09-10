package cmd

import (
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/BrianTillman/Denmother/internal/validator"
)

func TestValidationSummaryStepIncompleteIsNotSuccess(t *testing.T) {
	summary := validationSummary{
		ChecksRun:        5,
		IncompleteChecks: []string{"schema"},
		Results: []validator.CheckResult{
			{
				Name:     "schema",
				Status:   validator.CheckIncomplete,
				Summary:  "schema validation incomplete: docker not found",
				Findings: []string{"schema validation incomplete: docker not found"},
			},
		},
	}

	step := summary.Step()
	if step.Status != operator.StatusPartial {
		t.Fatalf("status = %q, want %q", step.Status, operator.StatusPartial)
	}
	if step.Status == operator.StatusSuccess {
		t.Fatal("incomplete schema must not report full verification")
	}
	incomplete, _ := step.Details["incomplete_checks"].([]string)
	if len(incomplete) != 1 || incomplete[0] != "schema" {
		t.Fatalf("incomplete_checks = %#v", step.Details["incomplete_checks"])
	}
	findings, _ := step.Details["findings"].([]string)
	if len(findings) == 0 {
		t.Fatal("JSON details discarded schema findings")
	}
}

func TestValidationSummaryStepFailureRetainsFindings(t *testing.T) {
	summary := validationSummary{
		ChecksRun:    5,
		FailedChecks: []string{"schema"},
		Results: []validator.CheckResult{
			{
				Name:     "schema",
				Status:   validator.CheckFailed,
				Summary:  "schema validation failed in custom-ha: Invalid config for [automation]: unknown device abc123",
				Findings: []string{"Invalid config for [automation]: unknown device abc123"},
			},
		},
	}

	step := summary.Step()
	if step.Status != operator.StatusFailure {
		t.Fatalf("status = %q, want %q", step.Status, operator.StatusFailure)
	}
	findings, _ := step.Details["findings"].([]string)
	if len(findings) != 1 || findings[0] != "Invalid config for [automation]: unknown device abc123" {
		t.Fatalf("findings = %#v", step.Details["findings"])
	}
}

func TestValidationSummaryStepSuccessWhenAllPassed(t *testing.T) {
	summary := validationSummary{
		ChecksRun: 5,
		Results: []validator.CheckResult{
			{Name: "yaml", Status: validator.CheckPassed, Summary: "yaml passed"},
			{Name: "schema", Status: validator.CheckPassed, Summary: "schema passed"},
		},
	}
	step := summary.Step()
	if step.Status != operator.StatusSuccess {
		t.Fatalf("status = %q", step.Status)
	}
}
