package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hahttp"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	devDashboardJSON           bool
	devDashboardStorage        bool
	devDashboardList           bool
	devDashboardEnsureDev      bool
	devDashboardRequireRunning bool
	devDashboardRender         bool
	devDashboardRenderTimeout  time.Duration
	devDashboardViews          []string
	devFixturesJSON            bool
	devFixturesOutput          string
	devFixturesReplace         bool
	devFixturesAllDashboards   bool
	devFixturesProdURL         string
	devFixturesProdToken       string
	devFixturesDevURL          string
	devFixturesDevToken        string
	devFixturesHAURL           string
	devFixturesHAToken         string
)

const dashboardRenderPlaywrightVersion = "1.61.1"

var devDashboardCmd = &cobra.Command{
	Use:   "dashboard [dashboard-slug-or-yaml-file]",
	Short: "Smoke-check a local Lovelace dashboard for dev rendering",
	Long: `Smoke-check a Lovelace dashboard for local iterative development.

The command reads local dashboard YAML, or a saved dashboard from the selected
development HA with --storage. It verifies entity references against the project
inventory, checks registered custom-card resources, and checks local display
states. Use --list to discover dashboards and --render to check every view.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if devDashboardList {
			return cobra.NoArgs(cmd, args)
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	RunE: runDevDashboard,
}

var devFixturesCmd = &cobra.Command{
	Use:   "fixtures [dashboard-slug-or-yaml-file]",
	Short: "Refresh filtered local-dev dashboard state fixtures",
	Long: `Refresh filtered local-dev dashboard state fixtures from a live Home Assistant target.

The command reads only the entities referenced by the selected dashboard and
writes a filtered fixture file for local mock rendering. Attribute filtering
removes known sensitive fields, but names and live state values remain private.
Review the output before sharing it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDevFixtures,
}

type dashboardSmokeSummary struct {
	Slug               string                `json:"slug"`
	File               string                `json:"file,omitempty"`
	Mode               string                `json:"mode"`
	EntityRefs         []string              `json:"entity_refs"`
	CustomCards        []string              `json:"custom_cards"`
	MissingReference   []string              `json:"missing_reference,omitempty"`
	MissingLiveState   []string              `json:"missing_live_state,omitempty"`
	UnhealthyLiveState []dashboardStateIssue `json:"unhealthy_live_state,omitempty"`
	ResourceURLs       []string              `json:"resource_urls,omitempty"`
	Render             *dashboardRenderCheck `json:"render,omitempty"`
}

type dashboardTarget struct {
	Slug string `json:"slug"`
	File string `json:"file"`
}

