package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var agentQualityJSON bool

var agentQualityCmd = &cobra.Command{
	Use:   "quality",
	Short: "Report the static quality score for agent work",
	RunE:  runAgentQuality,
}

type agentQualityReport struct {
	EvidenceScope   string               `json:"evidence_scope"`
	RuntimeVerified bool                 `json:"runtime_verified"`
	OverallScore    int                  `json:"overall_score"`
	Grade           string               `json:"grade"`
	Metrics         []agentQualityMetric `json:"metrics"`
	TopGaps         []string             `json:"top_gaps,omitempty"`
	NextCommands    []string             `json:"next_commands,omitempty"`
}

type agentQualityMetric struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Score        int            `json:"score"`
	Summary      string         `json:"summary"`
	Signals      map[string]any `json:"signals,omitempty"`
	NextCommands []string       `json:"next_commands,omitempty"`
}

func init() {
	agentQualityCmd.Flags().BoolVar(&agentQualityJSON, "json", false, "Emit a machine-readable JSON summary")
	agentCmd.AddCommand(agentQualityCmd)
}

func runAgentQuality(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentQualityJSON, cmd.OutOrStdout())
	rt.SetProfile("quality")

	report, err := buildAgentQualityReport()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "agent-quality",
			Title:   "Score agent harness quality",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "agent quality scoring failed")
	}

	step := operator.Step{
		ID:      "agent-quality",
		Title:   "Score agent harness quality",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("static hygiene score %d (%s)", report.OverallScore, report.Grade),
		Details: map[string]any{
			"quality":       report,
			"next_commands": report.NextCommands,
		},
	}
	rt.AddStep(step)

	if !agentQualityJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "Static hygiene score: %d (%s)\n", report.OverallScore, report.Grade)
		for _, metric := range report.Metrics {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %d - %s\n", metric.Title, metric.Score, metric.Summary)
		}
	}

	return rt.Complete(operator.StatusSuccess, fmt.Sprintf("agent static hygiene score %d (%s)", report.OverallScore, report.Grade))
}

func buildAgentQualityReport() (*agentQualityReport, error) {
	docs, automations, err := buildBaseGeneratedDocs()
	if err != nil {
		return nil, err
	}
	return buildAgentQualityReportFromInputs(automations, docs), nil
}

func buildAgentQualityReportFromInputs(automations []automationDocRow, generatedDocs []generatedDoc) *agentQualityReport {
	metrics := []agentQualityMetric{
		generatedMapsQualityMetric(generatedDocs),
		automationTestCoverageMetric(automations),
		testConfidenceQualityMetric(),
	}
	if managedRepository() {
		metrics = append(metrics, agentDocsQualityMetric(), agentOrchestrationQualityMetric(),
			productionSafetyQualityMetric(), runtimeLegibilityQualityMetric(), localIsolationQualityMetric(),
			localTestReadinessQualityMetric(), executionPlanningQualityMetric())
	}

	total := 0
	for _, metric := range metrics {
		total += metric.Score
	}
	overall := 0
	if len(metrics) > 0 {
		overall = total / len(metrics)
	}

	report := &agentQualityReport{
		EvidenceScope: "static_repository_hygiene",
		OverallScore:  overall,
		Grade:         qualityGrade(overall),
		Metrics:       metrics,
		NextCommands: []string{
			"./dm agent quality --json",
			"./dm agent review --json",
			"./dm agent lint --json",
			"./dm docs generate --check --json",
		},
	}

	report.TopGaps = topQualityGaps(metrics, 4)
	return report
}

func agentDocsQualityMetric() agentQualityMetric {
	if !managedRepository() {
		return agentQualityMetric{ID: "agent_docs", Title: "Agent Docs", Score: 100, Summary: "No mandatory repository documentation policy configured"}
	}
	var issues []agentIssue
	issues = append(issues, checkRequiredAgentDocs()...)
	issues = append(issues, checkAgentMapSizes()...)
	issues = append(issues, checkMarkdownLinks()...)

	score := boundedScore(100 - 15*len(issues))
	return agentQualityMetric{
		ID:      "agent_docs",
		Title:   "Agent Docs",
		Score:   score,
		Summary: fmt.Sprintf("%d doc issue(s) across %d required artifacts", len(issues), len(requiredAgentDocs)),
		Signals: map[string]any{
			"required_artifacts": len(requiredAgentDocs),
			"issues":             len(issues),
		},
		NextCommands: []string{"./dm agent lint --json"},
	}
}

func generatedMapsQualityMetric(docs []generatedDoc) agentQualityMetric {
	stale := 0
	for _, doc := range docs {
		if doc.Changed {
			stale++
		}
	}
	paths := generatedDocPaths(docs)
	paths = append(paths, projectArtifactPath("docs/generated/quality-score.md"))
	sort.Strings(paths)
	score := boundedScore(100 - 20*stale)
	return agentQualityMetric{
		ID:      "generated_maps",
		Title:   "Generated Maps",
		Score:   score,
		Summary: fmt.Sprintf("%d stale map(s) across %d generated map(s)", stale, len(docs)+1),
		Signals: map[string]any{
			"stale_maps": stale,
			"maps":       paths,
		},
		NextCommands: []string{"./dm docs generate --check --json"},
	}
}

