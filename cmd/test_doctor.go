package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hatest"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	testDoctorJSON    bool
	testDoctorCompact bool
)

var testDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run static automation-test hygiene checks",
	RunE:  runTestDoctor,
}

type testDoctorSummary struct {
	RuntimeRequirements []string                `json:"runtime_requirements,omitempty"`
	FilesChecked        int                     `json:"files_checked"`
	CasesChecked        int                     `json:"cases_checked"`
	IssueCount          int                     `json:"issue_count"`
	Stats               testConfidenceStats     `json:"stats"`
	Issues              []agentIssue            `json:"issues,omitempty"`
	IssueGroups         []testDoctorIssueGroup  `json:"issue_groups,omitempty"`
	FilesWithIssues     []testDoctorFileSummary `json:"files_with_issues,omitempty"`
	NextCommands        []string                `json:"next_commands,omitempty"`
}

type testConfidenceStats struct {
	TestFiles                      int `json:"test_files"`
	FastTaggedFiles                int `json:"fast_tagged_files"`
	TraceAssertionFiles            int `json:"trace_assertion_files"`
	AutomationTriggerFiles         int `json:"automation_trigger_files"`
	TestCases                      int `json:"test_cases"`
	NaturalTriggerCases            int `json:"natural_trigger_cases"`
	NaturalTriggerCasesWithTrace   int `json:"natural_trigger_cases_with_trace"`
	ExplicitCleanupGapFiles        int `json:"explicit_cleanup_gap_files"`
	LoadFailures                   int `json:"load_failures"`
	StaleTraceAutomationReferences int `json:"stale_trace_automation_references"`
}

type testDoctorIssueGroup struct {
	ID          string   `json:"id"`
	Severity    string   `json:"severity"`
	Count       int      `json:"count"`
	Files       []string `json:"files"`
	Hint        string   `json:"hint,omitempty"`
	NextCommand string   `json:"next_command,omitempty"`
}

type testDoctorFileSummary struct {
	File               string   `json:"file"`
	IssueCount         int      `json:"issue_count"`
	IssueIDs           []string `json:"issue_ids"`
	ValidationCommands []string `json:"validation_commands"`
}

var automationSlugInvalidRe = regexp.MustCompile(`[^a-z0-9_]+`)

func init() {
	testDoctorCmd.Flags().BoolVar(&testDoctorJSON, "json", false, "Emit a machine-readable JSON summary")
	testDoctorCmd.Flags().BoolVar(&testDoctorCompact, "compact", false, "Omit per-case failure details and emit grouped summaries")
	testCmd.AddCommand(testDoctorCmd)
}

func runTestDoctor(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("test", testDoctorJSON, cmd.OutOrStdout())
	rt.SetProfile("doctor")

	summary, err := buildTestDoctorSummary()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "doctor-tests",
			Title:   "Inspect automation test hygiene",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "test doctor failed")
	}

	step := testDoctorStep(summary, testDoctorCompact)
	rt.AddStep(step)
	if step.Status == operator.StatusWarning {
		rt.AddHint("Repair the static test hygiene findings, then rerun `./dm test doctor --compact --json`.")
	}

	if !testDoctorJSON {
		if testDoctorCompact {
			for _, group := range summary.IssueGroups {
				fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %d issue(s) across %d file(s)\n", group.ID, group.Severity, group.Count, len(group.Files))
				if group.Hint != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Hint: %s\n", group.Hint)
				}
			}
		} else {
			for _, issue := range summary.Issues {
				location := issue.File
				if issue.Line > 0 {
					location = fmt.Sprintf("%s:%d", issue.File, issue.Line)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %s\n", location, issue.ID, issue.Summary)
				if issue.Details != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Details: %s\n", issue.Details)
				}
				if issue.Hint != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Hint: %s\n", issue.Hint)
				}
			}
		}
	}

	return rt.Complete(step.Status, step.Summary)
}

