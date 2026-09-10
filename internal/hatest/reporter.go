package hatest

import (
	"fmt"
	"time"

	"github.com/fatih/color"
)

// TestStatus represents the status of a test case
type TestStatus string

const (
	StatusPassed  TestStatus = "PASSED"
	StatusFailed  TestStatus = "FAILED"
	StatusSkipped TestStatus = "SKIPPED"
)

// TestResults contains results from running a test specification
type TestResults struct {
	SpecName  string
	StartTime time.Time
	EndTime   time.Time
	Cases     []TestCaseResult
}

// TestCaseResult contains the result of a single test case
type TestCaseResult struct {
	Name                   string
	Status                 TestStatus
	Phase                  string
	ActionIndex            int
	EntityID               string
	Service                string
	EventType              string
	Error                  string
	CleanupErrors          []string // Cleanup failures are independent of the original failing phase.
	TraceError             string   // Non-empty if trace validation failed
	TraceSummary           string   // One-line trace summary for verbose output
	TraceAutomation        string
	TraceAttribution       string
	TraceAttributionReason string
	TraceContextIDs        []string
	TraceRunID             string
	StartTime              time.Time
	EndTime                time.Time
}

// PrintResults outputs test results to console with color formatting
func (r *TestResults) PrintResults() {
	fmt.Println()
	color.Cyan("======================================")
	color.Cyan("Test Results: %s", r.SpecName)
	color.Cyan("======================================")
	fmt.Println()

	passed := 0
	failed := 0
	skipped := 0

	for _, tc := range r.Cases {
		duration := tc.EndTime.Sub(tc.StartTime)

		switch tc.Status {
		case StatusPassed:
			color.Green("✓ %s (%.2fs)", tc.Name, duration.Seconds())
			if tc.TraceSummary != "" {
				color.Green("  Trace: %s", tc.TraceSummary)
			}
			passed++
		case StatusFailed:
			color.Red("✗ %s (%.2fs)", tc.Name, duration.Seconds())
			if tc.Error != "" {
				color.Red("  Error: %s", tc.Error)
			}
			if tc.TraceError != "" {
				color.Red("  Trace: %s", tc.TraceError)
			}
			failed++
		case StatusSkipped:
			color.Yellow("○ %s (skipped)", tc.Name)
			skipped++
		}
	}

	fmt.Println()
	totalDuration := r.EndTime.Sub(r.StartTime)

	fmt.Printf("Total: %d tests in %.2fs\n", len(r.Cases), totalDuration.Seconds())
	if passed > 0 {
		color.Green("Passed: %d", passed)
	}
	if failed > 0 {
		color.Red("Failed: %d", failed)
	}
	if skipped > 0 {
		color.Yellow("Skipped: %d", skipped)
	}
	fmt.Println()

	if failed > 0 {
		color.Red("TESTS FAILED")
	} else if passed > 0 {
		color.Green("ALL TESTS PASSED")
	} else {
		color.Yellow("NO TESTS RUN")
	}
	fmt.Println()
}

// GetExitCode returns appropriate exit code based on test results
func (r *TestResults) GetExitCode() int {
	for _, tc := range r.Cases {
		if tc.Status == StatusFailed {
			return 1
		}
	}
	return 0
}

// HasFailures returns true if any test cases failed
func (r *TestResults) HasFailures() bool {
	return r.GetExitCode() != 0
}

// PrintSummary outputs a brief summary line
func (r *TestResults) PrintSummary() {
	passed := 0
	failed := 0
	skipped := 0

	for _, tc := range r.Cases {
		switch tc.Status {
		case StatusPassed:
			passed++
		case StatusFailed:
			failed++
		case StatusSkipped:
			skipped++
		}
	}

	fmt.Printf("%s: %d passed, %d failed, %d skipped\n",
		r.SpecName, passed, failed, skipped)
}