func automationTestCoverageMetric(rows []automationDocRow) agentQualityMetric {
	byFile := map[string]bool{}
	for _, row := range rows {
		if row.Category == "config" && managedRepository() {
			continue
		}
		if _, ok := byFile[row.Path]; !ok {
			byFile[row.Path] = false
		}
		if row.Test != "" {
			byFile[row.Path] = true
		}
	}

	total := len(byFile)
	tested := 0
	var missing []string
	for path, hasTest := range byFile {
		if hasTest {
			tested++
		} else {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)

	score := 100
	if total > 0 {
		score = tested * 100 / total
	}
	return agentQualityMetric{
		ID:      "automation_tests",
		Title:   "Automation Tests",
		Score:   score,
		Summary: fmt.Sprintf("%d/%d non-config automation file(s) have conventional tests", tested, total),
		Signals: map[string]any{
			"tested_files":  tested,
			"total_files":   total,
			"missing_tests": missing,
		},
		NextCommands: []string{"./dm test --changed --json", "./dm test --tags fast --json"},
	}
}

func testConfidenceQualityMetric() agentQualityMetric {
	summary, err := buildTestDoctorSummary()
	if err != nil {
		return agentQualityMetric{
			ID:      "test_confidence",
			Title:   "Test Confidence",
			Score:   0,
			Summary: fmt.Sprintf("test doctor could not run: %v", err),
			Signals: map[string]any{
				"error": err.Error(),
			},
			NextCommands: []string{"./dm test doctor --compact --json", "./dm test doctor --json"},
		}
	}

	stats := summary.Stats
	fastScore := percent(stats.FastTaggedFiles, stats.TestFiles)
	traceScore := 100
	if stats.NaturalTriggerCases > 0 {
		traceScore = percent(stats.NaturalTriggerCasesWithTrace, stats.NaturalTriggerCases)
	}
	automationTriggerScore := 100
	if stats.TestFiles > 0 {
		automationTriggerScore = 100 - percent(stats.AutomationTriggerFiles, stats.TestFiles)
	}
	cleanupScore := 100
	if stats.TestFiles > 0 {
		cleanupScore = 100 - percent(stats.ExplicitCleanupGapFiles, stats.TestFiles)
	}
	loadScore := 100
	if stats.LoadFailures > 0 {
		loadScore = 0
	}
	score := boundedScore((fastScore * 25 / 100) + (traceScore * 35 / 100) + (automationTriggerScore * 20 / 100) + (cleanupScore * 10 / 100) + (loadScore * 10 / 100))

	return agentQualityMetric{
		ID:    "test_confidence",
		Title: "Test Confidence",
		Score: score,
		Summary: fmt.Sprintf("%d/%d fast-tagged files, %d/%d natural-trigger cases with trace assertions, %d automation-trigger file(s)",
			stats.FastTaggedFiles,
			stats.TestFiles,
			stats.NaturalTriggerCasesWithTrace,
			stats.NaturalTriggerCases,
			stats.AutomationTriggerFiles,
		),
		Signals: map[string]any{
			"stats":       stats,
			"issue_count": summary.IssueCount,
		},
		NextCommands: []string{"./dm test doctor --compact --json", "./dm test plan --json", "./dm test --changed --json"},
	}
}

func agentOrchestrationQualityMetric() agentQualityMetric {
	required := []string{
		"cmd/agent_tasks.go",
		"docs/generated/dm-json-schema.json",
	}
	missing := missingFiles(required)
	score := boundedScore(100 - 40*len(missing))
	return agentQualityMetric{
		ID:      "agent_orchestration",
		Title:   "Agent Orchestration",
		Score:   score,
		Summary: fmt.Sprintf("%d/%d task-selection and JSON-contract artifact(s) present", len(required)-len(missing), len(required)),
		Signals: map[string]any{
			"missing": missing,
		},
		NextCommands: []string{"./dm agent next --json", "./dm agent tasks --json"},
	}
}

func productionSafetyQualityMetric() agentQualityMetric {
	secretIssues := checkTrackedSecrets()
	homekitIssues := checkHomeKitHarnessDefects()
	score := boundedScore(100 - 30*len(secretIssues) - 25*len(homekitIssues))
	return agentQualityMetric{
		ID:      "production_safety",
		Title:   "Production Safety",
		Score:   score,
		Summary: fmt.Sprintf("%d secret issue(s), %d HomeKit exposure issue(s)", len(secretIssues), len(homekitIssues)),
		Signals: map[string]any{
			"secret_issues":  len(secretIssues),
			"homekit_issues": len(homekitIssues),
		},
		NextCommands: []string{"./dm agent lint --json"},
	}
}

func runtimeLegibilityQualityMetric() agentQualityMetric {
	required := []string{
		"cmd/observe.go",
		"cmd/trace.go",
		"cmd/logs.go",
		"cmd/audit.go",
		"cmd/verify.go",
	}
	missing := missingFiles(required)
	score := boundedScore(100 - 25*len(missing))
	return agentQualityMetric{
		ID:      "runtime_legibility",
		Title:   "Runtime Legibility",
		Score:   score,
		Summary: fmt.Sprintf("%d/%d read-only runtime surfaces present", len(required)-len(missing), len(required)),
		Signals: map[string]any{
			"missing": missing,
		},
		NextCommands: []string{"./dm observe <automation_entity> --json", "./dm trace <automation_entity> --json", "./dm logs --json"},
	}
}

func localIsolationQualityMetric() agentQualityMetric {
	required := []string{
		"cmd/dev.go",
		".devcontainer/docker-compose.yml",
		".devcontainer/storage-template/auth",
	}
	missing := missingFiles(required)
	score := boundedScore(100 - 35*len(missing))
	return agentQualityMetric{
		ID:      "local_isolation",
		Title:   "Local Isolation",
		Score:   score,
		Summary: fmt.Sprintf("%d/%d local dev isolation artifact(s) present", len(required)-len(missing), len(required)),
		Signals: map[string]any{
			"missing": missing,
		},
		NextCommands: []string{"./dm dev up --json", "./dm check --ensure-dev --json"},
	}
}

func localTestReadinessQualityMetric() agentQualityMetric {
	required := []string{
		"cmd/dev.go",
		"cmd/test.go",
		".devcontainer/docker-compose.yml",
		".devcontainer/dev-state-fixtures.json",
		".devcontainer/mock_entities/__init__.py",
	}
	missing := missingFiles(required)
	score := boundedScore(100 - 20*len(missing))

	signals := map[string]any{
		"missing":       missing,
		"runtime_check": "./dm dev status --json",
	}

	return agentQualityMetric{
		ID:           "local_test_readiness",
		Title:        "Local Test Readiness",
		Score:        score,
		Summary:      fmt.Sprintf("%d/%d local test readiness artifact(s) present; runtime reachability is checked by dm dev status", len(required)-len(missing), len(required)),
		Signals:      signals,
		NextCommands: []string{"./dm dev status --json", "./dm dev up --json", "./dm check --ensure-dev --json"},
	}
}

func executionPlanningQualityMetric() agentQualityMetric {
	required := []string{
		"cmd/plan.go",
		"docs/exec-plans/active/.gitkeep",
		"docs/exec-plans/completed/.gitkeep",
		"docs/exec-plans/tech-debt.md",
		"docs/agents/escalation.md",
	}
	missing := missingFiles(required)
	score := boundedScore(100 - 25*len(missing))
	return agentQualityMetric{
		ID:      "execution_planning",
		Title:   "Execution Planning",
		Score:   score,
		Summary: fmt.Sprintf("%d/%d planning and escalation artifacts present", len(required)-len(missing), len(required)),
		Signals: map[string]any{
			"missing": missing,
		},
		NextCommands: []string{"./dm plan status --json"},
	}
}

func generateQualityScoreDoc(report *agentQualityReport) string {
	var b strings.Builder
	writeGeneratedHeader(&b, "Quality Score")
	fmt.Fprintf(&b, "Static repository hygiene: **%d** (%s). Runtime behavior has not been verified by this score.\n\n", report.OverallScore, report.Grade)
	b.WriteString("| Metric | Score | Summary | Next Command |\n")
	b.WriteString("| --- | ---: | --- | --- |\n")
	for _, metric := range report.Metrics {
		next := "-"
		if len(metric.NextCommands) > 0 {
			next = "`" + escapeTable(metric.NextCommands[0]) + "`"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s |\n",
			escapeTable(metric.Title),
			metric.Score,
			escapeTable(metric.Summary),
			next)
	}
	if len(report.TopGaps) > 0 {
		b.WriteString("\n## Top Cleanup Targets\n\n")
		for _, gap := range report.TopGaps {
			b.WriteString("- " + gap + "\n")
		}
	}
	return b.String()
}

func missingFiles(paths []string) []string {
	var missing []string
	for _, path := range paths {
		if _, err := os.Stat(projectArtifactPath(path)); err != nil {
			missing = append(missing, path)
		}
	}
	return missing
}

func percent(numerator, denominator int) int {
	if denominator <= 0 {
		return 100
	}
	return numerator * 100 / denominator
}

func boundedScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func qualityGrade(score int) string {
	switch {
	case score >= 95:
		return "A"
	case score >= 85:
		return "B"
	case score >= 75:
		return "C"
	case score >= 65:
		return "D"
	default:
		return "F"
	}
}

func topQualityGaps(metrics []agentQualityMetric, limit int) []string {
	sorted := append([]agentQualityMetric(nil), metrics...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Score == sorted[j].Score {
			return sorted[i].Title < sorted[j].Title
		}
		return sorted[i].Score < sorted[j].Score
	})
	var gaps []string
	for _, metric := range sorted {
		if metric.Score >= 100 {
			continue
		}
		gaps = append(gaps, fmt.Sprintf("%s: %s", metric.Title, metric.Summary))
		if len(gaps) >= limit {
			break
		}
	}
	return gaps
}
