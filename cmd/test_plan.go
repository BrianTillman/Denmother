package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hayaml"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var testPlanJSON bool

var testPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan the narrowest useful automation test run from the current diff",
	RunE:  runTestPlan,
}

type testPlanSummary struct {
	ChangedFiles     []string             `json:"changed_files"`
	TestFiles        []plannedTestFile    `json:"test_files,omitempty"`
	MissingTests     []plannedMissingTest `json:"missing_tests,omitempty"`
	Commands         []string             `json:"commands,omitempty"`
	ExpandedCommands []string             `json:"expanded_commands,omitempty"`
	BroadCommands    []string             `json:"broad_commands,omitempty"`
	GoCommands       []string             `json:"go_commands,omitempty"`
	Reasons          []string             `json:"reasons,omitempty"`
}

type plannedTestFile struct {
	Path    string   `json:"path"`
	Reasons []string `json:"reasons"`
}

type plannedMissingTest struct {
	Automation string `json:"automation"`
	Test       string `json:"test"`
	Reason     string `json:"reason"`
}

func init() {
	testPlanCmd.Flags().BoolVar(&testPlanJSON, "json", false, "Emit a machine-readable JSON summary")
	testCmd.AddCommand(testPlanCmd)
}

func runTestPlan(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("test", testPlanJSON, cmd.OutOrStdout())
	rt.SetProfile("plan")

	changed, changedErr := changedFilesForReview()
	if changedErr != nil {
		rt.AddStep(operator.Step{
			ID:      "plan-tests",
			Title:   "Plan impacted tests",
			Status:  operator.StatusFailure,
			Summary: changedErr.Error(),
		})
		return rt.Complete(operator.StatusFailure, "test plan failed")
	}
	summary, err := buildTestPlanSummary(changed)
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "plan-tests",
			Title:   "Plan impacted tests",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "test plan failed")
	}

	step := testPlanStep(summary)
	rt.AddStep(step)

	if !testPlanJSON {
		for _, command := range testPlanNextCommands(summary) {
			fmt.Fprintln(cmd.OutOrStdout(), command)
		}
	}

	return rt.Complete(step.Status, step.Summary)
}

