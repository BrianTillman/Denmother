package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/haverify"
	"github.com/BrianTillman/Denmother/internal/operator"
)

type verifyExecutionOptions struct {
	Args         []string
	Pattern      string
	Verbose      bool
	DryRun       bool
	PrintResults bool
}

type resolvedVerifyDevice struct {
	auto       *haverify.ConfigAutomation
	sourceFile string
	z2mName    string
	prefix     string
	expects    []haverify.ParamExpectation
	skipped    bool
	skipReason string
}

type verifySummary struct {
	FilesDiscovered          int
	DevicesTotal             int
	DevicesChecked           int
	DevicesSkipped           int
	DevicesWithFailures      int
	Passed                   int
	Failed                   int
	NotFound                 int
	Unavailable              int
	InvalidOption            int
	Issues                   []verifyIssue
	StaleRestoredAutomations []haverify.StaleRestoredAutomation
	DryRun                   bool
}

type verifyIssue struct {
	Device     string   `json:"device"`
	SourceFile string   `json:"source_file"`
	Parameter  string   `json:"parameter"`
	EntityID   string   `json:"entity_id,omitempty"`
	Expected   string   `json:"expected"`
	Actual     string   `json:"actual,omitempty"`
	Reason     string   `json:"reason"`
	Options    []string `json:"options,omitempty"`
}

func (s *verifySummary) Status() operator.Status {
	switch {
	case s.Failed > 0 || s.NotFound > 0 || len(s.StaleRestoredAutomations) > 0:
		return operator.StatusFailure
	case s.DevicesChecked == 0:
		return operator.StatusWarning
	default:
		return operator.StatusSuccess
	}
}

func (s *verifySummary) Step(id, title string) operator.Step {
	details := map[string]any{
		"files_discovered":           s.FilesDiscovered,
		"devices_total":              s.DevicesTotal,
		"devices_checked":            s.DevicesChecked,
		"devices_skipped":            s.DevicesSkipped,
		"devices_with_failures":      s.DevicesWithFailures,
		"passed":                     s.Passed,
		"failed":                     s.Failed,
		"not_found":                  s.NotFound,
		"unavailable":                s.Unavailable,
		"invalid_option":             s.InvalidOption,
		"dry_run":                    s.DryRun,
		"stale_restored_automations": s.StaleRestoredAutomations,
	}
	if len(s.Issues) > 0 {
		details["issues"] = s.Issues
	}

	step := operator.Step{
		ID:      id,
		Title:   title,
		Status:  s.Status(),
		Summary: s.describe(),
		Details: details,
	}

	if step.Status == operator.StatusWarning {
		step.Hints = append(step.Hints, "No verifiable Inovelli config automations were found. Run `dm verify --pattern ...` on a narrower slice if needed.")
	}
	if len(s.StaleRestoredAutomations) > 0 {
		step.Hints = append(step.Hints,
			"Confirm each listed automation was retired from YAML before removing only that entity through the Home Assistant entity registry; dm is read-only and never deletes registry entries.",
			"Re-run `dm verify --json` after manual registry cleanup to confirm the restored unavailable entities are gone.",
		)
	}

	return step
}

func (s *verifySummary) describe() string {
	if s.DryRun {
		return fmt.Sprintf("resolved expectations for %d device(s), skipped %d", s.DevicesChecked, s.DevicesSkipped)
	}
	return fmt.Sprintf("%d passed, %d failed, %d not found, %d unavailable, %d invalid option, %d stale restored automations across %d verified device(s)", s.Passed, s.Failed, s.NotFound, s.Unavailable, s.InvalidOption, len(s.StaleRestoredAutomations), s.DevicesChecked)
}

