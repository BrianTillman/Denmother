package validator

import (
	"context"
	"errors"
	"fmt"
)

// CheckStatus is the typed outcome of one validation check.
type CheckStatus string

const (
	CheckPassed     CheckStatus = "passed"
	CheckFailed     CheckStatus = "failed"
	CheckIncomplete CheckStatus = "incomplete"
)

const (
	exitPartialCode = 3
	exitFailureCode = 1
)

// CheckResult is a machine-readable per-check outcome with retained findings.
type CheckResult struct {
	cause    error
	Name     string         `json:"name"`
	Status   CheckStatus    `json:"status"`
	Summary  string         `json:"summary"`
	Findings []string       `json:"findings,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

// Err converts a typed result into an error. Passed results return nil.
func (r CheckResult) Err() error {
	switch r.Status {
	case CheckFailed:
		return &CheckError{Result: r}
	case CheckIncomplete:
		return &CheckError{Result: r, incomplete: true}
	default:
		return nil
	}
}

// CheckError carries a typed validation outcome through the CLI and JSON suite.
type CheckError struct {
	Result     CheckResult
	incomplete bool
}

func (e *CheckError) Error() string {
	if e == nil {
		return ""
	}
	if e.Result.Summary != "" {
		return e.Result.Summary
	}
	return fmt.Sprintf("%s %s", e.Result.Name, e.Result.Status)
}

func (e *CheckError) Unwrap() error { return e.Result.cause }

// Incomplete reports that verification did not complete.
func (e *CheckError) Incomplete() bool {
	return e != nil && e.incomplete
}

// ExitCode maps failed checks to 1 and incomplete checks to 3.
func (e *CheckError) ExitCode() int {
	if e != nil && e.incomplete {
		return exitPartialCode
	}
	return exitFailureCode
}

// IsIncomplete reports whether err represents incomplete verification.
func IsIncomplete(err error) bool {
	var checkErr *CheckError
	return errors.As(err, &checkErr) && checkErr.Incomplete()
}

// ResultFromError reconstructs a CheckResult from a check function error.
func ResultFromError(name string, err error) CheckResult {
	if err == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckPassed,
			Summary: name + " passed",
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CheckResult{Name: name, Status: CheckIncomplete, Summary: name + " validation incomplete: " + err.Error(), cause: err}
	}
	var checkErr *CheckError
	if errors.As(err, &checkErr) {
		result := checkErr.Result
		if result.Name == "" {
			result.Name = name
		}
		return result
	}
	return CheckResult{
		Name:    name,
		Status:  CheckFailed,
		Summary: err.Error(),
	}
}

func incompleteResult(name, summary string, findings []string, details map[string]any) CheckResult {
	return CheckResult{
		Name:     name,
		Status:   CheckIncomplete,
		Summary:  summary,
		Findings: findings,
		Details:  details,
	}
}

func failedResult(name, summary string, findings []string, details map[string]any) CheckResult {
	return CheckResult{
		Name:     name,
		Status:   CheckFailed,
		Summary:  summary,
		Findings: findings,
		Details:  details,
	}
}