func buildTestDoctorSummary() (*testDoctorSummary, error) {
	files, err := allTestFiles()
	if err != nil {
		return nil, err
	}

	knownAutomations := knownAutomationEntityIDs()
	cleanupGapFiles := map[string]bool{}
	traceFiles := map[string]bool{}
	automationTriggerFiles := map[string]bool{}

	summary := &testDoctorSummary{
		FilesChecked: len(files),
		Stats: testConfidenceStats{
			TestFiles: len(files),
		},
		NextCommands: []string{"./dm test doctor --compact --json", "./dm agent tasks --json", "./dm test doctor --json", "./dm test plan --json"},
	}

	for _, file := range files {
		spec, err := hatest.LoadTestSpec(file)
		if err != nil {
			summary.Stats.LoadFailures++
			summary.Issues = append(summary.Issues, agentIssue{
				ID:       "test-load-failed",
				Severity: "error",
				File:     file,
				Summary:  "test spec could not be loaded",
				Details:  err.Error(),
				Hint:     fmt.Sprintf("Run `./dm test %s -v` after fixing the YAML/spec error.", file),
			})
			continue
		}

		if hasTag(spec.Tags, "fast") {
			summary.Stats.FastTaggedFiles++
		} else {
			summary.Issues = append(summary.Issues, agentIssue{
				ID:       "test-missing-fast-tag",
				Severity: "warning",
				File:     file,
				Summary:  "test file is not tagged fast",
				Hint:     "Add the `fast` tag unless the test is intentionally excluded from the local baseline.",
			})
		}

		seenNames := map[string]bool{}
		walkDoctorTestCases(spec.Tests, "", func(fullName string, tc hatest.TestCase) {
			summary.CasesChecked++
			summary.Stats.TestCases++

			if seenNames[fullName] {
				summary.Issues = append(summary.Issues, agentIssue{
					ID:       "test-duplicate-name",
					Severity: "warning",
					File:     file,
					Summary:  "test case name is duplicated",
					Details:  fullName,
					Hint:     "Use unique test names so `./dm test --case ...` reruns one stable case.",
				})
			}
			seenNames[fullName] = true
			for _, call := range append(append([]hatest.ServiceCall{}, tc.Trigger...), tc.Cleanup...) {
				if call.Target.DeviceID != "" || len(call.Target.Devices) > 0 || call.Target.AreaID != "" || len(call.Target.Areas) > 0 || call.Data["device_id"] != nil || call.Data["area_id"] != nil {
					summary.RuntimeRequirements = appendUnique(summary.RuntimeRequirements, fmt.Sprintf("%s: %s requires live HA extract_from_target for device/area membership; static cleanup coverage is incomplete until runtime expansion.", file, fullName))
				}
			}
			if tc.TraceAssertions != nil && tc.TraceAssertions.Attribution == "fresh_unique" {
				summary.RuntimeRequirements = appendUnique(summary.RuntimeRequirements, fmt.Sprintf("%s: %s uses isolated-window trace attribution: %s", file, fullName, tc.TraceAssertions.AttributionReason))
			}

			if len(tc.Assertions) == 0 && len(tc.Subtests) == 0 {
				summary.Issues = append(summary.Issues, agentIssue{
					ID:       "test-without-asserted-target",
					Severity: "warning",
					File:     file,
					Summary:  "test case has no asserted target entity",
					Details:  fullName,
					Hint:     "Assert at least one entity state or move this check into a clearer smoke test.",
				})
			}

			if testCaseUsesAutomationTrigger(tc) {
				automationTriggerFiles[file] = true
			}
			if testCaseUsesAutomationTrigger(tc) && strings.TrimSpace(tc.AutomationTriggerReason) == "" {
				summary.Issues = append(summary.Issues, agentIssue{
					ID:       "test-uses-automation-trigger",
					Severity: "warning",
					File:     file,
					Summary:  "test uses automation.trigger instead of the natural trigger path",
					Details:  fullName,
					Hint:     "Prefer mock_entities.set_state or events: so Home Assistant evaluates the real trigger context.",
				})
			}

			if hasUnsupportedTriggerPattern(tc) {
				summary.Issues = append(summary.Issues, agentIssue{
					ID:       "test-unsupported-trigger-pattern",
					Severity: "warning",
					File:     file,
					Summary:  "test trigger uses a pattern the local framework cannot fully validate",
					Details:  fullName,
					Hint:     "Use events: for HA events, mock_entities.set_state for state triggers, and mock_entities.fire_mqtt_message for local MQTT triggers.",
				})
			}

			natural := testCaseUsesNaturalTrigger(tc)
			if natural {
				summary.Stats.NaturalTriggerCases++
				if tc.TraceAssertions != nil && tc.TraceAssertions.Automation != "" {
					summary.Stats.NaturalTriggerCasesWithTrace++
				} else if strings.TrimSpace(tc.TraceSkipReason) == "" {
					summary.Issues = append(summary.Issues, agentIssue{
						ID:       "natural-trigger-without-trace-assertions",
						Severity: "warning",
						File:     file,
						Summary:  "natural-trigger test is missing trace assertions",
						Details:  fullName,
						Hint:     "Add trace_assertions with the automation entity, expected branch, and expected service action when the path is traceable.",
					})
				}
			}

			if tc.TraceAssertions != nil && tc.TraceAssertions.Automation != "" {
				traceFiles[file] = true
				if len(knownAutomations) > 0 && !knownAutomations[tc.TraceAssertions.Automation] {
					summary.Stats.StaleTraceAutomationReferences++
					summary.Issues = append(summary.Issues, agentIssue{
						ID:       "trace-automation-not-found",
						Severity: "warning",
						File:     file,
						Summary:  "trace_assertions references an automation entity that was not found locally",
						Details:  fmt.Sprintf("%s -> %s", fullName, tc.TraceAssertions.Automation),
						Hint:     "Confirm the automation alias/entity_id or update the trace assertion.",
					})
				}
			}

			if spec.Config.Cleanup {
				missing := missingCleanupEntities(tc)
				if len(missing) > 0 {
					cleanupGapFiles[file] = true
					summary.Issues = append(summary.Issues, agentIssue{
						ID:       "test-explicit-cleanup-gap",
						Severity: "warning",
						File:     file,
						Summary:  "test touches entities that explicit cleanup does not restore",
						Details:  fmt.Sprintf("%s: %s", fullName, strings.Join(limitStrings(missing, 8), ", ")),
						Hint:     "Add explicit cleanup for touched entities; the runner also snapshots and restores as a backstop.",
					})
				}
			}
		})
	}

	summary.Stats.TraceAssertionFiles = len(traceFiles)
	summary.Stats.AutomationTriggerFiles = len(automationTriggerFiles)
	summary.Stats.ExplicitCleanupGapFiles = len(cleanupGapFiles)
	summary.IssueCount = len(summary.Issues)
	sortAgentIssues(summary.Issues)
	summary.IssueGroups = groupDoctorIssues(summary.Issues)
	summary.FilesWithIssues = summarizeDoctorFiles(summary.Issues)
	return summary, nil
}

