package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	traceProdURL   string
	traceProdToken string
	traceDevURL    string
	traceDevToken  string
	traceHAURL     string
	traceHAToken   string
	traceJSON      bool
	traceRunID     string
	traceWindow    time.Duration
	traceLimit     int
)

var traceCmd = &cobra.Command{
	Use:   "trace <automation-entity-id>",
	Short: "Inspect Home Assistant automation traces",
	Long: `Inspect recent Home Assistant automation traces through the read-only
WebSocket trace API. The command defaults to the development target and can be
pointed at production with --prod-url/--prod-token or production environment
variables.`,
	Args: cobra.ExactArgs(1),
	RunE: runTrace,
}

type traceSummary struct {
	Automation string            `json:"automation"`
	Traces     []traceListResult `json:"traces"`
	FullTrace  json.RawMessage   `json:"full_trace,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
}

type traceListResult struct {
	RunID     string `json:"run_id"`
	Timestamp string `json:"timestamp"`
	State     string `json:"state"`
	LastStep  string `json:"last_step,omitempty"`
	Error     string `json:"error,omitempty"`
}

func init() {
	traceCmd.Flags().StringVar(&traceDevURL, "dev-url", "", "Development Home Assistant URL")
	traceCmd.Flags().StringVar(&traceDevToken, "dev-token", "", "Development Home Assistant token")
	traceCmd.Flags().StringVar(&traceProdURL, "prod-url", "", "Production Home Assistant URL")
	traceCmd.Flags().StringVar(&traceProdToken, "prod-token", "", "Production Home Assistant token")
	traceCmd.Flags().StringVar(&traceHAURL, "ha-url", "", "Home Assistant URL (legacy, profile-specific)")
	traceCmd.Flags().StringVar(&traceHAToken, "ha-token", "", "Home Assistant token (legacy, profile-specific)")
	traceCmd.Flags().BoolVar(&traceJSON, "json", false, "Emit a machine-readable JSON summary")
	traceCmd.Flags().StringVar(&traceRunID, "run-id", "", "Fetch a full trace for the given run ID")
	traceCmd.Flags().DurationVar(&traceWindow, "window", 24*time.Hour, "Only report traces newer than this lookback window")
	traceCmd.Flags().IntVar(&traceLimit, "limit", 10, "Maximum trace list entries to return")

	rootCmd.AddCommand(traceCmd)
}

func runTrace(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("trace", traceJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     traceProdURL,
		ProdToken:   traceProdToken,
		DevURL:      traceDevURL,
		DevToken:    traceDevToken,
		LegacyURL:   traceHAURL,
		LegacyToken: traceHAToken,
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
		return rt.Complete(operator.StatusFailure, "trace inspection could not start")
	}

	summary, inspectErr := inspectAutomationTrace(resolved.Config, args[0], traceRunID, traceWindow, traceLimit)
	if inspectErr != nil {
		rt.AddStep(operator.Step{
			ID:      "inspect-trace",
			Title:   "Inspect automation trace",
			Status:  operator.StatusFailure,
			Summary: inspectErr.Error(),
			Details: map[string]any{
				"automation": args[0],
				"next_commands": []string{
					fmt.Sprintf("./dm trace %s --json", args[0]),
				},
			},
		})
		return rt.Complete(operator.StatusFailure, "trace inspection failed")
	}

	status := operator.StatusSuccess
	if len(summary.Traces) == 0 {
		status = operator.StatusWarning
	}
	step := operator.Step{
		ID:      "inspect-trace",
		Title:   "Inspect automation trace",
		Status:  status,
		Summary: fmt.Sprintf("%d trace(s) found for %s", len(summary.Traces), summary.Automation),
		Details: map[string]any{
			"automation":    summary.Automation,
			"traces":        summary.Traces,
			"trace_count":   len(summary.Traces),
			"window":        traceWindow.String(),
			"next_commands": []string{fmt.Sprintf("./dm trace %s --json", summary.Automation)},
		},
	}
	if len(summary.FullTrace) > 0 {
		step.Details["full_trace"] = json.RawMessage(summary.FullTrace)
	}
	if len(summary.Warnings) > 0 {
		step.Hints = append(step.Hints, summary.Warnings...)
	}
	if status == operator.StatusWarning {
		step.Hints = append(step.Hints, "Trigger the automation or broaden --window, then rerun the trace command.")
	}
	rt.AddStep(step)

	if !traceJSON {
		printTraceSummary(cmd.OutOrStdout(), summary)
	}

	return rt.Complete(status, traceSummaryText(status, summary))
}

func inspectAutomationTrace(config *haconfig.HAConfig, automationID, runID string, window time.Duration, limit int) (*traceSummary, error) {
	if limit < 1 {
		limit = 1
	}
	ws := hasync.NewWSClient(config.URL, config.Token)
	ws.SetContext(commandContext())
	if err := ws.ConnectContext(commandContext()); err != nil {
		return nil, fmt.Errorf("WebSocket connect: %w", err)
	}
	defer ws.Close()

	resolver, err := haaudit.NewAutomationIDResolver(ws)
	if err != nil {
		return nil, fmt.Errorf("could not build automation ID resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)

	traces, err := haaudit.FetchTraceListWS(ws, itemID)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-window)
	summary := &traceSummary{Automation: automationID}
	for _, trace := range traces {
		if !trace.Timestamp.IsZero() && trace.Timestamp.Before(cutoff) {
			continue
		}
		summary.Traces = append(summary.Traces, traceListResult{
			RunID:     trace.RunID,
			Timestamp: trace.Timestamp.Format(time.RFC3339Nano),
			State:     trace.State,
			LastStep:  trace.LastStep,
			Error:     trace.Error,
		})
		if len(summary.Traces) >= limit {
			break
		}
	}

	if runID != "" {
		raw, err := ws.SendCommandWithParams("trace/get", map[string]interface{}{
			"domain":  "automation",
			"item_id": itemID,
			"run_id":  runID,
		})
		if err != nil {
			return nil, fmt.Errorf("trace/get %s run=%s: %w", itemID, runID, err)
		}
		summary.FullTrace = raw
	}

	return summary, nil
}

func printTraceSummary(w io.Writer, summary *traceSummary) {
	fmt.Fprintf(w, "\nAutomation traces for %s\n", summary.Automation)
	if len(summary.Traces) == 0 {
		fmt.Fprintln(w, "  No traces in selected window.")
		return
	}
	for _, trace := range summary.Traces {
		fmt.Fprintf(w, "  %s  %-10s run=%s", trace.Timestamp, trace.State, trace.RunID)
		if trace.LastStep != "" {
			fmt.Fprintf(w, " @ %s", trace.LastStep)
		}
		if trace.Error != "" {
			fmt.Fprintf(w, " err=%s", trace.Error)
		}
		fmt.Fprintln(w)
	}
	if len(summary.FullTrace) > 0 {
		fmt.Fprintln(w, "  Full trace included in JSON output.")
	}
}

func traceSummaryText(status operator.Status, summary *traceSummary) string {
	if status == operator.StatusWarning {
		return fmt.Sprintf("no traces found for %s", summary.Automation)
	}
	return fmt.Sprintf("trace inspection found %d trace(s) for %s", len(summary.Traces), summary.Automation)
}
