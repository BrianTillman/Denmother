package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	agentReviewJSON         bool
	agentReviewBase         string
	agentReviewChangedFiles []string
	agentReviewWarningsOK   bool
)

var agentReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review the current diff for agent-operable risks",
	RunE:  runAgentReview,
}

type reviewSummary struct {
	ChangedFiles []string         `json:"changed_files"`
	Issues       []agentIssue     `json:"issues,omitempty"`
	NextCommands []string         `json:"next_commands,omitempty"`
	TestPlan     *testPlanSummary `json:"test_plan,omitempty"`
}

func init() {
	agentReviewCmd.Flags().BoolVar(&agentReviewJSON, "json", false, "Emit a machine-readable JSON summary")
	agentReviewCmd.Flags().StringVar(&agentReviewBase, "base", "", "Review files changed between the given git ref and HEAD")
	agentReviewCmd.Flags().StringSliceVar(&agentReviewChangedFiles, "changed-file", []string{}, "Review an explicit changed file path; repeat or comma-separate")
	agentReviewCmd.Flags().BoolVar(&agentReviewWarningsOK, "warnings-ok", false, "Exit successfully when review findings are warnings; errors still fail")
	agentCmd.AddCommand(agentReviewCmd)
}

func runAgentReview(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentReviewJSON, cmd.OutOrStdout())
	rt.SetProfile("review")

	changed, err := changedFilesForReviewInputs()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "collect-changed-files",
			Title:   "Collect changed files",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "review could not collect changed files")
	}

	summary := buildReviewSummary(changed)
	step := agentReviewStep(summary)
	rt.AddStep(step)
	finalStatus := finalAgentReviewStatus(step.Status, agentReviewWarningsOK)
	if step.Status == operator.StatusWarning && finalStatus == operator.StatusSuccess {
		rt.AddHint("Review warnings were reported but did not fail because --warnings-ok was set.")
	}

	if !agentReviewJSON {
		if len(summary.ChangedFiles) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No local changes to review.")
		}
		for _, issue := range summary.Issues {
			location := issue.File
			if issue.Line > 0 {
				location = fmt.Sprintf("%s:%d", issue.File, issue.Line)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %s\n", location, issue.ID, issue.Summary)
			if issue.Hint != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Hint: %s\n", issue.Hint)
			}
		}
	}

	return rt.Complete(finalStatus, agentReviewSummaryText(step.Status, summary))
}

func finalAgentReviewStatus(status operator.Status, warningsOK bool) operator.Status {
	if warningsOK && status == operator.StatusWarning {
		return operator.StatusSuccess
	}
	return status
}

func buildReviewSummary(changed []string) reviewSummary {
	testPlan, _ := buildTestPlanSummary(changed)
	summary := reviewSummary{
		ChangedFiles: changed,
		NextCommands: reviewNextCommands(changed, testPlan),
		TestPlan:     testPlan,
	}
	summary.Issues = append(summary.Issues, reviewAutomationTests(changed)...)
	summary.Issues = append(summary.Issues, reviewAutomationStableIDs(changed)...)
	summary.Issues = append(summary.Issues, reviewBlueprintChanges(changed)...)
	summary.Issues = append(summary.Issues, reviewHomeKitChanges(changed)...)
	summary.Issues = append(summary.Issues, reviewProductionRisk(changed)...)
	summary.Issues = append(summary.Issues, reviewDocsDrift(changed)...)
	return summary
}

func reviewAutomationStableIDs(changed []string) []agentIssue {
	var issues []agentIssue
	for _, path := range changed {
		if !isAutomationFile(path) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}
		for _, automation := range automationMappings(&doc) {
			if yamlScalarValue(mapValue(automation, "id")) != "" {
				continue
			}
			issues = append(issues, agentIssue{
				ID:       "automation-missing-stable-id",
				Severity: "warning",
				File:     path,
				Line:     automation.Line,
				Summary:  "changed automation does not declare a stable id",
				Hint:     "Add an id when the automation should have trace, UI, or registry stability; document exceptions for blueprint-only generated IDs.",
			})
		}
	}
	return issues
}