func testDoctorStep(summary *testDoctorSummary, compact bool) operator.Step {
	status := operator.StatusSuccess
	if summary.IssueCount > 0 {
		status = operator.StatusWarning
	}
	for _, issue := range summary.Issues {
		if issue.Severity == "error" {
			status = operator.StatusFailure
			break
		}
	}

	details := map[string]any{
		"files_checked":        summary.FilesChecked,
		"cases_checked":        summary.CasesChecked,
		"issue_count":          summary.IssueCount,
		"stats":                summary.Stats,
		"runtime_requirements": summary.RuntimeRequirements,
		"next_commands":        summary.NextCommands,
	}
	if len(summary.Issues) > 0 {
		details["artifacts"] = issueArtifacts(summary.Issues)
		details["issue_groups"] = summary.IssueGroups
		details["files_with_issues"] = summary.FilesWithIssues
		if !compact {
			details["failures"] = summary.Issues
		}
	}
	safeReproCommand := "./dm test doctor --json"
	if compact {
		safeReproCommand = "./dm test doctor --compact --json"
	}
	return operator.Step{
		ID:               "doctor-tests",
		Title:            "Inspect automation test hygiene",
		Status:           status,
		Summary:          fmt.Sprintf("%d test file(s), %d case(s), %d hygiene issue(s)", summary.FilesChecked, summary.CasesChecked, summary.IssueCount),
		NextCommands:     summary.NextCommands,
		SafeReproCommand: safeReproCommand,
		Details:          details,
	}
}