type dashboardStateIssue struct {
	EntityID string `json:"entity_id"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
}

type dashboardRenderCheck struct {
	URL             string                 `json:"url"`
	Screenshot      string                 `json:"screenshot,omitempty"`
	ConsoleErrors   []string               `json:"console_errors,omitempty"`
	RequestFailures []string               `json:"request_failures,omitempty"`
	PageErrors      []string               `json:"page_errors,omitempty"`
	VisibleErrors   []string               `json:"visible_errors,omitempty"`
	CardCount       int                    `json:"card_count"`
	Views           []dashboardRenderCheck `json:"views,omitempty"`
}

type devFixturePayload struct {
	SchemaVersion string                    `json:"schema_version"`
	Description   string                    `json:"description"`
	Source        map[string]any            `json:"source,omitempty"`
	Entities      map[string]map[string]any `json:"entities"`
}

func init() {
	devDashboardCmd.Flags().BoolVar(&devDashboardStorage, "storage", false, "Read a saved dashboard from the selected development HA instead of local YAML")
	devDashboardCmd.Flags().BoolVar(&devDashboardList, "list", false, "List available dashboards (use --storage for dashboards saved in development HA)")
	devDashboardCmd.Flags().BoolVar(&devDashboardJSON, "json", false, "Emit a machine-readable JSON summary")
	devDashboardCmd.Flags().BoolVar(&devDashboardEnsureDev, "ensure-dev", false, "Start the isolated local dev environment if needed")
	devDashboardCmd.Flags().BoolVar(&devDashboardRequireRunning, "require-running", false, "Fail if local Home Assistant is not reachable")
	devDashboardCmd.Flags().BoolVar(&devDashboardRender, "render", false, "Open the dashboard in a browser with Playwright and fail on visible rendering errors")
	devDashboardCmd.Flags().DurationVar(&devDashboardRenderTimeout, "render-timeout", 90*time.Second, "Maximum time for the optional browser render check")
	devDashboardCmd.Flags().StringSliceVar(&devDashboardViews, "view", nil, "Render only these view paths or zero-based indexes (default: all views)")
	devFixturesCmd.Flags().BoolVar(&devFixturesJSON, "json", false, "Emit a machine-readable JSON summary")
	devFixturesCmd.Flags().StringVar(&devFixturesOutput, "output", ".devcontainer/dev-state-fixtures.json", "Fixture output path (default: selected project dev_fixtures)")
	devFixturesCmd.Flags().BoolVar(&devFixturesReplace, "replace", false, "Replace the fixture file instead of merging refreshed entities into it")
	devFixturesCmd.Flags().BoolVar(&devFixturesAllDashboards, "all-dashboards", false, "Refresh fixtures for every registered YAML dashboard")
	devFixturesCmd.Flags().StringVar(&devFixturesProdURL, "prod-url", "", "Production Home Assistant URL")
	devFixturesCmd.Flags().StringVar(&devFixturesProdToken, "prod-token", "", "Production Home Assistant token")
	devFixturesCmd.Flags().StringVar(&devFixturesDevURL, "dev-url", "", "Development Home Assistant URL")
	devFixturesCmd.Flags().StringVar(&devFixturesDevToken, "dev-token", "", "Development Home Assistant token")
	devFixturesCmd.Flags().StringVar(&devFixturesHAURL, "ha-url", "", "Home Assistant URL (legacy, use --prod-url)")
	devFixturesCmd.Flags().StringVar(&devFixturesHAToken, "ha-token", "", "Home Assistant token (legacy, use --prod-token)")
	devCmd.AddCommand(devDashboardCmd)
	devCmd.AddCommand(devFixturesCmd)
}

func runDevDashboard(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devDashboardJSON, cmd.OutOrStdout())
	rt.SetProfile("dashboard")

	target := ""
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		target = strings.TrimSpace(args[0])
	}

	if devDashboardEnsureDev {
		env, err := prepareDevEnvironment()
		if err != nil {
			rt.AddStep(operator.Step{
				ID:      "prepare-dev",
				Title:   "Prepare isolated dev environment",
				Status:  operator.StatusFailure,
				Summary: err.Error(),
			})
			return rt.Complete(operator.StatusFailure, "dashboard smoke check could not prepare dev environment")
		}
		if err := runDockerCompose(env, "up", "-d", "--force-recreate", "homeassistant", "ui-proxy"); err != nil {
			rt.AddStep(operator.Step{ID: "start-dev", Title: "Start isolated dev environment", Status: operator.StatusFailure, Summary: err.Error(), Mutates: true})
			return rt.Complete(operator.StatusFailure, "dashboard smoke check could not start dev environment")
		}
		if err := bootstrapPortableDev(cmd.Context(), env); err != nil {
			rt.AddStep(operator.Step{ID: "authenticate-dev", Title: "Authenticate isolated dev environment", Status: operator.StatusFailure, Summary: err.Error(), Mutates: true})
			return rt.Complete(operator.StatusFailure, "dashboard smoke check could not authenticate local HA")
		}

		rt.AddStep(operator.Step{
			ID:      "ensure-dev",
			Title:   "Ensure isolated dev environment",
			Status:  operator.StatusSuccess,
			Summary: fmt.Sprintf("dev environment available at %s", env.HAURL),
			Mutates: true,
			Details: map[string]any{
				"environment": env,
			},
		})
	}

	if devDashboardList {
		var listed any
		var err error
		if devDashboardStorage {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			var ws *hasync.WSClient
			ws, err = connectDevDashboard(ctx)
			if err == nil {
				defer ws.Close()
				listed, err = listLiveStorageDashboards(ctx, ws)
			}
		} else {
			listed, err = listDashboardTargets(configFile(""))
		}
		if err != nil {
			rt.AddStep(operator.Step{ID: "list-dashboards", Title: "List dashboards", Status: operator.StatusFailure, Summary: err.Error()})
			return rt.Complete(operator.StatusFailure, "dashboard listing failed")
		}
		rt.AddStep(operator.Step{ID: "list-dashboards", Title: "List dashboards", Status: operator.StatusSuccess, Summary: "available dashboards", Details: map[string]any{"dashboards": listed}})
		if !devDashboardJSON {
			data, _ := json.MarshalIndent(listed, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
		}
		return rt.Complete(operator.StatusSuccess, "dashboard listing complete")
	}
	source, err := resolveDashboardSource(cmd.Context(), configFile(""), target, devDashboardStorage)
	if err != nil {
		rt.AddStep(operator.Step{ID: "resolve-dashboard", Title: "Resolve Lovelace dashboard", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "dashboard smoke check could not resolve dashboard")
	}
	slug, dashboardFile := source.Slug, source.File
	entityRefs, customCards, err := inspectDashboardSource(source)
	if err != nil {
		rt.AddStep(operator.Step{ID: "parse-dashboard", Title: "Inspect Lovelace dashboard", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "dashboard inspection failed")
	}

	summary := dashboardSmokeSummary{
		Slug:        slug,
		File:        dashboardFile,
		Mode:        source.Mode,
		EntityRefs:  entityRefs,
		CustomCards: customCards,
	}

	rt.AddStep(operator.Step{
		ID:      "parse-dashboard",
		Title:   "Inspect Lovelace dashboard configuration",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("found %d entity reference(s) and %d custom card type(s)", len(entityRefs), len(customCards)),
		Details: map[string]any{
			"dashboard": summary,
		},
	})

	overall := operator.StatusSuccess
	settings, err := selectedProject()
	if err != nil {
		rt.AddStep(operator.Step{ID: "resolve-project", Title: "Resolve dashboard project", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "dashboard project resolution failed")
	}
	referenceEntities, err := loadReferenceEntities(filepath.Join(settings.ReferencesDir, "entity-list.txt"))
	if err != nil {
		overall = operator.MergeStatus(overall, operator.StatusPartial)
		rt.AddStep(operator.Step{
			ID:      "reference-entities",
			Title:   "Check entity reference list",
			Status:  operator.StatusPartial,
			Summary: err.Error(),
		})
	} else {
		missingReference := missingEntities(entityRefs, referenceEntities)
		summary.MissingReference = missingReference
		status := operator.StatusSuccess
		stepSummary := "all dashboard entities exist in the selected project reference list"
		if len(missingReference) > 0 {
			status = operator.StatusFailure
			stepSummary = fmt.Sprintf("%d dashboard entity reference(s) missing from reference list", len(missingReference))
		}
		overall = operator.MergeStatus(overall, status)
		rt.AddStep(operator.Step{
			ID:      "reference-entities",
			Title:   "Check entity reference list",
			Status:  status,
			Summary: stepSummary,
			Details: map[string]any{
				"missing_entities": missingReference,
			},
		})
	}

	resourceConfig := configFile("")
	if source.Mode == "storage" {
		resourceConfig = ""
	}
	resourceURLs, resourceStatus, resourceSummary := checkDevLovelaceResources(cmd.Context(), resourceConfig, customCards)
	summary.ResourceURLs = resourceURLs
	overall = operator.MergeStatus(overall, resourceStatus)
	rt.AddStep(operator.Step{
		ID:      "lovelace-resources",
		Title:   "Check local Lovelace resources",
		Status:  resourceStatus,
		Summary: resourceSummary,
		Details: map[string]any{
			"resource_urls": resourceURLs,
		},
	})

	liveStatus, liveSummary, liveMissing, liveIssues := checkLiveDashboardEntities(cmd.Context(), entityRefs)
	summary.MissingLiveState = liveMissing
	summary.UnhealthyLiveState = liveIssues
	overall = operator.MergeStatus(overall, liveStatus)
	rt.AddStep(operator.Step{
		ID:      "live-dashboard-state",
		Title:   "Check local Home Assistant dashboard state",
		Status:  liveStatus,
		Summary: liveSummary,
		Details: map[string]any{
			"missing_entities":   liveMissing,
			"unhealthy_entities": liveIssues,
			"evidence_scope":     "dashboard_display_state",
		},
		Hints: liveHints(liveStatus),
	})

	if devDashboardRender {
		renderStatus, renderSummary, renderCheck := runDashboardRenderCheck(cmd.Context(), source)
		summary.Render = renderCheck
		overall = operator.MergeStatus(overall, renderStatus)
		step := operator.Step{
			ID:      "render-dashboard",
			Title:   "Render dashboard in browser",
			Status:  renderStatus,
			Summary: renderSummary,
			Hints: []string{
				"The browser preflight reports the selected runner directory and setup command when Chromium or Linux system libraries are missing.",
			},
		}
		if renderCheck != nil {
			step.Details = map[string]any{
				"render": renderCheck,
			}
			if renderCheck.Screenshot != "" {
				step.Artifacts = []string{renderCheck.Screenshot}
				for _, view := range renderCheck.Views {
					if view.Screenshot != "" && view.Screenshot != renderCheck.Screenshot {
						step.Artifacts = append(step.Artifacts, view.Screenshot)
					}
				}
			}
		}
		rt.AddStep(step)
	}

	finalSummary := fmt.Sprintf("dashboard %s is ready for local rendering", slug)
	if overall != operator.StatusSuccess {
		finalSummary = fmt.Sprintf("dashboard %s has local rendering gaps", slug)
	}

	rt.AddStep(operator.Step{
		ID:      "dashboard-smoke-summary",
		Title:   "Dashboard smoke summary",
		Status:  overall,
		Summary: finalSummary,
		Details: map[string]any{
			"dashboard": summary,
		},
	})

	return rt.Complete(overall, finalSummary)
}

func runDevFixtures(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devFixturesJSON, cmd.OutOrStdout())
	rt.SetProfile("fixtures")
	outputPath := devFixturesOutput
	if !cmd.Flags().Changed("output") {
		settings, err := selectedProject()
		if err != nil {
			rt.AddStep(operator.Step{ID: "resolve-project", Title: "Resolve fixture project", Status: operator.StatusFailure, Summary: err.Error()})
			return rt.Complete(operator.StatusFailure, "fixture project resolution failed")
		}
		outputPath = settings.DevFixtures
	}

	target := ""
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		target = strings.TrimSpace(args[0])
	}

	dashboardTargets, err := resolveFixtureDashboardTargets(configFile(""), target, devFixturesAllDashboards)
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-dashboard",
			Title:   "Resolve Lovelace dashboard",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "fixture refresh could not resolve dashboard")
	}

	entitySet := map[string]bool{}
	for _, dashboard := range dashboardTargets {
		entityRefs, _, err := inspectDashboardYAML(dashboard.File)
		if err != nil {
			rt.AddStep(operator.Step{
				ID:      "parse-dashboard",
				Title:   "Inspect Lovelace dashboard configuration",
				Status:  operator.StatusFailure,
				Summary: err.Error(),
				Details: map[string]any{
					"dashboard": dashboard,
				},
			})
			return rt.Complete(operator.StatusFailure, "fixture refresh could not parse dashboard")
		}
		for _, entityID := range entityRefs {
			entitySet[entityID] = true
		}
	}
	entityRefs := sortedKeys(entitySet)
	sourceDashboard := dashboardTargets[0].Slug
	if len(dashboardTargets) > 1 {
		sourceDashboard = "all-dashboards"
	}
	rt.AddStep(operator.Step{
		ID:      "parse-dashboard",
		Title:   "Inspect Lovelace dashboard configuration",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("found %d unique entity reference(s) across %d dashboard(s)", len(entityRefs), len(dashboardTargets)),
		Details: map[string]any{
			"dashboards":   dashboardTargets,
			"entity_refs":  entityRefs,
			"merge_output": !devFixturesReplace,
		},
	})

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     devFixturesProdURL,
		ProdToken:   devFixturesProdToken,
		DevURL:      devFixturesDevURL,
		DevToken:    devFixturesDevToken,
		LegacyURL:   devFixturesHAURL,
		LegacyToken: devFixturesHAToken,
	}
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		resolved.Target.Guardrails = append(resolved.Target.Guardrails, "Writes a sanitized local fixture file.")
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve Home Assistant target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "fixture refresh could not resolve Home Assistant target")
	}

	client := hasync.NewClient(resolved.Config.URL, resolved.Config.Token).WithContext(cmd.Context())
	states, err := client.FetchEntities()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "fetch-states",
			Title:   "Fetch Home Assistant states",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "fixture refresh could not fetch Home Assistant states")
	}
	stateByEntity := make(map[string]hasync.EntityState, len(states))
	for _, state := range states {
		stateByEntity[state.EntityID] = state
	}

	refreshedFixtures := map[string]map[string]any{}
	var missing []string
	for _, entityID := range entityRefs {
		state, ok := stateByEntity[entityID]
		if !ok {
			missing = append(missing, entityID)
			continue
		}
		refreshedFixtures[entityID] = map[string]any{
			"state":      state.State,
			"attributes": sanitizeFixtureAttributes(state.Attributes),
		}
	}
	sort.Strings(missing)

	payload, err := buildFixturePayload(outputPath, refreshedFixtures, !devFixturesReplace)
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "merge-fixtures",
			Title:   "Merge redacted state fixtures",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "fixture refresh could not merge fixture file")
	}
	payload.Source = map[string]any{
		"dashboard":        sourceDashboard,
		"dashboards":       dashboardTargets,
		"target":           resolved.Target.Name,
		"generated":        time.Now().UTC().Format(time.RFC3339),
		"merge_existing":   !devFixturesReplace,
		"refreshed_count":  len(refreshedFixtures),
		"total_count":      len(payload.Entities),
		"missing_entities": missing,
	}

	if err := writeJSONFile(outputPath, payload); err != nil {
		rt.AddStep(operator.Step{
			ID:      "write-fixtures",
			Title:   "Write redacted state fixtures",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "fixture refresh could not write fixture file")
	}

	status := operator.StatusSuccess
	summary := fmt.Sprintf("wrote %d redacted fixture(s), refreshed %d", len(payload.Entities), len(refreshedFixtures))
	if len(missing) > 0 {
		status = operator.StatusPartial
		summary = fmt.Sprintf("wrote %d redacted fixture(s), refreshed %d, %d dashboard entity reference(s) missing from target", len(payload.Entities), len(refreshedFixtures), len(missing))
	}
	rt.AddStep(operator.Step{
		ID:      "write-fixtures",
		Title:   "Write redacted state fixtures",
		Status:  status,
		Summary: summary,
		Details: map[string]any{
			"output":             outputPath,
			"fixture_count":      len(payload.Entities),
			"refreshed_count":    len(refreshedFixtures),
			"missing_entities":   missing,
			"merged_with_output": !devFixturesReplace,
		},
		Artifacts: []string{outputPath},
	})
	return rt.Complete(status, summary)
}

func resolveDashboardFile(configDir, target string) (string, string, error) {
	targets, err := listDashboardTargets(configDir)
	if err != nil {
		return "", "", err
	}
	for _, candidate := range targets {
		if target == candidate.Slug {
			return candidate.Slug, candidate.File, nil
		}
	}
	if strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml") || strings.ContainsAny(target, "/\\") {
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", "", fmt.Errorf("dashboard file %q is not readable: %w", path, err)
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("dashboard file %q is a directory", path)
		}
		slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if registered, err := dashboardDefinitionSlugForPath(configDir, path); err != nil {
			return "", "", err
		} else if registered != "" {
			slug = registered
		}
		return slug, path, nil
	}
	return "", "", fmt.Errorf("dashboard %q was not found in %s Lovelace registrations", target, configDir)
}

func dashboardDefinitionSlugForPath(configDir, path string) (string, error) {
	targets, err := listDashboardTargets(configDir)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		targetPath, err := filepath.Abs(target.File)
		if err != nil {
			return "", err
		}
		if targetPath == absPath {
			return target.Slug, nil
		}
	}
	return "", nil
}

func resolveFixtureDashboardTargets(configDir, target string, allDashboards bool) ([]dashboardTarget, error) {
	if allDashboards {
		targets, err := listDashboardTargets(configDir)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("no dashboards were found under %s", filepath.Join(configDir, "dashboards", "definitions"))
		}
		return targets, nil
	}
	if strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("specify a dashboard slug or YAML file, or use --all-dashboards")
	}
	slug, file, err := resolveDashboardFile(configDir, target)
	if err != nil {
		return nil, err
	}
	return []dashboardTarget{{Slug: slug, File: file}}, nil
}

func inspectDashboardYAML(path string) ([]string, []string, error) {
	root, err := loadDashboardYAML(path)
	if err != nil {
		return nil, nil, err
	}
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("dashboard %s must be a YAML mapping", path)
	}
	entities := map[string]bool{}
	customCards := map[string]bool{}
	collectDashboardRefs(root, "", entities, customCards)
	return sortedKeys(entities), sortedKeys(customCards), nil
}

var entityIDPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z0-9_]+$`)