func agentReviewStep(summary reviewSummary) operator.Step {
	status := operator.StatusSuccess
	for _, issue := range summary.Issues {
		switch issue.Severity {
		case "error":
			status = operator.StatusFailure
		case "warning":
			if status == operator.StatusSuccess {
				status = operator.StatusWarning
			}
		}
	}

	details := map[string]any{
		"changed_files": summary.ChangedFiles,
		"issue_count":   len(summary.Issues),
	}
	if len(summary.Issues) > 0 {
		details["failures"] = summary.Issues
		details["artifacts"] = issueArtifacts(summary.Issues)
	}
	if len(summary.NextCommands) > 0 {
		details["next_commands"] = summary.NextCommands
	}
	if summary.TestPlan != nil {
		details["test_plan"] = summary.TestPlan
	}

	step := operator.Step{
		ID:      "agent-review",
		Title:   "Review current diff",
		Status:  status,
		Summary: fmt.Sprintf("%d changed file(s), %d review finding(s)", len(summary.ChangedFiles), len(summary.Issues)),
		Details: details,
	}
	if status == operator.StatusSuccess {
		step.Summary = fmt.Sprintf("%d changed file(s), no blocking review findings", len(summary.ChangedFiles))
	}
	step.Hints = append(step.Hints, summary.NextCommands...)
	return step
}

func agentReviewSummaryText(status operator.Status, summary reviewSummary) string {
	if len(summary.ChangedFiles) == 0 {
		return "no local changes to review"
	}
	if status == operator.StatusSuccess {
		return fmt.Sprintf("review passed for %d changed file(s)", len(summary.ChangedFiles))
	}
	return fmt.Sprintf("review found %d issue(s) across %d changed file(s)", len(summary.Issues), len(summary.ChangedFiles))
}

func changedFilesForReview() ([]string, error) {
	if err := commandContext().Err(); err != nil {
		return nil, err
	}
	root, err := projectGitCommand("rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("changed-file discovery failed: %w", err)
	}
	queries := [][]string{{"diff", "--name-only", "-z", "HEAD", "--"}, {"ls-files", "--others", "--exclude-standard", "--full-name", "-z"}}
	if err := projectGitCommand("rev-parse", "--verify", "HEAD").Run(); err != nil {
		// A newly initialized repository has no HEAD; its index is entirely new.
		queries[0] = []string{"ls-files", "--cached", "--full-name", "-z"}
	}
	seen := map[string]bool{}
	var changed []string
	add := func(path string) {
		path = normalizeGitPath(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		changed = append(changed, path)
	}

	var discoveryErrs []string
	for _, args := range queries {
		if err := commandContext().Err(); err != nil {
			return nil, err
		}
		output, err := projectGitCommand(args...).CombinedOutput()
		if err != nil {
			discoveryErrs = append(discoveryErrs, fmt.Sprintf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output))))
			continue
		}
		for _, line := range strings.Split(string(output), "\x00") {
			add(strings.TrimSpace(line))
		}
	}
	if err := commandContext().Err(); err != nil {
		return nil, err
	}
	if len(discoveryErrs) > 0 {
		// Failed discovery leaves the test selection unknown.
		return nil, fmt.Errorf("changed-file discovery failed: %s", strings.Join(discoveryErrs, "; "))
	}
	sort.Strings(changed)
	return gitReviewPaths(strings.TrimSpace(string(root)), changed), nil
}

func changedFilesForReviewInputs() ([]string, error) {
	if len(agentReviewChangedFiles) > 0 {
		return normalizeChangedFiles(agentReviewChangedFiles), nil
	}
	if strings.TrimSpace(agentReviewBase) != "" {
		return changedFilesForReviewBase(agentReviewBase)
	}
	return changedFilesForReview()
}