func groupDoctorIssues(issues []agentIssue) []testDoctorIssueGroup {
	byID := map[string]*testDoctorIssueGroup{}
	fileSets := map[string]map[string]bool{}
	for _, issue := range issues {
		if issue.ID == "" {
			continue
		}
		group := byID[issue.ID]
		if group == nil {
			group = &testDoctorIssueGroup{
				ID:          issue.ID,
				Severity:    issue.Severity,
				Hint:        issue.Hint,
				NextCommand: "./dm test doctor --json",
			}
			byID[issue.ID] = group
			fileSets[issue.ID] = map[string]bool{}
		}
		group.Count++
		if issueSeverityRank(issue.Severity) > issueSeverityRank(group.Severity) {
			group.Severity = issue.Severity
		}
		if issue.Hint != "" && group.Hint == "" {
			group.Hint = issue.Hint
		}
		if issue.File != "" {
			fileSets[issue.ID][issue.File] = true
		}
	}

	groups := make([]testDoctorIssueGroup, 0, len(byID))
	for id, group := range byID {
		for file := range fileSets[id] {
			group.Files = append(group.Files, file)
		}
		sort.Strings(group.Files)
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count == groups[j].Count {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].Count > groups[j].Count
	})
	return groups
}

func summarizeDoctorFiles(issues []agentIssue) []testDoctorFileSummary {
	type fileAccumulator struct {
		count int
		ids   map[string]bool
	}
	byFile := map[string]*fileAccumulator{}
	for _, issue := range issues {
		file := issue.File
		if file == "" {
			file = "ha-config/tests"
		}
		acc := byFile[file]
		if acc == nil {
			acc = &fileAccumulator{ids: map[string]bool{}}
			byFile[file] = acc
		}
		acc.count++
		if issue.ID != "" {
			acc.ids[issue.ID] = true
		}
	}

	files := make([]testDoctorFileSummary, 0, len(byFile))
	for file, acc := range byFile {
		ids := make([]string, 0, len(acc.ids))
		for id := range acc.ids {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		files = append(files, testDoctorFileSummary{
			File:               file,
			IssueCount:         acc.count,
			IssueIDs:           ids,
			ValidationCommands: doctorValidationCommandsForFile(file),
		})
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].IssueCount == files[j].IssueCount {
			return files[i].File < files[j].File
		}
		return files[i].IssueCount > files[j].IssueCount
	})
	return files
}

func doctorValidationCommandsForFile(file string) []string {
	commands := []string{"./dm test doctor --json"}
	if strings.HasPrefix(file, "ha-config/tests/") {
		commands = append(commands, fmt.Sprintf("./dm test %s --json", file))
	}
	return commands
}

