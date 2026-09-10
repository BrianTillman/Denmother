package cmd

import (
	"fmt"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/BrianTillman/Denmother/internal/validator"
)

type validationSummary struct {
	ChecksRun        int
	FailedChecks     []string
	IncompleteChecks []string
	Results          []validator.CheckResult
}

func runValidationSuite(configPath string, suppressOutput bool) validationSummary {
	return runSelectedValidation(configPath, suppressOutput, []string{"yaml", "config", "guard", "entities", "schema"})
}

func runSelectedValidation(configPath string, suppressOutput bool, names []string) validationSummary {
	v := validator.New(configPath, validator.Options{}).WithContext(commandContext())
	checks := []struct {
		name string
		run  func() error
	}{
		{name: "yaml", run: v.ValidateYAML},
		{name: "config", run: v.ValidateConfig},
		{name: "guard", run: v.ValidateGuard},
		{name: "entities", run: v.ValidateEntities},
		{name: "schema", run: v.ValidateSchema},
	}

	summary := validationSummary{ChecksRun: len(names)}

	for _, check := range checks {
		if !stringSliceContains(names, check.name) {
			continue
		}
		var result validator.CheckResult
		_ = withDiscardedConsole(suppressOutput, func() error {
			if check.name == "entities" {
				result = v.CheckEntities()
			} else {
				result = validator.ResultFromError(check.name, check.run())
			}
			return result.Err()
		})
		summary.Results = append(summary.Results, result)
		switch result.Status {
		case validator.CheckFailed:
			summary.FailedChecks = append(summary.FailedChecks, check.name)
		case validator.CheckIncomplete:
			summary.IncompleteChecks = append(summary.IncompleteChecks, check.name)
		}
	}

	return summary
}

func (s validationSummary) Step() operator.Step {
	details := map[string]any{
		"checks_run": s.ChecksRun,
		"results":    s.Results,
	}
	if len(s.FailedChecks) > 0 {
		details["failed_checks"] = s.FailedChecks
	}
	if len(s.IncompleteChecks) > 0 {
		details["incomplete_checks"] = s.IncompleteChecks
	}
	if findings := s.findings(); len(findings) > 0 {
		details["findings"] = findings
	}

	step := operator.Step{
		ID:      "validate",
		Title:   "Validate configuration",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("completed %d validation checks", s.ChecksRun),
		Details: details,
	}

	if len(s.FailedChecks) > 0 {
		step.Status = operator.StatusFailure
		step.Summary = fmt.Sprintf("failed validation checks: %s", strings.Join(s.FailedChecks, ", "))
		return step
	}
	if len(s.IncompleteChecks) > 0 {
		step.Status = operator.StatusPartial
		step.Summary = fmt.Sprintf("incomplete validation checks: %s", strings.Join(s.IncompleteChecks, ", "))
		step.Hints = []string{
			"Start this worktree with `./dm dev up --json` before treating schema verification as complete.",
		}
		return step
	}
	return step
}

func (s validationSummary) findings() []string {
	var findings []string
	for _, result := range s.Results {
		findings = append(findings, result.Findings...)
	}
	return findings
}