func changedFilesForReviewBase(base string) ([]string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, fmt.Errorf("base ref is empty")
	}

	root, err := projectGitCommand("rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, err
	}
	output, err := projectGitCommand("diff", "--name-only", "-z", base+"...HEAD", "--").CombinedOutput()
	if err != nil {
		fallbackOutput, fallbackErr := projectGitCommand("diff", "--name-only", "-z", base+"..HEAD", "--").CombinedOutput()
		if fallbackErr != nil {
			return nil, fmt.Errorf("git diff --name-only %s...HEAD failed: %w\n%s", base, err, strings.TrimSpace(string(output)))
		}
		output = fallbackOutput
	}

	return gitReviewPaths(strings.TrimSpace(string(root)), normalizeChangedFiles(strings.Split(string(output), "\x00"))), nil
}

func normalizeChangedFiles(paths []string) []string {
	seen := map[string]bool{}
	var changed []string
	for _, path := range paths {
		path = normalizeGitPath(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		changed = append(changed, path)
	}
	sort.Strings(changed)
	return changed
}

func normalizeGitPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = parts[len(parts)-1]
	}
	return filepath.ToSlash(path)
}

func reviewAutomationTests(changed []string) []agentIssue {
	var issues []agentIssue
	for _, path := range changed {
		if !isAutomationFile(path) || (managedRepository() && strings.HasPrefix(relativeConfigPath(path), "automations/config/")) {
			continue
		}
		if !fileExists(path) {
			continue
		}
		testPath := expectedAutomationTest(path)
		if testPath == "" || fileExists(testPath) {
			continue
		}
		issues = append(issues, agentIssue{
			ID:       "automation-without-test",
			Severity: "warning",
			File:     path,
			Summary:  "changed automation does not have the conventional matching test file",
			Hint:     fmt.Sprintf("Add %s or document why this automation is covered another way.", testPath),
		})
	}
	return issues
}

func expectedAutomationTest(path string) string {
	relative := relativeConfigPath(path)
	if !strings.HasPrefix(relative, "automations/") || !strings.HasSuffix(relative, ".yaml") {
		return ""
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(relative, "automations/"), ".yaml")
	prefix := strings.TrimSuffix(filepath.ToSlash(path), relative)
	return prefix + "tests/" + rel + "_test.yaml"

}

func reviewBlueprintChanges(changed []string) []agentIssue {
	var issues []agentIssue
	for _, path := range changed {
		if !isAutomationBlueprintFile(path) {
			continue
		}
		issues = append(issues, agentIssue{
			ID:       "blueprint-change-review",
			Severity: "warning",
			File:     path,
			Summary:  "automation blueprint changed; run the broad development suite with trace validation",
			Hint:     "./dm check dev --json",
		})
	}
	return issues
}

func reviewHomeKitChanges(changed []string) []agentIssue {
	var issues []agentIssue
	for _, path := range changed {
		if !strings.HasPrefix(relativeConfigPath(path), "homekit/") || !strings.HasSuffix(path, ".yaml") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "homekit-read-failed",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("could not read changed HomeKit bridge: %v", err),
			})
			continue
		}
		var node yaml.Node
		if err := yaml.Unmarshal(content, &node); err != nil {
			continue
		}
		walkYAMLMaps(&node, func(m *yaml.Node) {
			if includeDomains := mapValue(m, "include_domains"); includeDomains != nil {
				issues = append(issues, agentIssue{
					ID:       "homekit-include-domains",
					Severity: "error",
					File:     path,
					Line:     includeDomains.Line,
					Summary:  "HomeKit bridge uses include_domains instead of explicit include_entities",
					Hint:     "Expose entities explicitly to avoid accidental HomeKit exposure.",
				})
			}
		})
		if bytes.Contains(content, []byte("include_entities:")) {
			issues = append(issues, agentIssue{
				ID:       "homekit-exposure-review",
				Severity: "warning",
				File:     path,
				Summary:  "HomeKit exposure changed and should be reviewed intentionally",
				Hint:     "./dm docs generate --check --json",
			})
		}
		for _, entity := range sensitiveHomeKitEntities(&node) {
			issues = append(issues, agentIssue{
				ID:       "homekit-sensitive-exposure-review",
				Severity: "warning",
				File:     path,
				Summary:  fmt.Sprintf("HomeKit exposes sensitive entity %s", entity),
				Hint:     "Confirm the operator intended to expose locks, covers, doors, gates, cameras, or security-sensitive entities.",
			})
		}
	}
	return issues
}