func collectDashboardRefs(node *yaml.Node, key string, entities map[string]bool, customCards map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			childKey := node.Content[i].Value
			child := node.Content[i+1]
			if childKey == "type" && child.Kind == yaml.ScalarNode && strings.HasPrefix(child.Value, "custom:") {
				customCards[strings.TrimPrefix(child.Value, "custom:")] = true
			}
			if dashboardEntityKey(childKey) {
				collectEntityValues(child, entities)
			}
			collectDashboardRefs(child, childKey, entities, customCards)
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			collectDashboardRefs(child, key, entities, customCards)
		}
	case yaml.ScalarNode:
		if dashboardEntityKey(key) {
			collectEntityValues(node, entities)
		}
	}
}

func dashboardEntityKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if key == "entity" || key == "entity_id" || key == "entities" {
		return true
	}
	if strings.HasSuffix(key, "_entity") || strings.HasSuffix(key, "_entities") {
		return true
	}
	for _, suffix := range []string{
		"_sensor",
		"_sensors",
		"_button",
		"_buttons",
		"_number",
		"_numbers",
		"_select",
		"_switch",
		"_switches",
		"_light",
		"_lights",
		"_fan",
		"_fans",
		"_lock",
		"_locks",
		"_cover",
		"_covers",
		"_camera",
		"_cameras",
		"_climate",
		"_weather",
		"_vacuum",
		"_media_player",
		"_remote",
		"_water_heater",
	} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func collectEntityValues(node *yaml.Node, entities map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(node.Value)
		if entityIDPattern.MatchString(value) {
			entities[value] = true
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			collectEntityValues(child, entities)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if dashboardEntityKey(node.Content[i].Value) {
				collectEntityValues(node.Content[i+1], entities)
			}
		}
	}
}