func executeVerify(config *haconfig.HAConfig, opts verifyExecutionOptions) (*verifySummary, error) {
	configFiles, err := discoverConfigAutomationsWithPattern(opts.Args, opts.Pattern)
	if err != nil {
		return nil, err
	}

	devices, err := resolveVerifyDevices(configFiles)
	if err != nil {
		return nil, err
	}

	summary := &verifySummary{
		FilesDiscovered: len(configFiles),
		DevicesTotal:    len(devices),
		DryRun:          opts.DryRun,
	}

	if opts.DryRun {
		var dryResults []haverify.DryRunResult
		for _, d := range devices {
			if d.skipped {
				summary.DevicesSkipped++
			} else {
				summary.DevicesChecked++
			}
			dryResults = append(dryResults, haverify.DryRunResult{
				Name:         d.auto.Alias,
				SourceFile:   d.sourceFile,
				Z2MName:      d.z2mName,
				EntityPrefix: d.prefix,
				Expectations: d.expects,
				Skipped:      d.skipped,
				SkipReason:   d.skipReason,
			})
		}
		if opts.PrintResults {
			haverify.PrintDryRunReport(dryResults)
		}
		return summary, nil
	}

	client := hasync.NewClient(config.URL, config.Token)
	entities, err := client.FetchEntities()
	if err != nil {
		return nil, fmt.Errorf("fetch entities: %w", err)
	}
	staleRestoredAutomations, err := detectStaleRestoredAutomations(entities)
	if err != nil {
		return nil, err
	}
	summary.StaleRestoredAutomations = staleRestoredAutomations

	lookup := haverify.BuildEntityLookup(entities)
	results := make([]haverify.DeviceResult, 0, len(devices))

	for _, d := range devices {
		if d.skipped {
			summary.DevicesSkipped++
			results = append(results, haverify.DeviceResult{
				Name:       d.auto.Alias,
				SourceFile: d.sourceFile,
				Skipped:    true,
				SkipReason: d.skipReason,
			})
			continue
		}

		summary.DevicesChecked++

		result := haverify.DeviceResult{
			Name:       d.auto.Alias,
			SourceFile: d.sourceFile,
			Z2MName:    d.z2mName,
		}

		for _, exp := range d.expects {
			check := haverify.EvaluateExpectation(lookup, d.prefix, exp)

			if check.NotFound {
				result.NotFound++
				summary.NotFound++
				summary.Issues = append(summary.Issues, verifyIssueFromCheck(d, check, "entity_not_found"))
			} else if check.Unavailable {
				result.Failed++
				summary.Failed++
				summary.Unavailable++
				summary.Issues = append(summary.Issues, verifyIssueFromCheck(d, check, "entity_unavailable"))
			} else if check.InvalidOption {
				result.Failed++
				summary.Failed++
				summary.InvalidOption++
				summary.Issues = append(summary.Issues, verifyIssueFromCheck(d, check, "invalid_select_option"))
			} else if check.Pass {
				result.Passed++
				summary.Passed++
			} else {
				result.Failed++
				summary.Failed++
				summary.Issues = append(summary.Issues, verifyIssueFromCheck(d, check, "value_mismatch"))
			}

			result.Params = append(result.Params, check)
		}

		if result.Failed > 0 || result.NotFound > 0 {
			summary.DevicesWithFailures++
		}

		results = append(results, result)
	}

	if opts.PrintResults {
		haverify.PrintReport(results, opts.Verbose)
		printStaleRestoredAutomations(staleRestoredAutomations)
	}

	return summary, nil
}

func detectStaleRestoredAutomations(entities []hasync.EntityState) ([]haverify.StaleRestoredAutomation, error) {
	localIDs, err := haverify.LoadAutomationIDs(filepath.Join(configPath, "automations"))
	if err != nil {
		return nil, fmt.Errorf("load local automation IDs: %w", err)
	}
	return haverify.FindStaleRestoredAutomations(entities, localIDs), nil
}

func printStaleRestoredAutomations(entries []haverify.StaleRestoredAutomation) {
	if len(entries) == 0 {
		return
	}
	fmt.Println("Stale restored automations detected from REST states (read-only):")
	for _, entry := range entries {
		fmt.Printf("  - %s (automation id: %s)\n", entry.EntityID, entry.UniqueID)
	}
	fmt.Println("  Confirm YAML retirement before manually removing exact entries; dm never deletes registry data.")
}

func verifyIssueFromCheck(d resolvedVerifyDevice, check haverify.ParamCheck, reason string) verifyIssue {
	return verifyIssue{
		Device:     d.auto.Alias,
		SourceFile: d.sourceFile,
		Parameter:  check.MQTTParam,
		EntityID:   check.EntityID,
		Expected:   check.Expected,
		Actual:     check.Actual,
		Reason:     reason,
		Options:    check.Options,
	}
}