func sensitiveHomeKitEntities(doc *yaml.Node) []string {
	var sensitive []string
	for _, root := range homeKitBridgeMappings(doc) {
		filter := mapValue(root, "filter")
		includeEntities := mapValue(root, "include_entities")
		if filter != nil && filter.Kind == yaml.MappingNode {
			includeEntities = mapValue(filter, "include_entities")
		}
		for _, entity := range yamlStringSequence(includeEntities) {
			if isSensitiveHomeKitEntity(entity) {
				sensitive = append(sensitive, entity)
			}
		}
	}
	sort.Strings(sensitive)
	return sensitive
}

func isSensitiveHomeKitEntity(entity string) bool {
	switch {
	case strings.HasPrefix(entity, "lock."):
		return true
	case strings.HasPrefix(entity, "cover."):
		return true
	case strings.HasPrefix(entity, "camera."):
		return true
	case strings.Contains(entity, "door"), strings.Contains(entity, "gate"), strings.Contains(entity, "lock"):
		return true
	default:
		return false
	}
}

func reviewProductionRisk(changed []string) []agentIssue {
	riskyPrefixes := map[string]string{
		".gitea/workflows/":   "CI behavior changed",
		"chart/":              "Kubernetes deployment chart changed",
		"configuration.yaml":  "root Home Assistant configuration changed",
		"homekit/":            "HomeKit exposure changed",
		"zigbee2mqtt/":        "Zigbee2MQTT runtime configuration changed",
		"automations/config/": "device configuration automation changed",
	}

	var issues []agentIssue
	for _, path := range changed {
		for prefix, summary := range riskyPrefixes {
			candidate := relativeConfigPath(path)
			if prefix == ".gitea/workflows/" || prefix == "chart/" {
				candidate = projectDisplayPath(path)
			}
			if candidate == prefix || (strings.HasSuffix(prefix, "/") && strings.HasPrefix(candidate, prefix)) {
				issues = append(issues, agentIssue{
					ID:       "production-risk-review",
					Severity: "warning",
					File:     path,
					Summary:  summary,
					Hint:     "Review production blast radius before merge; production-facing operations should remain read-only unless explicitly opted in.",
				})
				break
			}
		}
	}
	return issues
}

func reviewDocsDrift(changed []string) []agentIssue {
	if !commandImplementationChanged(changed) {
		return nil
	}
	if anyDocsChanged(changed) {
		return nil
	}
	return []agentIssue{{
		ID:       "command-docs-drift",
		Severity: "warning",
		Summary:  "dm command or target behavior changed without a docs update",
		Hint:     "Update README.md or docs/ to describe the changed command contract.",
	}}
}

func reviewNextCommands(changed []string, testPlan *testPlanSummary) []string {
	commands := []string{"./dm agent lint --json", "./dm test plan --json"}
	if testPlan != nil {
		commands = append(commands, testPlan.Commands...)
	}
	for _, path := range changed {
		if command := goTestCommandForChange(path); command != "" {
			commands = append(commands, command)
		}
	}
	if anyHAConfigChanged(changed) {
		commands = append(commands, "./dm check --ensure-dev --json")
	} else {
		commands = append(commands, "./dm")
	}
	if anyDocsChanged(changed) || commandImplementationChanged(changed) {
		commands = append(commands, "./dm docs generate --check --json")
	}
	if testPlan != nil {
		commands = append(commands, testPlan.BroadCommands...)
	}
	return uniqueStrings(commands)
}

func isAutomationFile(path string) bool {
	return strings.HasPrefix(relativeConfigPath(path), "automations/") && strings.HasSuffix(path, ".yaml")
}

func anyHAConfigChanged(paths []string) bool {
	for _, path := range paths {
		if relativeConfigPath(path) != "" {
			return true
		}
	}
	return false
}

func anyDocsChanged(paths []string) bool {
	for _, path := range paths {
		path = projectDisplayPath(path)
		if strings.HasPrefix(path, "docs/") || path == "AGENTS.md" || strings.HasSuffix(path, "/AGENTS.md") || path == "README.md" {
			return true
		}
	}
	return false
}
