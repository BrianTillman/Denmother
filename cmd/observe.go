package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	observeProdURL       string
	observeProdToken     string
	observeDevURL        string
	observeDevToken      string
	observeHAURL         string
	observeHAToken       string
	observeJSON          bool
	observeRunID         string
	observeWindow        time.Duration
	observeLimit         int
	observeErrorLogLimit int
)

var observeCmd = &cobra.Command{
	Use:   "observe <automation-entity-id>",
	Short: "Correlate read-only traces, logs, and local config for one automation",
	Long: `Correlate Home Assistant runtime observations for one automation.

The command is read-only. It fetches recent automation traces, logbook entries,
error-log lines, and local config/test references so agents can debug behavior
without manually stitching together multiple commands.`,
	Args: cobra.ExactArgs(1),
	RunE: runObserve,
}

type observeSummary struct {
	Automation     string             `json:"automation"`
	Window         string             `json:"window"`
	ConfigRefs     []observeConfigRef `json:"config_refs,omitempty"`
	Traces         []traceListResult  `json:"traces,omitempty"`
	FullTrace      json.RawMessage    `json:"full_trace,omitempty"`
	LogbookEntries []map[string]any   `json:"logbook_entries,omitempty"`
	ErrorLogLines  []string           `json:"error_log_lines,omitempty"`
	ErrorLogSource string             `json:"error_log_source,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
	NextCommands   []string           `json:"next_commands,omitempty"`
}

type observeConfigRef struct {
	Path      string `json:"path"`
	Test      string `json:"test,omitempty"`
	ID        string `json:"id,omitempty"`
	Alias     string `json:"alias,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Blueprint string `json:"blueprint,omitempty"`
}

func init() {
	observeCmd.Flags().StringVar(&observeDevURL, "dev-url", "", "Development Home Assistant URL")
	observeCmd.Flags().StringVar(&observeDevToken, "dev-token", "", "Development Home Assistant token")
	observeCmd.Flags().StringVar(&observeProdURL, "prod-url", "", "Production Home Assistant URL")
	observeCmd.Flags().StringVar(&observeProdToken, "prod-token", "", "Production Home Assistant token")
	observeCmd.Flags().StringVar(&observeHAURL, "ha-url", "", "Home Assistant URL (legacy, profile-specific)")
	observeCmd.Flags().StringVar(&observeHAToken, "ha-token", "", "Home Assistant token (legacy, profile-specific)")
	observeCmd.Flags().BoolVar(&observeJSON, "json", false, "Emit a machine-readable JSON summary")
	observeCmd.Flags().StringVar(&observeRunID, "run-id", "", "Fetch a full trace for the given run ID")
	observeCmd.Flags().DurationVar(&observeWindow, "window", 24*time.Hour, "Lookback window for traces and logbook entries")
	observeCmd.Flags().IntVar(&observeLimit, "limit", 10, "Maximum trace and logbook entries to return")
	observeCmd.Flags().IntVar(&observeErrorLogLimit, "error-log-limit", 20, "Maximum error-log lines to return")

	rootCmd.AddCommand(observeCmd)
}

func runObserve(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("observe", observeJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     observeProdURL,
		ProdToken:   observeProdToken,
		DevURL:      observeDevURL,
		DevToken:    observeDevToken,
		LegacyURL:   observeHAURL,
		LegacyToken: observeHAToken,
	}
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceDev, flags, operator.ModeReadOnly, true)
	if resolved != nil {
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
		return rt.Complete(operator.StatusFailure, "observation could not start")
	}

	summary := buildObserveSummary(resolved.Config, args[0])
	status := operator.StatusSuccess
	if len(summary.Warnings) > 0 {
		status = operator.StatusPartial
	}
	if len(summary.Traces) == 0 && len(summary.LogbookEntries) == 0 && len(summary.ErrorLogLines) == 0 {
		status = operator.MergeStatus(status, operator.StatusWarning)
		summary.Warnings = append(summary.Warnings, "no trace, logbook, or error-log observations matched the selected automation and window")
	}

	step := operator.Step{
		ID:           "observe-automation",
		Title:        "Observe automation runtime",
		Status:       status,
		Summary:      observeSummaryText(summary),
		Artifacts:    observeArtifacts(summary),
		NextCommands: summary.NextCommands,
		Details: map[string]any{
			"observe": summary,
		},
		Hints: summary.Warnings,
	}
	rt.AddStep(step)

	if !observeJSON {
		printObserveSummary(cmd.OutOrStdout(), summary)
	}

	return rt.Complete(status, step.Summary)
}

