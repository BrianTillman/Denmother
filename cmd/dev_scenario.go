package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BrianTillman/Denmother/internal/hahttp"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	devScenarioJSON     bool
	devScenarioList     bool
	devScenarioDevURL   string
	devScenarioDevToken string
)

var devScenarioCmd = &cobra.Command{
	Use:   "scenario [name]",
	Short: "Apply a local-dev state scenario",
	Long: `Apply a predefined, local-only state scenario to the development
Home Assistant instance. Scenarios are for visual dashboard iteration and
automation test setup; production targets are never allowed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDevScenario,
}

type devScenarioState struct {
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type devScenario struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	States      map[string]devScenarioState `json:"states"`
}

func init() {
	devScenarioCmd.Flags().BoolVar(&devScenarioJSON, "json", false, "Emit a machine-readable JSON summary")
	devScenarioCmd.Flags().BoolVar(&devScenarioList, "list", false, "List available local-dev scenarios")
	devScenarioCmd.Flags().StringVar(&devScenarioDevURL, "dev-url", "", "Development Home Assistant URL")
	devScenarioCmd.Flags().StringVar(&devScenarioDevToken, "dev-token", "", "Development Home Assistant token")
	devCmd.AddCommand(devScenarioCmd)
}

func runDevScenario(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devScenarioJSON, cmd.OutOrStdout())
	rt.SetProfile("scenario")

	scenarios, err := devScenarios()
	if err != nil {
		rt.AddStep(operator.Step{ID: "load-scenarios", Title: "Read local development scenarios", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "could not load local development scenarios")
	}
	if devScenarioList || len(args) == 0 {
		names := scenarioNames(scenarios)
		rt.AddStep(operator.Step{
			ID:      "list-scenarios",
			Title:   "List local-dev scenarios",
			Status:  operator.StatusSuccess,
			Summary: fmt.Sprintf("%d scenario(s) available", len(names)),
			Details: map[string]any{
				"scenarios": scenarios,
				"names":     names,
			},
		})
		return rt.Complete(operator.StatusSuccess, "listed local-dev scenarios")
	}

	name := strings.TrimSpace(args[0])
	scenario, ok := scenarios[name]
	if !ok {
		rt.AddStep(operator.Step{
			ID:      "select-scenario",
			Title:   "Select local-dev scenario",
			Status:  operator.StatusFailure,
			Summary: fmt.Sprintf("unknown scenario %q", name),
			Details: map[string]any{
				"available": scenarioNames(scenarios),
			},
		})
		return rt.Complete(operator.StatusFailure, "unknown local-dev scenario")
	}

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		DevURL:   devScenarioDevURL,
		DevToken: devScenarioDevToken,
	}
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceDev, flags, operator.ModeMutating, false)
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
		})
		return rt.Complete(operator.StatusFailure, "scenario could not resolve local Home Assistant")
	}

	{
		var failures []string
		applied := 0
		entities := make([]string, 0, len(scenario.States))
		for entityID := range scenario.States {
			entities = append(entities, entityID)
		}
		sort.Strings(entities)
		for _, entityID := range entities {
			if err := cmd.Context().Err(); err != nil {
				failures = append(failures, err.Error())
				break
			}
			if setErr := postHAJSONContext(cmd.Context(), resolved.Config, "/api/states/"+entityID, markDevDisplayState(scenario.States[entityID], "scenario")); setErr != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", entityID, setErr))
			} else {
				applied++
			}
			if err := cmd.Context().Err(); err != nil {
				failures = append(failures, err.Error())
				break
			}
		}
		if len(failures) > 0 {
			rt.AddStep(operator.Step{
				ID:      "apply-scenario",
				Title:   "Apply local-dev scenario",
				Status:  operator.StatusFailure,
				Mutates: true,
				Summary: fmt.Sprintf("scenario incomplete: applied %d of %d state(s)", applied, len(scenario.States)),
				Details: map[string]any{
					"scenario":      scenario,
					"failures":      failures,
					"applied_count": applied,
				},
			})
			return rt.Complete(operator.StatusFailure, "scenario apply failed")
		}
	}

	rt.AddStep(operator.Step{
		ID:      "apply-scenario",
		Title:   "Apply local-dev scenario",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("applied %s to %d state(s)", scenario.Name, len(scenario.States)),
		Mutates: true,
		Details: map[string]any{
			"scenario":       scenario,
			"used_service":   false,
			"evidence_scope": "transient_display_state",
		},
	})
	return rt.Complete(operator.StatusSuccess, fmt.Sprintf("applied local-dev scenario %s", scenario.Name))
}

func postHAJSON(config *haconfig.HAConfig, path string, payload any) error {
	return postHAJSONContext(commandContext(), config, path, payload)
}

func postHAJSONContext(ctx context.Context, config *haconfig.HAConfig, path string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(config.URL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	request.Header.Set("Content-Type", "application/json")
	client := hahttp.NewClient(30 * time.Second)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func scenarioNames(scenarios map[string]devScenario) []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func devScenarios() (map[string]devScenario, error) {
	settings, err := selectedProject()
	if err != nil {
		return nil, err
	}
	return loadDevScenarios(settings.DevScenarios)
}

func loadDevScenarios(path string) (map[string]devScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenarios %s: %w; configure dev_scenarios in .denmother.yaml (see examples/dashboard)", path, err)
	}
	var scenarios map[string]devScenario
	if err := strictDevJSON(data, &scenarios); err != nil {
		return nil, fmt.Errorf("parse scenarios %s: %w", path, err)
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("scenarios %s contains no scenarios", path)
	}
	for key, scenario := range scenarios {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("scenario name must not be empty")
		}
		if scenario.Name == "" {
			scenario.Name = key
		}
		if scenario.Name != key {
			return nil, fmt.Errorf("scenario %q has mismatched name %q", key, scenario.Name)
		}
		if err := validateDevStates(scenario.States); err != nil {
			return nil, fmt.Errorf("scenario %q: %w", key, err)
		}
		scenarios[key] = scenario
	}
	return scenarios, nil
}

func validateDevStates(states map[string]devScenarioState) error {
	if len(states) == 0 {
		return fmt.Errorf("no entity states specified")
	}
	for entityID, state := range states {
		if !entityIDPattern.MatchString(entityID) || entityID != strings.ToLower(entityID) {
			return fmt.Errorf("invalid entity ID %q", entityID)
		}
		if strings.TrimSpace(state.State) == "" || !utf8.ValidString(state.State) || utf8.RuneCountInString(state.State) > 255 {
			return fmt.Errorf("%s state must contain 1–255 characters", entityID)
		}
	}
	return nil
}
