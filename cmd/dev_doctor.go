package cmd

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	devDoctorJSON bool
	devDoctorTail int
)

var devDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Inspect local dev Home Assistant runtime health",
	Long: `Inspect the local development Home Assistant runtime for issues that
can make dashboards or tests look healthy while real YAML failed to load.`,
	RunE: runDevDoctor,
}

type devLogFinding struct {
	RuleID  string   `json:"rule_id"`
	Count   int      `json:"count"`
	Samples []string `json:"samples,omitempty"`
}

func init() {
	devDoctorCmd.Flags().BoolVar(&devDoctorJSON, "json", false, "Emit a machine-readable JSON summary")
	devDoctorCmd.Flags().IntVar(&devDoctorTail, "tail", 800, "Number of recent Home Assistant log lines to inspect")
	devCmd.AddCommand(devDoctorCmd)
}

func runDevDoctor(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devDoctorJSON, cmd.OutOrStdout())
	rt.SetProfile("doctor")

	overall := operator.StatusSuccess
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-dev",
			Title:   "Resolve local Home Assistant",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
			Hints:   []string{"Run `./dm dev up --json` and wait for `./dm dev status --json` to report a reachable instance."},
		})
		return rt.Complete(operator.StatusFailure, "local dev doctor could not connect")
	}

	dashboardRefs, dashboardErr := allDashboardEntityRefs()
	if dashboardErr != nil {
		overall = operator.MergeStatus(overall, operator.StatusFailure)
		rt.AddStep(operator.Step{
			ID:      "dashboard-refs",
			Title:   "Parse dashboard entity references",
			Status:  operator.StatusFailure,
			Summary: dashboardErr.Error(),
		})
	} else {
		rt.AddStep(operator.Step{
			ID:      "dashboard-refs",
			Title:   "Parse dashboard entity references",
			Status:  operator.StatusSuccess,
			Summary: fmt.Sprintf("found %d unique dashboard entity reference(s)", len(dashboardRefs)),
			Details: map[string]any{
				"entity_refs": dashboardRefs,
			},
		})
	}

	client := hasync.NewClient(resolved.Config.URL, resolved.Config.Token).WithContext(cmd.Context())
	states, fetchErr := client.FetchEntities()
	if fetchErr != nil {
		overall = operator.MergeStatus(overall, operator.StatusFailure)
		rt.AddStep(operator.Step{
			ID:      "fetch-state",
			Title:   "Fetch local Home Assistant state",
			Status:  operator.StatusFailure,
			Summary: fetchErr.Error(),
		})
	} else {
		stateStep := localStateDoctorStep(states, dashboardRefs)
		rt.AddStep(stateStep)
		overall = operator.MergeStatus(overall, stateStep.Status)
	}

	cards, cardsErr := allDashboardCustomCards()
	resourceURLs, resourceStatus, resourceSummary := checkDevLovelaceResources(cmd.Context(), configFile(""), cards)
	if cardsErr != nil {
		resourceStatus, resourceSummary = operator.StatusFailure, cardsErr.Error()
	}
	overall = operator.MergeStatus(overall, resourceStatus)
	rt.AddStep(operator.Step{
		ID:      "lovelace-resource",
		Title:   "Check dashboard resources",
		Status:  resourceStatus,
		Summary: resourceSummary,
		Details: map[string]any{
			"resource_urls": resourceURLs,
		},
	})

	logStep := devLogDoctorStep(devDoctorTail)
	overall = operator.MergeStatus(overall, logStep.Status)
	rt.AddStep(logStep)

	summary := "local dev runtime looks healthy"
	if overall != operator.StatusSuccess {
		summary = "local dev runtime has health issues"
	}
	return rt.Complete(overall, summary)
}

func allDashboardEntityRefs() ([]string, error) {
	targets, err := listDashboardTargets(configFile(""))
	if err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	for _, target := range targets {
		entityRefs, _, err := inspectDashboardYAML(target.File)
		if err != nil {
			return nil, err
		}
		for _, entityID := range entityRefs {
			refs[entityID] = true
		}
	}
	return sortedKeys(refs), nil
}

func allDashboardCustomCards() ([]string, error) {
	targets, err := listDashboardTargets(configFile(""))
	if err != nil {
		return nil, err
	}
	cards := map[string]bool{}
	for _, target := range targets {
		_, custom, err := inspectDashboardYAML(target.File)
		if err != nil {
			return nil, err
		}
		for _, card := range custom {
			cards[card] = true
		}
	}
	return sortedKeys(cards), nil
}

