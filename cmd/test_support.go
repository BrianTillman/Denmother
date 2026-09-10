package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hatest"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
)

type testExecutionOptions struct {
	Context      context.Context
	Args         []string
	Pattern      string
	Case         string
	Tags         []string
	Verbose      bool
	Trace        bool
	PrintResults bool
}

type testSummary struct {
	FilesDiscovered int
	FilesSelected   int
	Passed          int
	Failed          int
	Skipped         int
	LoadFailures    int
	RunFailures     int
	Failures        []testFailure
	Cases           []testCaseEvidence
}

type testFailure struct {
	File          string   `json:"file"`
	Suite         string   `json:"suite,omitempty"`
	Test          string   `json:"test,omitempty"`
	Phase         string   `json:"phase,omitempty"`
	ActionIndex   *int     `json:"action_index,omitempty"`
	Error         string   `json:"error,omitempty"`
	TraceError    string   `json:"trace_error,omitempty"`
	CleanupErrors []string `json:"cleanup_errors,omitempty"`
	EntityID      string   `json:"entity_id,omitempty"`
	Service       string   `json:"service,omitempty"`
	EventType     string   `json:"event_type,omitempty"`
	Automation    string   `json:"automation,omitempty"`
	TraceRunID    string   `json:"trace_run_id,omitempty"`
	Expected      string   `json:"expected,omitempty"`
	Actual        string   `json:"actual,omitempty"`
	Duration      string   `json:"duration,omitempty"`
	NextCommand   string   `json:"next_command,omitempty"`
}

func (s *testSummary) AnyRun() bool {
	return s.Passed > 0 || s.Failed > 0 || s.Skipped > 0 || s.LoadFailures > 0 || s.RunFailures > 0
}

func (s *testSummary) Status() operator.Status {
	switch {
	case s.LoadFailures > 0 || s.RunFailures > 0 || s.Failed > 0:
		return operator.StatusFailure
	case s.Passed == 0:
		return operator.StatusWarning
	default:
		return operator.StatusSuccess
	}
}

func (s *testSummary) Step(id, title string, tags []string) operator.Step {
	details := map[string]any{
		"files_discovered": s.FilesDiscovered,
		"files_selected":   s.FilesSelected,
		"passed":           s.Passed,
		"failed":           s.Failed,
		"skipped":          s.Skipped,
		"cases":            s.Cases,
		"load_failures":    s.LoadFailures,
		"run_failures":     s.RunFailures,
	}
	if len(tags) > 0 {
		details["tags"] = tags
	}
	if len(s.Failures) > 0 {
		details["failures"] = s.Failures
		details["artifacts"] = failureArtifacts(s.Failures)
		details["next_commands"] = failureNextCommands(s.Failures)
	}

	step := operator.Step{
		ID:      id,
		Title:   title,
		Status:  s.Status(),
		Summary: s.describeCounts(),
		Details: details,
	}

	if s.Status() == operator.StatusWarning {
		step.Summary = "no tests matched the selected files and tags"
		if s.Skipped > 0 {
			step.Summary = fmt.Sprintf("no test cases executed (%d skipped); adjust selection or skip flags", s.Skipped)
		}
		step.Hints = append(step.Hints, "Adjust --tags, --pattern, --case, or file arguments to select at least one test case.")
	}

	if s.LoadFailures > 0 || s.RunFailures > 0 {
		step.Hints = append(step.Hints, "Inspect the failing test loads or execution errors above.")
	}
	if len(s.Failures) > 0 {
		step.Hints = append(step.Hints, "Run the failing test file with `./dm test <file> -v` for the full setup, trigger, assertion, and cleanup log.")
	}

	return step
}

func (s *testSummary) describeCounts() string {
	parts := []string{
		fmt.Sprintf("%d passed", s.Passed),
		fmt.Sprintf("%d failed", s.Failed),
		fmt.Sprintf("%d skipped", s.Skipped),
	}
	if s.LoadFailures > 0 {
		parts = append(parts, fmt.Sprintf("%d load failures", s.LoadFailures))
	}
	if s.RunFailures > 0 {
		parts = append(parts, fmt.Sprintf("%d execution errors", s.RunFailures))
	}
	return strings.Join(parts, ", ")
}

