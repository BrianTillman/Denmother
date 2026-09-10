package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	agentTasksJSON bool
	agentNextJSON  bool
)

var agentTasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Emit a ranked queue of agent-executable cleanup work",
	RunE:  runAgentTasks,
}

var agentNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Choose the next best agent action from repo state",
	RunE:  runAgentNext,
}

type agentTask struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Category           string         `json:"category"`
	Priority           int            `json:"priority"`
	Risk               string         `json:"risk"`
	Mutates            bool           `json:"mutates"`
	RequiresHuman      bool           `json:"requires_human"`
	Files              []string       `json:"files,omitempty"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	ValidationCommands []string       `json:"validation_commands"`
	Source             string         `json:"source"`
	Details            map[string]any `json:"details,omitempty"`
}

type agentTasksSummary struct {
	Tasks        []agentTask `json:"tasks"`
	TaskCount    int         `json:"task_count"`
	NextCommands []string    `json:"next_commands,omitempty"`
}

type agentNextSummary struct {
	Action              string       `json:"action"`
	Command             string       `json:"command"`
	Reason              string       `json:"reason"`
	Task                *agentTask   `json:"task,omitempty"`
	Alternatives        []string     `json:"alternatives,omitempty"`
	ValidationCommands  []string     `json:"validation_commands,omitempty"`
	RequiresHuman       bool         `json:"requires_human"`
	CurrentReviewStatus string       `json:"current_review_status,omitempty"`
	ReviewIssues        []agentIssue `json:"review_issues,omitempty"`
}

func init() {
	agentTasksCmd.Flags().BoolVar(&agentTasksJSON, "json", false, "Emit a machine-readable JSON summary")
	agentNextCmd.Flags().BoolVar(&agentNextJSON, "json", false, "Emit a machine-readable JSON summary")

	agentCmd.AddCommand(agentTasksCmd)
	agentCmd.AddCommand(agentNextCmd)
}

func runAgentTasks(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentTasksJSON, cmd.OutOrStdout())
	rt.SetProfile("tasks")

	summary, err := buildAgentTasksSummary()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "agent-tasks",
			Title:   "Build agent task queue",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "agent task queue failed")
	}

	step := operator.Step{
		ID:           "agent-tasks",
		Title:        "Build agent task queue",
		Status:       operator.StatusSuccess,
		Summary:      fmt.Sprintf("%d agent task(s) available", summary.TaskCount),
		NextCommands: summary.NextCommands,
		Details: map[string]any{
			"tasks":         summary.Tasks,
			"task_count":    summary.TaskCount,
			"next_commands": summary.NextCommands,
		},
	}
	if summary.TaskCount == 0 {
		step.Summary = "no agent cleanup tasks available"
	}
	rt.AddStep(step)

	if !agentTasksJSON {
		if summary.TaskCount == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No agent cleanup tasks available.")
		}
		for _, task := range summary.Tasks {
			fmt.Fprintf(cmd.OutOrStdout(), "%s [P%d] %s\n", task.ID, task.Priority, task.Title)
		}
	}

	return rt.Complete(operator.StatusSuccess, step.Summary)
}

func runAgentNext(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentNextJSON, cmd.OutOrStdout())
	rt.SetProfile("next")

	summary, err := buildAgentNextSummary()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "agent-next",
			Title:   "Choose next agent action",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "agent next action failed")
	}

	step := operator.Step{
		ID:               "agent-next",
		Title:            "Choose next agent action",
		Status:           operator.StatusSuccess,
		Summary:          summary.Reason,
		NextCommands:     uniqueStrings(append([]string{summary.Command}, summary.Alternatives...)),
		RequiresHuman:    summary.RequiresHuman,
		SafeReproCommand: summary.Command,
		Details: map[string]any{
			"next": summary,
		},
	}
	if len(summary.ValidationCommands) > 0 {
		step.Details["validation_commands"] = summary.ValidationCommands
	}
	if summary.Task != nil {
		step.Artifacts = summary.Task.Files
	}
	rt.AddStep(step)

	if !agentNextJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", summary.Command)
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", summary.Reason)
	}

	return rt.Complete(operator.StatusSuccess, summary.Reason)
}

func buildAgentTasksSummary() (*agentTasksSummary, error) {
	report, err := buildAgentQualityReport()
	if err != nil {
		return nil, err
	}

	var tasks []agentTask
	tasks = append(tasks, automationCoverageTasks(report)...)
	tasks = append(tasks, generatedMapTasks(report)...)
	tasks = append(tasks, agentDocTasks(report)...)
	tasks = append(tasks, testDoctorTasks()...)
	tasks = append(tasks, techDebtTasks()...)
	changed, changedErr := changedFilesForReview()
	if changedErr != nil {
		return nil, changedErr
	}
	tasks = append(tasks, reviewIssueTasks(buildReviewSummary(changed))...)

	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority == tasks[j].Priority {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].Priority < tasks[j].Priority
	})

	return &agentTasksSummary{
		Tasks:        tasks,
		TaskCount:    len(tasks),
		NextCommands: []string{"./dm agent next --json", "./dm agent tasks --json", "./dm agent quality --json"},
	}, nil
}

func automationCoverageTasks(report *agentQualityReport) []agentTask {
	metric := qualityMetricByID(report, "automation_tests")
	if metric == nil {
		return nil
	}
	missing, ok := stringSliceFromSignal(metric.Signals["missing_tests"])
	if !ok {
		return nil
	}

	tasks := make([]agentTask, 0, len(missing))
	for _, automation := range missing {
		testPath := expectedAutomationTest(automation)
		if testPath == "" {
			continue
		}
		tasks = append(tasks, agentTask{
			ID:       "automation-test:" + strings.TrimSuffix(strings.TrimPrefix(relativeConfigPath(automation), "automations/"), ".yaml"),
			Title:    "Add conventional automation test for " + automation,
			Category: "automation_tests",
			Priority: 20,
			Risk:     "safe",
			Mutates:  true,
			Files:    []string{automation, testPath},
			AcceptanceCriteria: []string{
				"Create the conventional test file for the automation.",
				"Cover at least one representative trigger and expected outcome.",
				"Keep setup, assertions, and cleanup scoped to local development Home Assistant.",
			},
			ValidationCommands: []string{
				fmt.Sprintf("./dm test %s --json", testPath),
				"./dm check --ensure-dev --json",
				"./dm agent quality --json",
			},
			Source: "agent-quality:automation_tests",
			Details: map[string]any{
				"automation": automation,
				"test":       testPath,
			},
		})
	}
	return tasks
}

func generatedMapTasks(report *agentQualityReport) []agentTask {
	metric := qualityMetricByID(report, "generated_maps")
	if metric == nil || metric.Score == 100 {
		return nil
	}
	return []agentTask{{
		ID:       "generated-maps:refresh",
		Title:    "Refresh stale generated agent maps",
		Category: "generated_maps",
		Priority: 10,
		Risk:     "safe",
		Mutates:  true,
		Files:    []string{projectArtifactPath("docs/generated")},
		AcceptanceCriteria: []string{
			"Run the generator and commit only intentional generated map updates.",
			"`./dm docs generate --check --json` reports success.",
		},
		ValidationCommands: []string{"./dm docs generate", "./dm docs generate --check --json"},
		Source:             "agent-quality:generated_maps",
	}}
}

func agentDocTasks(report *agentQualityReport) []agentTask {
	metric := qualityMetricByID(report, "agent_docs")
	if metric == nil || metric.Score == 100 {
		return nil
	}
	return []agentTask{{
		ID:       "agent-docs:repair",
		Title:    "Repair agent-facing docs and links",
		Category: "agent_docs",
		Priority: 15,
		Risk:     "safe",
		Mutates:  true,
		Files:    []string{projectArtifactPath("AGENTS.md"), projectArtifactPath("docs/agents")},
		AcceptanceCriteria: []string{
			"Required agent docs exist and entrypoint maps remain compact.",
			"Agent markdown links resolve locally.",
		},
		ValidationCommands: []string{"./dm agent lint --json"},
		Source:             "agent-quality:agent_docs",
	}}
}

func testDoctorTasks() []agentTask {
	summary, err := buildTestDoctorSummary()
	if err != nil || len(summary.Issues) == 0 {
		return nil
	}
	byFile := map[string][]agentIssue{}
	for _, issue := range summary.Issues {
		file := issue.File
		if file == "" {
			file = configFile("tests")
		}
		byFile[file] = append(byFile[file], issue)
	}
	var files []string
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)

	tasks := make([]agentTask, 0, len(files))
	for _, file := range files {
		issues := byFile[file]
		priority := 25
		for _, issue := range issues {
			if issue.Severity == "error" {
				priority = 8
				break
			}
		}
		validation := doctorValidationCommandsForFile(file)
		tasks = append(tasks, agentTask{
			ID:       "test-doctor:" + strings.TrimSuffix(strings.TrimPrefix(relativeConfigPath(file), "tests/"), ".yaml"),
			Title:    fmt.Sprintf("Repair %d static test hygiene finding(s) in %s", len(issues), file),
			Category: "test_doctor",
			Priority: priority,
			Risk:     "safe",
			Mutates:  true,
			Files:    []string{file},
			AcceptanceCriteria: []string{
				"Resolve the doctor findings for this file without widening unrelated test behavior.",
				"`./dm test doctor --json` no longer reports these findings.",
			},
			ValidationCommands: validation,
			Source:             "test-doctor",
			Details: map[string]any{
				"issue_ids": doctorIssueIDs(issues),
				"issues":    issues,
			},
		})
	}
	return tasks
}

func doctorIssueIDs(issues []agentIssue) []string {
	seen := map[string]bool{}
	var ids []string
	for _, issue := range issues {
		if issue.ID == "" || seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		ids = append(ids, issue.ID)
	}
	sort.Strings(ids)
	return ids
}

func reviewIssueTasks(summary reviewSummary) []agentTask {
	var tasks []agentTask
	for _, issue := range summary.Issues {
		if issue.Severity == "warning" && issue.ID == "production-risk-review" {
			continue
		}
		priority := 30
		if issue.Severity == "error" {
			priority = 5
		}
		files := []string{}
		if issue.File != "" {
			files = append(files, issue.File)
		}
		task := agentTask{
			ID:            "review:" + issue.ID,
			Title:         issue.Summary,
			Category:      "current_diff",
			Priority:      priority,
			Risk:          "safe",
			Mutates:       true,
			RequiresHuman: false,
			Files:         files,
			AcceptanceCriteria: []string{
				"Resolve the review finding without widening unrelated scope.",
				"`./dm agent review --json` no longer reports this finding.",
			},
			ValidationCommands: []string{"./dm agent review --json", "./dm agent lint --json"},
			Source:             "agent-review:" + issue.ID,
			Details: map[string]any{
				"issue": issue,
			},
		}
		if issue.Hint != "" {
			task.AcceptanceCriteria = append(task.AcceptanceCriteria, issue.Hint)
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func techDebtTasks() []agentTask {
	lines, err := readActiveTechDebtItems(projectArtifactPath("docs/exec-plans/tech-debt.md"))
	if err != nil {
		return nil
	}
	tasks := make([]agentTask, 0, len(lines))
	for idx, item := range lines {
		tasks = append(tasks, agentTask{
			ID:       fmt.Sprintf("tech-debt:%02d", idx+1),
			Title:    item,
			Category: "agent_tech_debt",
			Priority: 40 + idx,
			Risk:     "safe",
			Mutates:  true,
			Files:    []string{projectArtifactPath("docs/exec-plans/tech-debt.md")},
			AcceptanceCriteria: []string{
				"Implement or retire the active tech-debt item.",
				"Move the item to Completed or update it with the remaining narrower follow-up.",
			},
			ValidationCommands: []string{"./dm agent lint --json", "./dm docs generate --check --json"},
			Source:             "docs/exec-plans/tech-debt.md",
		})
	}
	return tasks
}

func readActiveTechDebtItems(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	inActive := false
	var items []string
	var current strings.Builder
	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			items = append(items, text)
		}
		current.Reset()
	}

	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "## Active":
			inActive = true
			continue
		case "## Completed":
			flush()
			inActive = false
			continue
		}
		if !inActive {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			flush()
			current.WriteString(strings.TrimPrefix(trimmed, "- "))
			continue
		}
		if current.Len() > 0 && trimmed != "" {
			current.WriteString(" ")
			current.WriteString(trimmed)
		}
	}
	flush()
	return items, nil
}

func buildAgentNextSummary() (*agentNextSummary, error) {
	changed, err := changedFilesForReview()
	if err != nil {
		return nil, err
	}
	review := buildReviewSummary(changed)
	if len(changed) > 0 {
		if len(review.Issues) > 0 {
			return &agentNextSummary{
				Action:              "resolve-review-finding",
				Command:             "./dm agent review --json",
				Reason:              fmt.Sprintf("current diff has %d review finding(s); resolve those before broader cleanup", len(review.Issues)),
				Alternatives:        review.NextCommands,
				ValidationCommands:  review.NextCommands,
				CurrentReviewStatus: string(agentReviewStep(review).Status),
				ReviewIssues:        review.Issues,
			}, nil
		}
		return &agentNextSummary{
			Action:              "validate-current-diff",
			Command:             firstNonEmpty(review.NextCommands, "./dm"),
			Reason:              fmt.Sprintf("current diff has %d changed file(s) and no review findings; run the recommended validation", len(changed)),
			Alternatives:        review.NextCommands,
			ValidationCommands:  review.NextCommands,
			CurrentReviewStatus: string(agentReviewStep(review).Status),
		}, nil
	}

	tasks, err := buildAgentTasksSummary()
	if err != nil {
		return nil, err
	}
	if len(tasks.Tasks) > 0 {
		task := tasks.Tasks[0]
		return &agentNextSummary{
			Action:             "execute-agent-task",
			Command:            fmt.Sprintf("./dm agent tasks --json # start %s", task.ID),
			Reason:             "no local diff; highest-ranked agent cleanup task is " + task.Title,
			Task:               &task,
			Alternatives:       tasks.NextCommands,
			ValidationCommands: task.ValidationCommands,
			RequiresHuman:      task.RequiresHuman,
		}, nil
	}

	return &agentNextSummary{
		Action:             "baseline-health-check",
		Command:            "./dm agent quality --json",
		Reason:             "no local diff and no queued cleanup tasks; refresh the agent quality baseline",
		Alternatives:       []string{"./dm agent lint --json", "./dm docs generate --check --json"},
		ValidationCommands: []string{"./dm agent quality --json"},
	}, nil
}

func qualityMetricByID(report *agentQualityReport, id string) *agentQualityMetric {
	if report == nil {
		return nil
	}
	for idx := range report.Metrics {
		if report.Metrics[idx].ID == id {
			return &report.Metrics[idx]
		}
	}
	return nil
}

func stringSliceFromSignal(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func firstNonEmpty(values []string, fallback string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return fallback
}