func localStateDoctorStep(states []hasync.EntityState, dashboardRefs []string) operator.Step {
	byEntity := make(map[string]hasync.EntityState, len(states))
	for _, state := range states {
		byEntity[state.EntityID] = state
	}
	var missing []string
	var synthetic []string
	var unhealthy []dashboardStateIssue
	for _, entityID := range dashboardRefs {
		state, ok := byEntity[entityID]
		if !ok {
			missing = append(missing, entityID)
			continue
		}
		if source, _ := state.Attributes["denmother_source"].(string); source == "fixture" || source == "scenario" {
			synthetic = append(synthetic, entityID)
		}
		if reason := unhealthyDashboardStateReason(entityID, state.State); reason != "" {
			unhealthy = append(unhealthy, dashboardStateIssue{
				EntityID: entityID,
				State:    state.State,
				Reason:   reason,
			})
		}
	}
	sort.Strings(missing)
	sort.Strings(synthetic)
	sort.Slice(unhealthy, func(i, j int) bool {
		return unhealthy[i].EntityID < unhealthy[j].EntityID
	})

	status := operator.StatusSuccess
	summary := fmt.Sprintf("local HA has %d state(s); dashboard refs have healthy display state", len(states))
	if len(synthetic) > 0 {
		status = operator.StatusWarning
		summary = fmt.Sprintf("dashboard uses %d synthetic display state(s); integration behavior is unverified", len(synthetic))
	}
	if len(missing) > 0 || len(unhealthy) > 0 {
		status = operator.StatusFailure
		summary = fmt.Sprintf("dashboard refs have %d missing and %d unhealthy local state(s)", len(missing), len(unhealthy))
	}
	return operator.Step{
		ID:      "dashboard-state-health",
		Title:   "Check dashboard state health",
		Status:  status,
		Summary: summary,
		Details: map[string]any{
			"state_count":          len(states),
			"missing_entities":     missing,
			"unhealthy_entities":   unhealthy,
			"dashboard_ref_count":  len(dashboardRefs),
			"synthetic_entity_ids": synthetic,
			"evidence_scope":       "dashboard_display_state",
		},
	}
}

func devLogDoctorStep(tail int) operator.Step {
	if tail <= 0 {
		tail = 800
	}
	env, err := currentDevEnvironment()
	if err != nil {
		return operator.Step{
			ID:      "runtime-logs",
			Title:   "Inspect Home Assistant logs",
			Status:  operator.StatusWarning,
			Summary: err.Error(),
		}
	}
	output, err := exec.CommandContext(commandContext(), "docker", "logs", "--tail", fmt.Sprintf("%d", tail), env.ProjectName+"-homeassistant").CombinedOutput()
	if err != nil {
		return operator.Step{
			ID:      "runtime-logs",
			Title:   "Inspect Home Assistant logs",
			Status:  operator.StatusWarning,
			Summary: fmt.Sprintf("could not inspect Home Assistant logs: %v", err),
			Details: map[string]any{
				"output": strings.TrimSpace(string(output)),
			},
		}
	}
	findings := collectDevLogFindings(string(output))
	status := operator.StatusSuccess
	summary := "recent Home Assistant logs contain no known local-dev blockers"
	if len(findings) > 0 {
		status = operator.StatusFailure
		summary = fmt.Sprintf("recent Home Assistant logs contain %d known blocker type(s)", len(findings))
	}
	return operator.Step{
		ID:      "runtime-logs",
		Title:   "Inspect Home Assistant logs",
		Status:  status,
		Summary: summary,
		Details: map[string]any{
			"tail":     tail,
			"findings": findings,
		},
	}
}

func collectDevLogFindings(logs string) []devLogFinding {
	patterns := []struct {
		id string
		re *regexp.Regexp
	}{
		{"duplicate-automation-id", regexp.MustCompile(`(?i)Platform automation does not generate unique IDs`)},
		{"duplicate-entity-id", regexp.MustCompile(`(?i)Entity id already exists - ignoring:`)},
		{"service-target-unavailable", regexp.MustCompile(`(?i)Mock entity service target unavailable:`)},
		{"setup-failed", regexp.MustCompile(`(?i)(setup failed|failed to set up|error setting up entry|unable to prepare setup)`)},
		{"traceback", regexp.MustCompile(`(?i)traceback \(most recent call last\)`)},
		{"custom-component-error", regexp.MustCompile(`(?i)(custom_components|mock_entities).*(error|exception|failed)`)},
	}
	counts := map[string]int{}
	samples := map[string][]string{}
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(stripANSI(line))
		if line == "" {
			continue
		}
		for _, pattern := range patterns {
			if !pattern.re.MatchString(line) {
				continue
			}
			counts[pattern.id]++
			if len(samples[pattern.id]) < 8 {
				samples[pattern.id] = append(samples[pattern.id], line)
			}
		}
	}
	var findings []devLogFinding
	for id, count := range counts {
		findings = append(findings, devLogFinding{
			RuleID:  id,
			Count:   count,
			Samples: samples[id],
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].RuleID < findings[j].RuleID
	})
	return findings
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(line string) string {
	return ansiEscapeRE.ReplaceAllString(line, "")
}