func loadReferenceEntities(path string) (map[string]bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entities := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if entityIDPattern.MatchString(line) {
			entities[line] = true
		}
	}
	return entities, nil
}

func checkDevLovelaceResources(ctx context.Context, configDir string, customCards []string) ([]string, operator.Status, string) {
	if len(customCards) == 0 {
		return nil, operator.StatusSuccess, "dashboard does not use custom cards"
	}
	var configured []string
	if configDir != "" {
		var err error
		configured, err = configuredDashboardResources(configDir)
		if err != nil {
			return nil, operator.StatusFailure, err.Error()
		}
	}
	live, err := fetchLiveLovelaceResources(ctx)
	if err != nil {
		return configured, operator.StatusPartial, fmt.Sprintf("custom-card resource readiness requires local HA: %v", err)
	}
	if len(live) == 0 {
		return live, operator.StatusFailure, "running HA has no registered Lovelace resources for custom cards"
	}
	registered := map[string]bool{}
	for _, resource := range live {
		registered[resource] = true
	}
	for _, resource := range configured {
		if !registered[resource] {
			return live, operator.StatusFailure, fmt.Sprintf("configured resource %s is not registered in local HA", resource)
		}
	}
	for _, resource := range live {
		if err := fetchDevStaticResource(ctx, resource); err != nil {
			return live, operator.StatusFailure, fmt.Sprintf("registered resource %s is not fetchable: %v", resource, err)
		}
	}
	return live, operator.StatusSuccess, "registered Lovelace resources are reachable; use --render to verify custom elements"
}

func fetchDevStaticResource(ctx context.Context, resourceURL string) error {
	resolved, err := operator.ResolveTargetContext(ctx, haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if err != nil {
		return err
	}
	return fetchDashboardResource(ctx, resolved.Config.URL, resolved.Config.Token, resourceURL)
}

func fetchDashboardResource(ctx context.Context, baseURL, token, resourceURL string) error {
	base, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	reference, err := url.Parse(resourceURL)
	if err != nil {
		return err
	}
	target := base.ResolveReference(reference)
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("unsupported resource URL scheme")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	// Third-party resources must never receive the Home Assistant token.
	if target.Scheme == base.Scheme && target.Host == base.Host && token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := hahttp.NewClient(15 * time.Second).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	// A SPA fallback is not a successfully loaded JavaScript/CSS resource.
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return fmt.Errorf("received HTML instead of a frontend resource")
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 16<<20))
	return err
}