func issueSeverityRank(severity string) int {
	switch severity {
	case "error":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

func walkDoctorTestCases(cases []hatest.TestCase, parent string, visit func(string, hatest.TestCase)) {
	for _, tc := range cases {
		fullName := tc.Name
		if parent != "" {
			fullName = parent + " > " + tc.Name
		}
		visit(fullName, tc)
		walkDoctorTestCases(tc.Subtests, fullName, visit)
	}
}

func hasTag(tags []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, tag := range tags {
		if strings.ToLower(strings.TrimSpace(tag)) == want {
			return true
		}
	}
	return false
}

func testCaseUsesAutomationTrigger(tc hatest.TestCase) bool {
	for _, trigger := range tc.Trigger {
		if trigger.Service == "automation.trigger" {
			return true
		}
	}
	return false
}

func testCaseUsesNaturalTrigger(tc hatest.TestCase) bool {
	if len(tc.Events) > 0 {
		return true
	}
	for _, trigger := range tc.Trigger {
		if trigger.Service == "mock_entities.set_state" || trigger.Service == "mock_entities.fire_mqtt_message" {
			return true
		}
		// Input booleans are the portable runtime's synthetic state triggers.
		// This identifies a candidate path; trace assertions supply execution evidence.
		if trigger.Service == "input_boolean.turn_on" || trigger.Service == "input_boolean.turn_off" || trigger.Service == "input_boolean.toggle" {
			return true
		}
	}
	return false
}

func hasUnsupportedTriggerPattern(tc hatest.TestCase) bool {
	for _, trigger := range tc.Trigger {
		if trigger.Service == "" || !strings.Contains(trigger.Service, ".") {
			return true
		}
		if trigger.Service == "mqtt.publish" {
			return true
		}
	}
	for _, event := range tc.Events {
		if event.EventType == "" {
			return true
		}
	}
	return false
}

func missingCleanupEntities(tc hatest.TestCase) []string {
	touched := testCaseTouchedEntities(tc)
	cleaned := map[string]bool{}
	for _, call := range tc.Cleanup {
		for _, entity := range serviceCallEntities(call) {
			cleaned[entity] = true
		}
	}
	var missing []string
	for entity := range touched {
		if !cleaned[entity] && !strings.HasPrefix(entity, "automation.") {
			missing = append(missing, entity)
		}
	}
	sort.Strings(missing)
	return missing
}

func testCaseTouchedEntities(tc hatest.TestCase) map[string]bool {
	entities := map[string]bool{}
	for _, action := range tc.Setup {
		if action.EntityID != "" {
			entities[action.EntityID] = true
		}
	}
	for _, trigger := range tc.Trigger {
		for _, entity := range serviceCallEntities(trigger) {
			entities[entity] = true
		}
	}
	for _, assertion := range tc.Assertions {
		// Sensor assertions alone do not require explicit cleanup in this check.
		// Sensors named in setup or triggers are already included above.
		if assertion.EntityID != "" && !strings.HasPrefix(assertion.EntityID, "sensor.") && !strings.HasPrefix(assertion.EntityID, "binary_sensor.") {
			entities[assertion.EntityID] = true
		}
	}
	return entities
}

func serviceCallEntities(call hatest.ServiceCall) []string {
	var entities []string
	if call.Target.EntityID != "" {
		entities = append(entities, call.Target.EntityID)
	}
	entities = append(entities, call.Target.Entities...)
	entities = append(entities, entitiesFromAny(call.Data["entity_id"])...)
	sort.Strings(entities)
	return uniqueStrings(entities)
}

func entitiesFromAny(value interface{}) []string {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, ".") {
			return []string{v}
		}
	case []string:
		var entities []string
		for _, item := range v {
			if strings.Contains(item, ".") {
				entities = append(entities, item)
			}
		}
		return entities
	case []interface{}:
		var entities []string
		for _, item := range v {
			entities = append(entities, entitiesFromAny(item)...)
		}
		return entities
	case map[string]interface{}:
		var entities []string
		for _, item := range v {
			entities = append(entities, entitiesFromAny(item)...)
		}
		return entities
	}
	return nil
}

func knownAutomationEntityIDs() map[string]bool {
	rows, err := collectAutomationDocs()
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, row := range rows {
		for _, entity := range []string{
			automationEntityIDFromAlias(row.Alias),
			automationEntityIDFromAlias(row.ID),
			"automation." + strings.TrimSuffix(filepath.Base(row.Path), filepath.Ext(row.Path)),
		} {
			if entity != "automation." {
				known[entity] = true
			}
		}
	}
	// Include optional recovery fixtures that automation discovery does not scan.
	if content, err := os.ReadFile(configFile("tests/fixtures/recovery_automations.yaml")); err == nil {
		var fixtures []struct {
			Alias string `yaml:"alias"`
		}
		if yaml.Unmarshal(content, &fixtures) == nil {
			for _, fixture := range fixtures {
				known[automationEntityIDFromAlias(fixture.Alias)] = true
			}
		}
	}
	return known
}

func automationEntityIDFromAlias(alias string) string {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return ""
	}
	alias = strings.ReplaceAll(alias, "&", "and")
	alias = automationSlugInvalidRe.ReplaceAllString(alias, "_")
	alias = strings.Trim(alias, "_")
	alias = regexp.MustCompile(`_+`).ReplaceAllString(alias, "_")
	if alias == "" {
		return ""
	}
	return "automation." + alias
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	limited := append([]string{}, values[:limit]...)
	limited = append(limited, fmt.Sprintf("...and %d more", len(values)-limit))
	return limited
}

func sortAgentIssues(issues []agentIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].File == issues[j].File {
			if issues[i].ID == issues[j].ID {
				return issues[i].Summary < issues[j].Summary
			}
			return issues[i].ID < issues[j].ID
		}
		return issues[i].File < issues[j].File
	})
}