func executeTests(config *haconfig.HAConfig, opts testExecutionOptions) (*testSummary, error) {
	testFiles, err := discoverTestFiles(opts.Args, opts.Pattern)
	if err != nil {
		return nil, err
	}

	summary := &testSummary{
		FilesDiscovered: len(testFiles),
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	runner, err := hatest.NewTestRunnerContext(ctx, config.URL, config.Token, opts.Verbose, opts.Trace)
	if err != nil {
		return nil, fmt.Errorf("failed to create test runner: %w\n\nMake sure Home Assistant is running at %s", err, config.URL)
	}
	defer runner.Close()

	if opts.PrintResults && opts.Verbose {
		fmt.Printf("Connected to Home Assistant: %s\n\n", config.URL)
	}

	var allResults []*hatest.TestResults

	for _, testFile := range testFiles {
		spec, err := hatest.LoadTestSpec(testFile)
		if err != nil {
			if opts.PrintResults {
				color.Red("Failed to load %s: %v", testFile, err)
			}
			summary.LoadFailures++
			summary.Failures = append(summary.Failures, testFailure{
				File:        testFile,
				Error:       fmt.Sprintf("load failed: %v", err),
				NextCommand: fmt.Sprintf("./dm test %s -v", testFile),
			})
			continue
		}

		if len(opts.Tags) > 0 && !hasMatchingTag(spec.Tags, opts.Tags) {
			continue
		}

		summary.FilesSelected++

		results, err := runner.RunTestSpecWithFilter(spec, opts.Case)
		if err != nil {
			if opts.PrintResults {
				color.Red("Test execution failed for %s: %v", testFile, err)
			}
			summary.RunFailures++
			summary.Failures = append(summary.Failures, testFailure{
				File:        testFile,
				Suite:       spec.Name,
				Error:       fmt.Sprintf("execution failed: %v", err),
				NextCommand: fmt.Sprintf("./dm test %s -v", testFile),
			})
			continue
		}
		if opts.Case != "" && len(results.Cases) == 0 {
			continue
		}

		if opts.PrintResults {
			results.PrintResults()
		}
		allResults = append(allResults, results)

		for _, tc := range results.Cases {
			summary.Cases = append(summary.Cases, testCaseEvidence{File: testFile, Name: tc.Name, Status: tc.Status, TraceAutomation: tc.TraceAutomation, TraceRunID: tc.TraceRunID, TraceSummary: tc.TraceSummary, TraceAttribution: tc.TraceAttribution, TraceAttributionReason: tc.TraceAttributionReason, TraceContextIDs: tc.TraceContextIDs})
			switch tc.Status {
			case hatest.StatusPassed:
				summary.Passed++
			case hatest.StatusFailed:
				summary.Failed++
				summary.Failures = append(summary.Failures, buildTestFailure(testFile, results.SpecName, tc))
			case hatest.StatusSkipped:
				summary.Skipped++
			}
		}
	}

	if opts.PrintResults && len(allResults) > 1 {
		fmt.Println()
		color.Cyan("======================================")
		color.Cyan("Overall Summary")
		color.Cyan("======================================")
		fmt.Println()
		fmt.Printf("Total: %d test files, %d test cases\n", len(allResults), summary.Passed+summary.Failed+summary.Skipped)
		if summary.Passed > 0 {
			color.Green("Passed: %d", summary.Passed)
		}
		if summary.Failed > 0 {
			color.Red("Failed: %d", summary.Failed)
		}
		if summary.Skipped > 0 {
			color.Yellow("Skipped: %d", summary.Skipped)
		}
		fmt.Println()
	}

	return summary, nil
}

func buildTestFailure(file string, suite string, tc hatest.TestCaseResult) testFailure {
	failure := testFailure{
		File:          file,
		Suite:         suite,
		Test:          tc.Name,
		Phase:         tc.Phase,
		Error:         tc.Error,
		TraceError:    tc.TraceError,
		CleanupErrors: tc.CleanupErrors,
		EntityID:      tc.EntityID,
		Service:       tc.Service,
		EventType:     tc.EventType,
		Automation:    tc.TraceAutomation,
		TraceRunID:    tc.TraceRunID,
		Duration:      tc.EndTime.Sub(tc.StartTime).String(),
		NextCommand:   fmt.Sprintf("./dm test %s --case %s -v", file, shellQuote(tc.Name)),
	}
	if tc.Phase != "" {
		idx := tc.ActionIndex
		failure.ActionIndex = &idx
	}

	if entityID, expected, actual := parseAssertionFailure(tc.Error); entityID != "" {
		if failure.EntityID == "" {
			failure.EntityID = entityID
		}
		failure.Expected = expected
		failure.Actual = actual
	}

	return failure
}

var assertionFailureRe = regexp.MustCompile(`timeout waiting for ([^\s]+) to match "([^"]+)"(?s:.*?Current: "([^"]*)")?`)

func parseAssertionFailure(message string) (entityID string, expected string, actual string) {
	match := assertionFailureRe.FindStringSubmatch(message)
	if len(match) == 0 {
		return "", "", ""
	}
	entityID = match[1]
	expected = match[2]
	if len(match) > 3 {
		actual = match[3]
	}
	return entityID, expected, actual
}

func failureNextCommands(failures []testFailure) []string {
	seen := make(map[string]bool)
	var commands []string

	for _, failure := range failures {
		if failure.NextCommand == "" || seen[failure.NextCommand] {
			continue
		}
		seen[failure.NextCommand] = true
		commands = append(commands, failure.NextCommand)
	}

	return commands
}

func failureArtifacts(failures []testFailure) []string {
	seen := make(map[string]bool)
	var artifacts []string

	for _, failure := range failures {
		if failure.File == "" || seen[failure.File] {
			continue
		}
		seen[failure.File] = true
		artifacts = append(artifacts, failure.File)
	}

	return artifacts
}

func discoverTestFiles(args []string, pattern string) ([]string, error) {
	var testFiles []string

	if pattern != "" {
		fsys := os.DirFS(".")
		matches, err := doublestar.Glob(fsys, pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %w", err)
		}
		testFiles = matches
	} else if len(args) > 0 {
		testFiles = args
	} else {
		matches, err := globUnderConfig("tests/**/*_test.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to discover tests: %w", err)
		}
		testFiles = matches

		if len(testFiles) == 0 {
			patterns := []string{
				"tests/*_test.yaml",
				"tests/*/*_test.yaml",
			}
			for _, fallback := range patterns {
				matches, err := globUnderConfig(fallback)
				if err != nil {
					continue
				}
				testFiles = append(testFiles, matches...)
			}
		}
	}

	if len(testFiles) == 0 {
		root, _ := resolvedConfigRoot()
		if root == "" {
			root = configPath
		}
		return nil, fmt.Errorf("no test files found in %s/tests\n\nTry:\n  dm test <test-file>\n  dm --config %s test --pattern \"tests/**/*_test.yaml\"", root, configPath)
	}

	return testFiles, nil
}

func hasMatchingTag(specTags, filterTags []string) bool {
	if len(filterTags) == 0 {
		return true
	}

	tagSet := make(map[string]bool)
	for _, tag := range specTags {
		tagSet[strings.ToLower(strings.TrimSpace(tag))] = true
	}

	for _, filterTag := range filterTags {
		if tagSet[strings.ToLower(strings.TrimSpace(filterTag))] {
			return true
		}
	}

	return false
}

type testCaseEvidence struct {
	TraceAttribution       string            `json:"trace_attribution,omitempty"`
	TraceAttributionReason string            `json:"trace_attribution_reason,omitempty"`
	TraceContextIDs        []string          `json:"trace_context_ids,omitempty"`
	File                   string            `json:"file"`
	Name                   string            `json:"name"`
	Status                 hatest.TestStatus `json:"status"`
	TraceAutomation        string            `json:"trace_automation,omitempty"`
	TraceRunID             string            `json:"trace_run_id,omitempty"`
	TraceSummary           string            `json:"trace_summary,omitempty"`
}