func fetchLiveLovelaceResources(ctx context.Context) ([]string, error) {
	resolved, err := operator.ResolveTargetContext(ctx, haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws := hasync.NewWSClient(resolved.Config.URL, resolved.Config.Token)
	if err := ws.ConnectContext(ctx); err != nil {
		return nil, err
	}
	defer ws.Close()
	data, err := ws.SendCommandContext(ctx, "lovelace/resources/list", nil)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	urls := extractResourceURLs(payload)
	sort.Strings(urls)
	return urls, nil
}

var fixtureAttributeAllowlist = map[string]bool{
	"clock_best_wall":           true,
	"brightness":                true,
	"cloud_coverage":            true,
	"color_mode":                true,
	"condition":                 true,
	"current_position":          true,
	"current_temperature":       true,
	"datetime":                  true,
	"device_class":              true,
	"dew_point":                 true,
	"event_type":                true,
	"event_types":               true,
	"forecast":                  true,
	"friendly_name":             true,
	"humidity":                  true,
	"hs_color":                  true,
	"hvac_modes":                true,
	"max":                       true,
	"media_artist":              true,
	"media_content_type":        true,
	"media_position":            true,
	"media_title":               true,
	"min":                       true,
	"native_temperature":        true,
	"native_templow":            true,
	"operation_list":            true,
	"options":                   true,
	"percentage":                true,
	"precipitation":             true,
	"precipitation_probability": true,
	"pressure":                  true,
	"pressure_unit":             true,
	"source":                    true,
	"state_class":               true,
	"step":                      true,
	"supported_color_modes":     true,
	"supported_features":        true,
	"target_temp_high":          true,
	"target_temp_low":           true,
	"temperature":               true,
	"temperature_unit":          true,
	"templow":                   true,
	"unit_of_measurement":       true,
	"visibility":                true,
	"visibility_unit":           true,
	"wind_bearing":              true,
	"wind_gust_speed":           true,
	"wind_speed":                true,
	"wind_speed_unit":           true,
}

func sanitizeFixtureAttributes(attrs map[string]interface{}) map[string]any {
	sanitized := map[string]any{}
	for key, value := range attrs {
		if !fixtureAttributeAllowlist[key] {
			continue
		}
		sanitized[key] = sanitizeFixtureValue(value)
	}
	return sanitized
}

func sanitizeFixtureValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, nested := range typed {
			if fixtureAttributeAllowlist[key] {
				result[key] = sanitizeFixtureValue(nested)
			}
		}
		return result
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, sanitizeFixtureValue(item))
		}
		return items
	default:
		return typed
	}
}

func buildFixturePayload(path string, refreshed map[string]map[string]any, mergeExisting bool) (*devFixturePayload, error) {
	data, err := json.Marshal(refreshed)
	if err != nil {
		return nil, fmt.Errorf("invalid refreshed fixture records: %w", err)
	}
	if _, err := decodeDevFixtureDocument(data); err != nil {
		return nil, fmt.Errorf("invalid refreshed fixture records: %w", err)
	}
	payload := &devFixturePayload{
		SchemaVersion: devFixtureSchemaVersion,
		Description:   "Filtered live-state fixtures for local dashboard rendering. Names and state values may contain private data; review before sharing.",
		Entities:      map[string]map[string]any{},
	}
	if mergeExisting {
		existing, err := readFixturePayload(path)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			for entityID, fixture := range existing.Entities {
				payload.Entities[entityID] = sanitizeFixtureRecord(fixture)
			}
		}
	}
	for entityID, fixture := range refreshed {
		payload.Entities[entityID] = sanitizeFixtureRecord(fixture)
	}
	return payload, nil
}

func readFixturePayload(path string) (*devFixturePayload, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	document, err := decodeDevFixtureDocument(content)
	if err != nil {
		return nil, fmt.Errorf("parse existing fixture file %s: %w", path, err)
	}
	payload := &devFixturePayload{SchemaVersion: devFixtureSchemaVersion, Description: document.Description, Source: document.Source, Entities: map[string]map[string]any{}}
	for entityID, state := range document.Entities {
		payload.Entities[entityID] = map[string]any{"state": state.State, "attributes": sanitizeFixtureAttributes(state.Attributes)}
	}
	return payload, nil
}

func sanitizeFixtureRecord(fixture map[string]any) map[string]any {
	sanitized := map[string]any{}
	if state, ok := fixture["state"]; ok {
		sanitized["state"] = state
	}
	if attrs, ok := fixture["attributes"].(map[string]any); ok {
		sanitized["attributes"] = sanitizeFixtureAttributes(attrs)
	}
	return sanitized
}

func writeJSONFile(path string, payload any) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func checkLiveDashboardEntities(ctx context.Context, entityRefs []string) (operator.Status, string, []string, []dashboardStateIssue) {
	resolved, err := operator.ResolveTargetContext(ctx, haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if err != nil {
		if devDashboardRequireRunning {
			return operator.StatusFailure, err.Error(), nil, nil
		}
		return operator.StatusPartial, fmt.Sprintf("local HA state check skipped: %v", err), nil, nil
	}
	client := hasync.NewClient(resolved.Config.URL, resolved.Config.Token).WithContext(ctx)
	states, err := client.FetchEntities()
	if err != nil {
		if devDashboardRequireRunning {
			return operator.StatusFailure, err.Error(), nil, nil
		}
		return operator.StatusPartial, fmt.Sprintf("local HA state check skipped: %v", err), nil, nil
	}
	stateByEntity := make(map[string]hasync.EntityState, len(states))
	for _, state := range states {
		stateByEntity[state.EntityID] = state
	}
	var missing []string
	var issues []dashboardStateIssue
	for _, entityID := range entityRefs {
		state, ok := stateByEntity[entityID]
		if !ok {
			missing = append(missing, entityID)
			continue
		}
		if reason := unhealthyDashboardStateReason(entityID, state.State); reason != "" {
			issues = append(issues, dashboardStateIssue{
				EntityID: entityID,
				State:    state.State,
				Reason:   reason,
			})
		}
	}
	sort.Strings(missing)
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].EntityID < issues[j].EntityID
	})
	if len(missing) > 0 || len(issues) > 0 {
		parts := []string{}
		if len(missing) > 0 {
			parts = append(parts, fmt.Sprintf("%d missing", len(missing)))
		}
		if len(issues) > 0 {
			parts = append(parts, fmt.Sprintf("%d unhealthy", len(issues)))
		}
		return operator.StatusFailure, fmt.Sprintf("dashboard entity state gaps: %s", strings.Join(parts, ", ")), missing, issues
	}
	return operator.StatusSuccess, "all dashboard entities have healthy local HA display states", nil, nil
}

