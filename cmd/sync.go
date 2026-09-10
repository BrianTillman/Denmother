package cmd

import (
	"fmt"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	syncProdURL      string
	syncProdToken    string
	syncDevURL       string
	syncDevToken     string
	haURL            string
	haToken          string
	skipMismatch     bool
	syncQuiet        bool
	syncEntitiesOnly bool
	skipOrphans      bool
	syncJSON         bool
)

type syncCommandRunner interface {
	SetQuiet(bool)
	SyncEntities() (int, string, error)
	SyncAll() (*hasync.SyncResult, error)
	FindOrphanedDevices() (*hasync.OrphanResult, error)
}

var (
	resolveSyncTarget          = operator.ResolveTarget
	newSyncCommandRunner       = func(url, token, configPath string) syncCommandRunner { return hasync.NewSyncer(url, token, configPath) }
	generateSyncSchema         = hasync.GenerateEntitySchema
	analyzeSyncMismatches      = hasync.AnalyzeMismatches
	generateSyncMismatchReport = hasync.GenerateMismatchReport
	printSyncMismatchSummary   = hasync.PrintMismatchSummary
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync entities, devices, and areas from Home Assistant",
	Long: `Sync entities, devices, and areas from a Home Assistant instance.

This command fetches the current state of entities, devices, and areas from
Home Assistant and saves them locally:
  - docs/reference/entity-list.txt
  - docs/reference/device-list.txt
  - docs/reference/area-list.txt
  - docs/reference/mismatch-report.txt (optional)

This command defaults to the PRODUCTION Home Assistant instance since it
syncs real device names, but explicit development flags can override that.

Connection Resolution (in order of precedence):
  1. Explicit override flags (--prod-url/--prod-token or --dev-url/--dev-token)
  2. Environment variables for the selected instance
  3. Legacy environment variables

Examples:
  # Use production environment variables
  export HASS_PROD_URL="https://ha.example.com"
  export HASS_PROD_TOKEN="your-token"
  dm sync

  # Explicit production URL and token
  dm sync --prod-url https://ha.example.com --prod-token TOKEN

  # Override to development explicitly
  dm sync --dev-url http://localhost:8123 --dev-token TOKEN`,
	RunE: runSync,
}

func init() {
	syncCmd.Flags().StringVar(&syncProdURL, "prod-url", "", "Production Home Assistant URL")
	syncCmd.Flags().StringVar(&syncProdToken, "prod-token", "", "Production Home Assistant token")
	syncCmd.Flags().StringVar(&syncDevURL, "dev-url", "", "Development Home Assistant URL")
	syncCmd.Flags().StringVar(&syncDevToken, "dev-token", "", "Development Home Assistant token")
	syncCmd.Flags().StringVar(&haURL, "ha-url", "", "Home Assistant URL (legacy, use --prod-url)")
	syncCmd.Flags().StringVar(&haToken, "ha-token", "", "Home Assistant token (legacy, use --prod-token)")
	syncCmd.Flags().BoolVar(&skipMismatch, "skip-mismatch-report", false, "Skip generating the mismatch report")
	syncCmd.Flags().BoolVarP(&syncQuiet, "quiet", "q", false, "Suppress sync progress output")
	syncCmd.Flags().BoolVar(&syncEntitiesOnly, "entities-only", false, "Only sync entities (skip devices and areas)")
	syncCmd.Flags().BoolVar(&skipOrphans, "no-orphans", false, "Skip orphaned device detection")
	syncCmd.Flags().BoolVar(&syncJSON, "json", false, "Emit a machine-readable JSON summary")

	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("sync", syncJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     syncProdURL,
		ProdToken:   syncProdToken,
		DevURL:      syncDevURL,
		DevToken:    syncDevToken,
		LegacyURL:   haURL,
		LegacyToken: haToken,
	}

	resolved, err := resolveSyncTarget(haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		resolved.Target.Guardrails = append(resolved.Target.Guardrails, "Updates local reference files.")
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
		return rt.Complete(operator.StatusFailure, "sync could not start")
	}

	effectiveQuiet := syncQuiet || syncJSON
	syncer := newSyncCommandRunner(resolved.Config.URL, resolved.Config.Token, configPath)
	syncer.SetQuiet(effectiveQuiet)

	if syncEntitiesOnly {
		return runEntityOnlySync(rt, syncer, effectiveQuiet)
	}

	return runFullSync(rt, syncer, resolved.Config.URL, effectiveQuiet)
}