func buildTestPlanSummary(changed []string) (*testPlanSummary, error) {
	if changed == nil {
		var err error
		changed, err = changedFilesForReview()
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(changed)

	summary := &testPlanSummary{
		ChangedFiles: changed,
		Commands:     []string{"./dm test doctor --compact --json"},
	}

	tests := map[string]*plannedTestFile{}
	addTest := func(path, reason string) {
		if path == "" {
			return
		}
		path = filepath.ToSlash(path)
		entry := tests[path]
		if entry == nil {
			entry = &plannedTestFile{Path: path}
			tests[path] = entry
		}
		entry.Reasons = appendUnique(entry.Reasons, reason)
	}
	addMissing := func(automation, test, reason string) {
		if test == "" {
			return
		}
		summary.MissingTests = append(summary.MissingTests, plannedMissingTest{
			Automation: automation,
			Test:       filepath.ToSlash(test),
			Reason:     reason,
		})
	}

	blueprintUses, _ := automationBlueprintUses()
	blueprintConsumers := map[string][]string{}
	for _, use := range blueprintUses {
		blueprintConsumers[use.Path] = append(blueprintConsumers[use.Path], use.File)
	}

	for _, path := range changed {
		if relativeConfigPath(path) != "" && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			if content, err := os.ReadFile(path); err == nil {
				if refs, err := hayaml.ExtractReferences(content); err == nil {
					for _, ref := range refs.Dynamic {
						summary.Reasons = appendUnique(summary.Reasons, fmt.Sprintf("%s:%d: %s Static test selection cannot prove coverage of this target.", filepath.ToSlash(path), ref.Line, ref.Reason))
					}
					if len(refs.Dynamic) > 0 {
						summary.BroadCommands = appendUnique(summary.BroadCommands, "./dm test --json")
					}
				}
			}
		}
		switch {
		case isTestFile(path):
			// Deleted test files still appear in the diff. Skip them so
			// `dm test --changed` does not try to load a missing path.
			if fileExists(path) {
				addTest(path, "changed test file")
			} else {
				summary.Reasons = append(
					summary.Reasons,
					fmt.Sprintf("changed test file %s is deleted; not scheduled", filepath.ToSlash(path)),
				)
			}
		case isAutomationFile(path):
			testPath := expectedAutomationTest(path)
			if fileExists(testPath) {
				addTest(testPath, "changed automation "+path)
			} else {
				addMissing(path, testPath, "changed automation has no conventional test")
			}
		case isAutomationBlueprintFile(path):
			rel := blueprintRelPath(path)
			consumers := uniqueStrings(blueprintConsumers[rel])
			sort.Strings(consumers)
			if len(consumers) == 0 {
				summary.Reasons = append(summary.Reasons, fmt.Sprintf("blueprint %s changed but no local automation consumers were found", rel))
			}
			for _, automation := range consumers {
				testPath := expectedAutomationTest(automation)
				if fileExists(testPath) {
					addTest(testPath, "changed blueprint "+rel)
				} else {
					addMissing(automation, testPath, "automation uses changed blueprint "+rel)
				}
			}
			summary.BroadCommands = appendUnique(summary.BroadCommands, "./dm check dev --json")
		case isHAConfigEntityImpactFile(path):
			for _, entity := range extractEntityReferencesFromFile(path) {
				for _, testPath := range testsReferencingEntity(entity) {
					addTest(testPath, "changed entity reference "+entity)
				}
			}
		}

		if command := goTestCommandForChange(path); command != "" {
			summary.GoCommands = appendUnique(summary.GoCommands, command)
		}
	}

	for _, test := range mapValues(tests) {
		sort.Strings(test.Reasons)
		summary.TestFiles = append(summary.TestFiles, *test)
	}
	sort.Slice(summary.TestFiles, func(i, j int) bool {
		return summary.TestFiles[i].Path < summary.TestFiles[j].Path
	})
	sort.Slice(summary.MissingTests, func(i, j int) bool {
		return summary.MissingTests[i].Test < summary.MissingTests[j].Test
	})

	if len(summary.TestFiles) > 0 {
		summary.Commands = append(summary.Commands, "./dm test --changed --json")
		summary.ExpandedCommands = append(summary.ExpandedCommands, testRunCommand(plannedTestPaths(summary.TestFiles), false))
	}
	summary.Commands = append(summary.Commands, summary.GoCommands...)
	if len(summary.BroadCommands) == 0 && anyHAConfigChanged(changed) {
		summary.BroadCommands = append(summary.BroadCommands, "./dm check --json")
	}
	if anyDocsChanged(changed) || commandImplementationChanged(changed) {
		summary.BroadCommands = appendUnique(summary.BroadCommands, "./dm docs generate --check --json")
	}

	summary.Commands = uniqueStrings(summary.Commands)
	summary.ExpandedCommands = uniqueStrings(summary.ExpandedCommands)
	summary.BroadCommands = uniqueStrings(summary.BroadCommands)
	summary.GoCommands = uniqueStrings(summary.GoCommands)
	summary.Reasons = uniqueStrings(summary.Reasons)
	return summary, nil
}

func testPlanStep(summary *testPlanSummary) operator.Step {
	status := operator.StatusSuccess
	stepSummary := testPlanSummaryText(summary)
	if len(summary.MissingTests) > 0 {
		status = operator.StatusWarning
	}

	details := map[string]any{
		"changed_files": summary.ChangedFiles,
		"test_files":    summary.TestFiles,
		"commands":      summary.Commands,
		"next_commands": testPlanNextCommands(summary),
	}
	if len(summary.ExpandedCommands) > 0 {
		details["expanded_commands"] = summary.ExpandedCommands
	}
	if len(summary.MissingTests) > 0 {
		details["missing_tests"] = summary.MissingTests
		details["failures"] = testPlanMissingIssues(summary.MissingTests)
	}
	if len(summary.BroadCommands) > 0 {
		details["broad_commands"] = summary.BroadCommands
	}
	if len(summary.GoCommands) > 0 {
		details["go_commands"] = summary.GoCommands
	}
	if len(summary.Reasons) > 0 {
		details["reasons"] = summary.Reasons
	}

	step := operator.Step{
		ID:           "plan-tests",
		Title:        "Plan impacted tests",
		Status:       status,
		Summary:      stepSummary,
		Artifacts:    plannedTestPaths(summary.TestFiles),
		NextCommands: testPlanNextCommands(summary),
		Details:      details,
	}
	if len(summary.TestFiles) == 0 {
		step.Hints = append(step.Hints, "Run `./dm test --tags fast --json` or `./dm check --json` when you still want a baseline sweep.")
	}
	if len(summary.MissingTests) > 0 {
		step.Hints = append(step.Hints, "Add missing conventional tests or document why those automations are covered another way.")
	}
	return step
}