func buildObserveSummary(config *haconfig.HAConfig, automation string) observeSummary {
	if observeLimit < 1 {
		observeLimit = 1
	}
	if observeErrorLogLimit < 1 {
		observeErrorLogLimit = 1
	}

	objectID := automationObjectID(automation)
	summary := observeSummary{
		Automation: automation,
		Window:     observeWindow.String(),
		ConfigRefs: observeConfigRefs(automation),
		NextCommands: []string{
			fmt.Sprintf("./dm trace %s --json", automation),
			fmt.Sprintf("./dm logs --contains %s --json", objectID),
			fmt.Sprintf("./dm observe %s --json", automation),
		},
	}

	traceSummary, err := inspectAutomationTrace(config, automation, observeRunID, observeWindow, observeLimit)
	if err != nil {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("trace inspection failed: %v", err))
	} else {
		summary.Traces = traceSummary.Traces
		summary.FullTrace = traceSummary.FullTrace
		summary.Warnings = append(summary.Warnings, traceSummary.Warnings...)
	}

	logbook, truncated, err := fetchHALogbook(config, "", objectID, observeWindow, observeLimit)
	if err != nil {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("logbook inspection failed: %v", err))
	} else {
		summary.LogbookEntries = logbook
		if truncated {
			summary.Warnings = append(summary.Warnings, "logbook entries were truncated by --limit")
		}
	}

	errorLog, err := fetchHAErrorLog(config, objectID, observeErrorLogLimit)
	if err != nil {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("error-log inspection failed: %v", err))
	} else {
		summary.ErrorLogLines = errorLog.Lines
		summary.ErrorLogSource = errorLog.Source
		summary.Warnings = append(summary.Warnings, errorLog.Warnings...)
		if errorLog.Truncated {
			summary.Warnings = append(summary.Warnings, "error-log lines were truncated by --error-log-limit")
		}
	}

	return summary
}

func observeConfigRefs(automation string) []observeConfigRef {
	rows, err := collectAutomationDocs()
	if err != nil {
		return nil
	}
	objectID := automationObjectID(automation)
	var refs []observeConfigRef
	for _, row := range rows {
		if !automationRowMatches(row, automation, objectID) {
			continue
		}
		refs = append(refs, observeConfigRef{
			Path:      row.Path,
			Test:      row.Test,
			ID:        row.ID,
			Alias:     row.Alias,
			Mode:      row.Mode,
			Blueprint: row.Blueprint,
		})
	}
	return refs
}

func automationRowMatches(row automationDocRow, automation string, objectID string) bool {
	if row.ID == automation || row.ID == objectID {
		return true
	}
	if strings.TrimSuffix(filepath.Base(row.Path), ".yaml") == objectID {
		return true
	}
	normalizedAlias := strings.ReplaceAll(strings.ToLower(row.Alias), " ", "_")
	return normalizedAlias == objectID
}

func automationObjectID(automation string) string {
	return strings.TrimPrefix(automation, "automation.")
}

func observeArtifacts(summary observeSummary) []string {
	seen := map[string]bool{}
	var artifacts []string
	for _, ref := range summary.ConfigRefs {
		for _, path := range []string{ref.Path, ref.Test} {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			artifacts = append(artifacts, path)
		}
	}
	return artifacts
}

func observeSummaryText(summary observeSummary) string {
	return fmt.Sprintf(
		"observed %s: %d trace(s), %d logbook entry(s), %d error-log line(s)",
		summary.Automation,
		len(summary.Traces),
		len(summary.LogbookEntries),
		len(summary.ErrorLogLines),
	)
}

func printObserveSummary(w io.Writer, summary observeSummary) {
	fmt.Fprintf(w, "\nObservation for %s\n", summary.Automation)
	fmt.Fprintf(w, "  Config refs: %d\n", len(summary.ConfigRefs))
	fmt.Fprintf(w, "  Traces: %d\n", len(summary.Traces))
	fmt.Fprintf(w, "  Logbook entries: %d\n", len(summary.LogbookEntries))
	fmt.Fprintf(w, "  Error log lines: %d\n", len(summary.ErrorLogLines))
	for _, warning := range summary.Warnings {
		fmt.Fprintf(w, "  Warning: %s\n", warning)
	}
}