func runEntityOnlySync(rt *operator.Runtime, syncer syncCommandRunner, effectiveQuiet bool) error {
	count, path, err := syncer.SyncEntities()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "sync-entities",
			Title:   "Sync entity reference data",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "entity sync failed")
	}

	step := operator.Step{
		ID:     "sync-entities",
		Title:  "Sync entity reference data",
		Status: operator.StatusSuccess,
		Summary: fmt.Sprintf(
			"synced %d entities",
			count,
		),
		Details: map[string]any{
			"entity_count": count,
			"entity_list":  path,
		},
	}
	rt.AddStep(step)

	status := operator.StatusSuccess
	summary := fmt.Sprintf("synced %d entities", count)

	schemaPath, schemaErr := generateSyncSchema(configPath)
	if schemaErr != nil {
		status = operator.StatusPartial
		rt.AddStep(operator.Step{
			ID:      "generate-schema",
			Title:   "Generate VSCode entity schema",
			Status:  operator.StatusPartial,
			Summary: schemaErr.Error(),
		})
		rt.AddHint("Entity sync completed, but the VSCode entity schema was not regenerated.")
	} else {
		rt.AddStep(operator.Step{
			ID:      "generate-schema",
			Title:   "Generate VSCode entity schema",
			Status:  operator.StatusSuccess,
			Summary: "regenerated VSCode entity schema",
			Details: map[string]any{"schema_path": schemaPath},
		})
		if !effectiveQuiet && schemaPath != "" && !syncJSON {
			fmt.Printf("Schema: %s\n", schemaPath)
		}
	}

	return rt.Complete(status, summary)
}

func runFullSync(rt *operator.Runtime, syncer syncCommandRunner, resolvedURL string, effectiveQuiet bool) error {
	result, err := syncer.SyncAll()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "sync-reference-data",
			Title:   "Sync Home Assistant reference data",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "sync failed")
	}

	overall := operator.StatusSuccess

	syncStepStatus := operator.StatusSuccess
	if len(result.Warnings) > 0 {
		syncStepStatus = operator.StatusPartial
	}
	rt.AddStep(operator.Step{
		ID:      "sync-reference-data",
		Title:   "Sync Home Assistant reference data",
		Status:  syncStepStatus,
		Summary: fmt.Sprintf("synced %d entities, %d devices, %d areas", result.EntitiesCount, result.DevicesCount, result.AreasCount),
		Details: map[string]any{
			"entity_count":  result.EntitiesCount,
			"device_count":  result.DevicesCount,
			"area_count":    result.AreasCount,
			"entity_list":   result.EntityListPath,
			"device_list":   result.DeviceListPath,
			"area_list":     result.AreaListPath,
			"core_warnings": result.Warnings,
		},
		Hints: result.Warnings,
	})
	overall = operator.MergeStatus(overall, syncStepStatus)

	postWarnings := []string{}
	postDetails := map[string]any{}

	if !skipMismatch && result.Entities != nil {
		analysis, err := analyzeSyncMismatches(configPath, result.Entities)
		if err != nil {
			postWarnings = append(postWarnings, fmt.Sprintf("mismatch analysis failed: %v", err))
		} else {
			report, err := generateSyncMismatchReport(configPath, resolvedURL, analysis)
			if err != nil {
				postWarnings = append(postWarnings, fmt.Sprintf("could not save mismatch report: %v", err))
			} else {
				postDetails["mismatch_report"] = report.ReportPath
				if !effectiveQuiet && !syncJSON {
					printSyncMismatchSummary(analysis)
				}
			}
		}
	}

	if !skipOrphans {
		orphanResult, err := syncer.FindOrphanedDevices()
		if err != nil {
			postWarnings = append(postWarnings, fmt.Sprintf("orphan detection failed: %v", err))
		} else {
			postDetails["orphan_report"] = orphanResult.ReportPath
		}
	}

	schemaPath, err := generateSyncSchema(configPath)
	if err != nil {
		postWarnings = append(postWarnings, fmt.Sprintf("could not generate entity schema: %v", err))
	} else if schemaPath != "" {
		postDetails["schema_path"] = schemaPath
	}

	postStatus := operator.StatusSuccess
	postSummary := "generated derived sync artifacts"
	if len(postWarnings) > 0 {
		postStatus = operator.StatusPartial
		postSummary = fmt.Sprintf("generated derived artifacts with %d warning(s)", len(postWarnings))
	}

	rt.AddStep(operator.Step{
		ID:      "post-sync-artifacts",
		Title:   "Generate mismatch, orphan, and editor artifacts",
		Status:  postStatus,
		Summary: postSummary,
		Details: postDetails,
		Hints:   postWarnings,
	})
	overall = operator.MergeStatus(overall, postStatus)

	if overall == operator.StatusPartial {
		rt.AddHint("Core sync completed, but some follow-up reports or derived artifacts need attention.")
	}

	return rt.Complete(overall, syncSummaryText(result, overall, len(postWarnings)+len(result.Warnings)))
}

func syncSummaryText(result *hasync.SyncResult, status operator.Status, warnings int) string {
	if result == nil {
		return "sync finished with no result summary"
	}

	if status == operator.StatusSuccess {
		return fmt.Sprintf("synced %d entities, %d devices, and %d areas", result.EntitiesCount, result.DevicesCount, result.AreasCount)
	}

	return fmt.Sprintf("synced core reference data with %d warning(s)", warnings)
}