func testPlanSummaryText(summary *testPlanSummary) string {
	if summary == nil {
		return "test plan unavailable"
	}
	if len(summary.MissingTests) > 0 {
		return fmt.Sprintf("%d impacted test file(s), %d missing conventional test(s)", len(summary.TestFiles), len(summary.MissingTests))
	}
	if len(summary.TestFiles) == 0 {
		return "no impacted automation tests found"
	}
	return fmt.Sprintf("%d impacted test file(s)", len(summary.TestFiles))
}

func testPlanNextCommands(summary *testPlanSummary) []string {
	if summary == nil {
		return nil
	}
	return uniqueStrings(append(append([]string{}, summary.Commands...), summary.BroadCommands...))
}

func testPlanMissingIssues(missing []plannedMissingTest) []agentIssue {
	issues := make([]agentIssue, 0, len(missing))
	for _, item := range missing {
		issues = append(issues, agentIssue{
			ID:       "impacted-test-missing",
			Severity: "warning",
			File:     item.Automation,
			Summary:  "impacted automation has no conventional test",
			Details:  fmt.Sprintf("expected %s (%s)", item.Test, item.Reason),
			Hint:     fmt.Sprintf("Add %s or document alternate coverage.", item.Test),
		})
	}
	return issues
}

func plannedTestPaths(tests []plannedTestFile) []string {
	paths := make([]string, 0, len(tests))
	for _, test := range tests {
		paths = append(paths, test.Path)
	}
	return paths
}

func testRunCommand(files []string, trace bool) string {
	parts := []string{"./dm", "test", "--json"}
	if trace {
		parts = append(parts, "--trace")
	}
	for _, file := range files {
		parts = append(parts, shellQuote(file))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("/._-:0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isTestFile(path string) bool {
	return strings.HasPrefix(relativeConfigPath(path), "tests/") && strings.HasSuffix(path, "_test.yaml")
}

func isAutomationBlueprintFile(path string) bool {
	return strings.HasPrefix(relativeConfigPath(path), "blueprints/automation/") && strings.HasSuffix(path, ".yaml")
}

func isHAConfigEntityImpactFile(path string) bool {
	if relativeConfigPath(path) == "" || !strings.HasSuffix(path, ".yaml") {
		return false
	}
	for _, prefix := range []string{
		"dashboards/",
		"homekit/",
		"zigbee2mqtt/",
	} {
		if strings.HasPrefix(relativeConfigPath(path), prefix) {
			return false
		}
	}
	return true
}

func blueprintRelPath(path string) string {
	return normalizeAutomationBlueprintPath(strings.TrimPrefix(relativeConfigPath(path), "blueprints/automation/"))
}

func extractEntityReferencesFromFile(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return extractEntityReferences(string(content))
}

func extractEntityReferences(content string) []string {
	refs, err := hayaml.ExtractReferences([]byte(content))
	if err != nil {
		return nil
	}
	var matches []string
	for _, ref := range refs.Static {
		matches = append(matches, ref.Entity)
	}
	return uniqueStrings(matches)
}

func testsReferencingEntity(entity string) []string {
	files, err := allTestFiles()
	if err != nil {
		return nil
	}
	var matches []string
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, ref := range extractEntityReferences(string(content)) {
			if ref == entity {
				matches = append(matches, file)
				break
			}
		}
	}
	sort.Strings(matches)
	return matches
}

func allTestFiles() ([]string, error) {
	matches, err := globUnderConfig("tests/**/*_test.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mapValues[K comparable, V any](m map[K]*V) []*V {
	values := make([]*V, 0, len(m))
	for _, value := range m {
		values = append(values, value)
	}
	return values
}