func unhealthyDashboardStateReason(entityID string, state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" {
		return "entity state is empty"
	}
	if normalized == "unavailable" {
		return "entity is unavailable"
	}
	if normalized == "unknown" && !dashboardDomainAllowsUnknown(entityID) {
		return "entity state is unknown"
	}
	return ""
}

func dashboardDomainAllowsUnknown(entityID string) bool {
	switch dashboardEntityDomain(entityID) {
	case "scene", "script", "button", "input_button", "image", "event":
		return true
	default:
		return false
	}
}

func dashboardEntityDomain(entityID string) string {
	domain, _, ok := strings.Cut(entityID, ".")
	if !ok {
		return ""
	}
	return domain
}

func dashboardLoginCredentials(env *devEnvironment, targetURL string) (devLoginCredentials, error) {
	credentials := devLoginCredentials{
		Username: os.Getenv("HASS_DEV_USERNAME"),
		Password: os.Getenv("HASS_DEV_PASSWORD"),
	}
	if credentials.Username != "" || credentials.Password != "" {
		if credentials.Username == "" || credentials.Password == "" {
			return credentials, fmt.Errorf("set both HASS_DEV_USERNAME and HASS_DEV_PASSWORD for dashboard rendering")
		}
		return credentials, nil
	}
	if strings.TrimRight(targetURL, "/") != strings.TrimRight(env.HAURL, "/") {
		return credentials, fmt.Errorf("dashboard target differs from the selected runtime; supply its HASS_DEV_USERNAME and HASS_DEV_PASSWORD")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(env.ComposeFile), "credentials.json"))
	if err != nil {
		return credentials, fmt.Errorf("dashboard login unavailable; start the portable runtime or supply HASS_DEV_USERNAME and HASS_DEV_PASSWORD")
	}
	if json.Unmarshal(data, &credentials) != nil || credentials.Username == "" || credentials.Password == "" {
		return devLoginCredentials{}, fmt.Errorf("invalid local development credentials file")
	}
	return credentials, nil
}

func runDashboardRenderCheck(ctx context.Context, source *dashboardSource) (operator.Status, string, *dashboardRenderCheck) {
	slug := source.Slug
	if source.Mode == "yaml" {
		registered, err := dashboardDefinitionSlugForPath(configFile(""), source.File)
		if err != nil {
			return operator.StatusFailure, err.Error(), nil
		}
		if registered == "" {
			return operator.StatusFailure, "register the YAML file under lovelace.dashboards before rendering it in HA", nil
		}
	}
	paths, err := dashboardNodeViewPaths(source.Root, devDashboardViews)
	if err != nil {
		return operator.StatusFailure, err.Error(), nil
	}
	_, customCards, err := inspectDashboardSource(source)
	if err != nil {
		return operator.StatusFailure, err.Error(), nil
	}
	pathsJSON, _ := json.Marshal(paths)
	customCardsJSON, _ := json.Marshal(customCards)
	resolved, err := operator.ResolveTargetContext(ctx, haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if err != nil {
		if devDashboardRequireRunning {
			return operator.StatusFailure, err.Error(), nil
		}
		return operator.StatusPartial, fmt.Sprintf("dashboard render skipped: %v", err), nil
	}

	env, err := currentDevEnvironment()
	if err != nil {
		return operator.StatusFailure, err.Error(), nil
	}
	credentials, err := dashboardLoginCredentials(env, resolved.Config.URL)
	if err != nil {
		return operator.StatusFailure, err.Error(), nil
	}

	url := strings.TrimSuffix(resolved.Config.URL, "/") + dashboardURLPath(slug)
	artifactDir := filepath.Join(selectedProjectRoot(), "artifacts", "dashboard-render")
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return operator.StatusFailure, fmt.Sprintf("dashboard render could not create artifact directory: %v", err), nil
	}
	runDir, err := os.MkdirTemp(artifactDir, slug+"-")
	if err != nil {
		return operator.StatusFailure, fmt.Sprintf("dashboard render could not create run directory: %v", err), nil
	}
	screenshot := filepath.Join(runDir, slug+".png")
	resultPath := filepath.Join(runDir, slug+".json")
	runnerDir, err := ensureDashboardRenderRunner(ctx, artifactDir)
	if err != nil {
		return operator.StatusFailure, fmt.Sprintf("dashboard render could not prepare Playwright runner: %v", err), nil
	}
	screenshot, _ = filepath.Abs(screenshot)
	resultPath, _ = filepath.Abs(resultPath)
	scriptPath := filepath.Join(runnerDir, "render-"+slug+".cjs")
	scriptPath, _ = filepath.Abs(scriptPath)
	script := dashboardRenderScript()
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		return operator.StatusFailure, fmt.Sprintf("dashboard render could not write Playwright script: %v", err), nil
	}

	timeout := devDashboardRenderTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", scriptPath)
	command.Dir = runnerDir
	command.Env = append(os.Environ(),
		"DASHBOARD_URL="+url,
		"VIEW_PATHS="+string(pathsJSON),
		"CUSTOM_CARDS="+string(customCardsJSON),
		"HA_USERNAME="+credentials.Username,
		"HA_PASSWORD="+credentials.Password,
		"SCREENSHOT_PATH="+screenshot,
		"RESULT_PATH="+resultPath,
		fmt.Sprintf("RENDER_TIMEOUT_MS=%d", timeout.Milliseconds()),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		summary := fmt.Sprintf("dashboard render failed: %v", err)
		if ctx.Err() == context.DeadlineExceeded {
			summary = fmt.Sprintf("dashboard render timed out after %s", timeout+15*time.Second)
		}
		check := &dashboardRenderCheck{
			URL:        url,
			Screenshot: screenshot,
		}
		loadDashboardRenderResult(resultPath, check)
		if message := redactDashboardDiagnostic(strings.TrimSpace(string(output))); message != "" {
			check.PageErrors = append(check.PageErrors, message)
		}
		if _, statErr := os.Stat(check.Screenshot); statErr != nil {
			check.Screenshot = ""
		}
		return operator.StatusFailure, summary, check
	}

	check := &dashboardRenderCheck{URL: url, Screenshot: screenshot}
	if err := loadDashboardRenderResult(resultPath, check); err != nil {
		return operator.StatusFailure, fmt.Sprintf("dashboard render result could not be decoded: %v", err), check
	}
	if _, statErr := os.Stat(check.Screenshot); statErr != nil {
		check.Screenshot = ""
	}
	problems := len(check.ConsoleErrors) + len(check.RequestFailures) + len(check.PageErrors) + len(check.VisibleErrors)
	if problems > 0 {
		return operator.StatusFailure, fmt.Sprintf("dashboard rendered with %d browser issue(s)", problems), check
	}
	if check.CardCount == 0 {
		return operator.StatusFailure, "dashboard rendered but no Lovelace cards were detected", check
	}
	return operator.StatusSuccess, fmt.Sprintf("dashboard rendered %d view(s) with %d Lovelace card element(s)", len(check.Views), check.CardCount), check
}

var dashboardDiagnosticURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

func redactDashboardDiagnostic(value string) string {
	return dashboardDiagnosticURL.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	})
}

func loadDashboardRenderResult(path string, check *dashboardRenderCheck) error {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return readErr
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var redact func(any) any
	redact = func(value any) any {
		switch v := value.(type) {
		case string:
			return redactDashboardDiagnostic(v)
		case []any:
			for i := range v {
				v[i] = redact(v[i])
			}
		case map[string]any:
			for key := range v {
				v[key] = redact(v[key])
			}
		}
		return value
	}
	data, err := json.Marshal(redact(raw))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, check)
}

func dashboardRenderPackageCurrent(runnerDir string) bool {
	data, err := os.ReadFile(filepath.Join(runnerDir, "node_modules", "playwright", "package.json"))
	if err != nil {
		return false
	}
	var metadata struct {
		Version string `json:"version"`
	}
	return json.Unmarshal(data, &metadata) == nil && metadata.Version == dashboardRenderPlaywrightVersion
}

func ensureDashboardRenderRunner(ctx context.Context, artifactDir string) (string, error) {
	if err := dashboardBrowserToolchain(); err != nil {
		return "", err
	}
	runnerDir := filepath.Join(artifactDir, "playwright-runner")
	if err := os.MkdirAll(runnerDir, 0755); err != nil {
		return "", err
	}
	packageJSONPath := filepath.Join(runnerDir, "package.json")
	packageJSON := fmt.Sprintf(`{
  "private": true,
  "type": "commonjs",
  "dependencies": {
    "playwright": "%s"
  }
}
`, dashboardRenderPlaywrightVersion)
	current, readErr := os.ReadFile(packageJSONPath)
	if readErr != nil || string(current) != packageJSON {
		if err := os.WriteFile(packageJSONPath, []byte(packageJSON), 0644); err != nil {
			return "", err
		}
	}
	if !dashboardRenderPackageCurrent(runnerDir) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "npm", "install", "--no-audit", "--no-fund", "--silent")
		command.Dir = runnerDir
		output, err := command.CombinedOutput()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return "", fmt.Errorf("npm install timed out after 3m")
			}
			return "", fmt.Errorf("npm install failed: %v: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if err := ensureDashboardRenderChromium(ctx, runnerDir); err != nil {
		return "", err
	}
	if err := preflightDashboardBrowser(ctx, runnerDir); err != nil {
		return "", err
	}
	return runnerDir, nil
}