func resolveVerifyDevices(configFiles []string) ([]resolvedVerifyDevice, error) {
	devices := make([]resolvedVerifyDevice, 0, len(configFiles))

	for _, path := range configFiles {
		auto, err := haverify.LoadConfigAutomation(path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}

		if strings.Contains(auto.UseBlueprint.Path, "z2m_inovelli_blue_dimmer_settings") ||
			strings.Contains(auto.UseBlueprint.Path, "z2m_inovelli_mmWave_dimmer_settings") ||
			strings.Contains(auto.UseBlueprint.Path, "z2m_inovelli_dimmer_settings") ||
			strings.Contains(auto.UseBlueprint.Path, "z2m_inovelli_fan_canopy_settings") {
			bp, err := haverify.LoadBlueprint(configPath, auto.UseBlueprint.Path)
			if err != nil {
				return nil, fmt.Errorf("load blueprint for %s: %w", path, err)
			}

			defaults := haverify.ExtractDefaults(bp)
			mergedInputs := haverify.MergeInputs(defaults, auto.UseBlueprint.Input)
			expectations, declarative, err := haverify.ResolveDeclarativeMQTTSettings(bp.Actions, mergedInputs)
			if err != nil {
				return nil, fmt.Errorf("resolve declarative settings for %s: %w", path, err)
			}
			if !declarative {
				variables := haverify.BuildVariableMap(bp.Variables, mergedInputs)
				payloadParams := haverify.ExtractMQTTPayloads(bp.Actions)
				getParams := haverify.ExtractGETParams(bp.Actions)
				payloadParams = haverify.FilterVerifiable(payloadParams, getParams)
				expectations = haverify.ResolvePayloadParams(payloadParams, variables, mergedInputs)
				expectations = haverify.NormalizeDerivedExpectations(expectations)
				expectations = haverify.AppendReadOnlyInputExpectations(expectations, mergedInputs, getParams)
			}

			z2mName := fmt.Sprintf("%v", mergedInputs["z2m_friendly_name"])
			prefix := haverify.Z2MNameToEntityPrefix(z2mName)

			devices = append(devices, resolvedVerifyDevice{
				auto:       auto,
				sourceFile: path,
				z2mName:    z2mName,
				prefix:     prefix,
				expects:    expectations,
			})
			continue
		}

		if strings.Contains(auto.UseBlueprint.Path, "matter_inovelli_dimmer_settings") {
			bp, err := haverify.LoadBlueprint(configPath, auto.UseBlueprint.Path)
			if err != nil {
				return nil, fmt.Errorf("load blueprint for %s: %w", path, err)
			}

			defaults := haverify.ExtractDefaults(bp)
			mergedInputs := haverify.MergeInputs(defaults, auto.UseBlueprint.Input)
			expectations := haverify.ResolveMatterEntityExpectations(mergedInputs)

			devices = append(devices, resolvedVerifyDevice{
				auto:       auto,
				sourceFile: path,
				expects:    expectations,
			})
			continue
		}

		if strings.Contains(auto.UseBlueprint.Path, "matter_inovelli_fan_canopy_settings") {
			bp, err := haverify.LoadBlueprint(configPath, auto.UseBlueprint.Path)
			if err != nil {
				return nil, fmt.Errorf("load blueprint for %s: %w", path, err)
			}

			defaults := haverify.ExtractDefaults(bp)
			mergedInputs := haverify.MergeInputs(defaults, auto.UseBlueprint.Input)
			expectations := haverify.ResolveMatterFanCanopyEntityExpectations(mergedInputs)

			devices = append(devices, resolvedVerifyDevice{
				auto:       auto,
				sourceFile: path,
				expects:    expectations,
			})
			continue
		}

		devices = append(devices, resolvedVerifyDevice{
			auto:       auto,
			sourceFile: path,
			skipped:    true,
			skipReason: fmt.Sprintf("Unsupported config blueprint: %s", auto.UseBlueprint.Path),
		})
	}

	return devices, nil
}

func discoverConfigAutomationsWithPattern(args []string, pattern string) ([]string, error) {
	original := verifyPattern
	verifyPattern = pattern
	defer func() { verifyPattern = original }()
	return discoverConfigAutomations(args)
}

func verifySummaryText(summary *verifySummary) string {
	if summary == nil {
		return "verification finished with no summary"
	}

	switch summary.Status() {
	case operator.StatusSuccess:
		if summary.DryRun {
			return fmt.Sprintf("verification dry run resolved %d device(s)", summary.DevicesChecked)
		}
		return fmt.Sprintf("verification passed: %d parameter checks matched across %d device(s)", summary.Passed, summary.DevicesChecked)
	case operator.StatusWarning:
		if summary.DryRun {
			return "verification dry run found no verifiable devices"
		}
		return "verification found no verifiable Inovelli config devices"
	default:
		return fmt.Sprintf("verification failed: %s", summary.describe())
	}
}