func ensureDashboardRenderChromium(ctx context.Context, runnerDir string) error {
	if path, err := dashboardRenderChromiumPath(ctx, runnerDir); err == nil && path != "" {
		if _, statErr := os.Stat(path); statErr == nil {
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "npm", "exec", "--", "playwright", "install", "chromium")
	command.Dir = runnerDir
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("playwright chromium install timed out after 5m")
		}
		return fmt.Errorf("playwright chromium install failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func dashboardRenderChromiumPath(ctx context.Context, runnerDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "-e", `const { chromium } = require("playwright"); process.stdout.write(chromium.executablePath());`)
	command.Dir = runnerDir
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("chromium path lookup timed out")
		}
		return "", fmt.Errorf("chromium path lookup failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func dashboardURLPath(slug string) string {
	if slug == "" || slug == "lovelace" {
		return "/lovelace/0"
	}
	return "/" + url.PathEscape(strings.Trim(slug, "/")) + "/0"
}

func dashboardRenderScript() string {
	return `const { chromium } = require("playwright");
const fs = require("node:fs/promises");
const url = process.env.DASHBOARD_URL;
const username = process.env.HA_USERNAME;
const password = process.env.HA_PASSWORD;
const screenshotPath = process.env.SCREENSHOT_PATH;
const resultPath = process.env.RESULT_PATH;
const viewPaths = JSON.parse(process.env.VIEW_PATHS || '["0"]');
const customCards = JSON.parse(process.env.CUSTOM_CARDS || '[]');
const timeout = Number(process.env.RENDER_TIMEOUT_MS || "90000");
const redactDiagnostic = value => String(value).split(password || "\0").join("[redacted]").replace(/https?:\/\/[^\s"'<>]+/g, text => {
  try { const target = new URL(text); target.username = ""; target.password = ""; target.search = ""; target.hash = ""; return target.toString(); }
  catch { return "[redacted URL]"; }
});
const result = { url, console_errors: [], request_failures: [], page_errors: [], visible_errors: [], card_count: 0, views: [] };
let browser;
let page;
// Resource query strings can contain short-lived HA credentials.
const diagnostic = value => String(value).replace(/https?:\/\/[^\s"'<>]+/g, value => {
  try { const parsed = new URL(value); parsed.search = ""; parsed.hash = ""; return parsed.toString(); } catch { return value; }
});
let current = result;
(async () => {
  if (!username || !password) throw new Error("Dashboard login credentials are required");
  browser = await chromium.launch({ headless: true });
  page = await browser.newPage({ viewport: { width: 1366, height: 900 } });
  page.setDefaultTimeout(timeout);
  page.on("console", message => { if (message.type() === "error") current.console_errors.push(diagnostic(message.text())); });
  page.on("pageerror", error => current.page_errors.push(diagnostic(error)));
  page.on("requestfailed", request => {
    const reason = request.failure()?.errorText || "";
    if (!reason.includes("ERR_ABORTED")) current.request_failures.push(request.method() + " " + diagnostic(request.url()) + " " + reason);
  });
  page.on("response", response => {
    if (response.status() >= 400) current.request_failures.push("HTTP " + response.status() + " " + diagnostic(response.url()));
  });
  // DOM readiness does not require streaming integrations to become network-idle.
  await page.goto(url, { waitUntil: "domcontentloaded", timeout });
  await Promise.race([
    page.locator("home-assistant-main").waitFor({state:"attached"}),
    page.locator("input[name='username']").first().waitFor({state:"visible"})
  ]);
  if (new URL(page.url()).origin !== new URL(url).origin) throw new Error("Dashboard navigation left the selected origin before login");
  const usernameInput = page.locator("input[name='username']").first();
  if (await usernameInput.isVisible()) {
    await usernameInput.fill(username);
    const passwordInput = page.locator("input[name='password'], ha-password-field input").first();
    await passwordInput.fill(password);
    const loginButton = page.getByRole("button", { name: /^log in$/i }).first();
    if (await loginButton.count()) await loginButton.click();
    else await passwordInput.press("Enter");
    await page.waitForURL(next => !next.pathname.startsWith("/auth/"), {timeout});
    if (new URL(page.url()).pathname.includes("onboarding")) throw new Error("Local HA onboarding is incomplete; run dm dev up or --ensure-dev before rendering");
    await page.locator("home-assistant-main").waitFor({state:"attached"});
  }
  for (let index = 0; index < viewPaths.length; index++) {
    const viewURL = url.replace(/\/[^/]*$/, "/" + encodeURIComponent(viewPaths[index]));
    current = { url: viewURL, screenshot: screenshotPath.replace(/\.png$/, "-" + index + ".png"), console_errors: [], request_failures: [], page_errors: [], visible_errors: [], card_count: 0 };
    result.views.push(current);
    try {
      await page.goto(viewURL, { waitUntil: "domcontentloaded", timeout });
      await page.locator("hui-root").waitFor({state:"attached"});
      await page.waitForFunction(expected => {
        const visit = root => Array.from(root.querySelectorAll("*")).some(node =>
          ((node.localName === "ha-card" || (node.localName.startsWith("hui-") && node.localName.endsWith("-card")) || expected.includes(node.localName)) && node.getBoundingClientRect().height > 0) ||
          (node.shadowRoot && visit(node.shadowRoot)));
        return visit(document);
      }, customCards, {timeout});
      // Custom resources may register after the first built-in card is ready.
      if (customCards.length) await page.waitForFunction(expected => expected.every(name => customElements.get(name)), customCards, {timeout}).catch(async () => {
        const missing = await page.evaluate(expected => expected.filter(name => !customElements.get(name)), customCards);
        throw new Error("Custom element doesn't exist: " + missing.join(", "));
      });
      // Let card state updates report their failures.
      await page.waitForTimeout(1000);
      const inspection = await page.evaluate(expected => {
        const cards = new Set();
        const errors = new Set();
        const visible = node => {
          const box = node.getBoundingClientRect();
          const style = getComputedStyle(node);
          return box.width > 0 && box.height > 0 && style.display !== "none" && style.visibility !== "hidden";
        };
        const walk = root => {
          for (const node of root.querySelectorAll("*")) {
            const name = node.localName;
            if (visible(node)) {
              if (name === "ha-card" || (name.startsWith("hui-") && name.endsWith("-card")) || expected.includes(name)) cards.add(node);
              if (name === "hui-error-card" || (name === "ha-alert" && (node.alertType === "error" || node.getAttribute("alert-type") === "error")) || node.matches(".error, [error]")) {
                const text = (node.innerText || node.textContent || "").trim();
                if (text) errors.add(text);
              }
              // Read text inside every open shadow root, where HA renders cards.
              for (const textNode of node.childNodes) {
                if (textNode.nodeType === Node.TEXT_NODE) {
                  const text = textNode.textContent || "";
                  for (const pattern of [/Configuration error/i, /Entity not found/i, /Custom element doesn't exist/i, /No card type configured/i]) {
                    if (pattern.test(text)) errors.add(text.trim());
                  }
                }
              }
            }
            if (node.shadowRoot) walk(node.shadowRoot);
          }
        };
        walk(document);
        // A successful HTTP fetch does not guarantee a card registered itself.
        for (const name of expected) if (!customElements.get(name)) errors.add("Custom element doesn't exist: " + name);
        return { count: cards.size, errors: [...errors] };
      }, customCards);
      current.card_count = inspection.count;
      current.visible_errors = inspection.errors;
      if (current.card_count === 0) current.visible_errors.push("No visible Lovelace cards detected");
      if (new URL(page.url()).pathname !== new URL(viewURL).pathname) current.visible_errors.push("HA redirected away from the selected view");
    } catch (error) {
      current.page_errors.push(diagnostic(error));
    }
    await page.screenshot({path: current.screenshot, fullPage: true}).catch(error => current.page_errors.push(diagnostic(error)));
    result.card_count += current.card_count;
    for (const key of ["console_errors", "request_failures", "page_errors", "visible_errors"]) result[key].push(...current[key].map(value => viewPaths[index] + ": " + value));
  }
  result.screenshot = result.views[0]?.screenshot;
})().catch(async error => {
  result.page_errors.push(diagnostic(error && error.stack ? error.stack : error));
  if (page) {
    await page.screenshot({path: screenshotPath, fullPage: true}).then(() => { result.screenshot = screenshotPath; }).catch(() => {});
  }
  process.exitCode = 1;
}).finally(async () => {
  if (browser) await browser.close();
  await fs.writeFile(resultPath, JSON.stringify(result, (_key, value) => typeof value === "string" ? redactDiagnostic(value) : value, 2) + "\n");
});
`
}

func extractResourceURLs(payload any) []string {
	var urls []string
	switch value := payload.(type) {
	case []any:
		for _, item := range value {
			urls = append(urls, extractResourceURLs(item)...)
		}
	case map[string]any:
		if url, ok := value["url"].(string); ok {
			urls = append(urls, url)
		}
		for _, key := range []string{"data", "resources", "items", "result"} {
			if nested, ok := value[key]; ok {
				urls = append(urls, extractResourceURLs(nested)...)
			}
		}
	}
	return urls
}

func liveHints(status operator.Status) []string {
	if status == operator.StatusSuccess {
		return nil
	}
	return []string{
		"Run `./dm dev up --json` or rerun with `./dm dev dashboard DASHBOARD --ensure-dev --json`.",
	}
}

func missingEntities(refs []string, available map[string]bool) []string {
	var missing []string
	for _, ref := range refs {
		if !available[ref] {
			missing = append(missing, ref)
		}
	}
	sort.Strings(missing)
	return missing
}

func yamlDocument(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func yamlMapScalar(node *yaml.Node, key string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key && node.Content[i+1].Kind == yaml.ScalarNode {
			return strings.TrimSpace(node.Content[i+1].Value)
		}
	}
	return ""
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
