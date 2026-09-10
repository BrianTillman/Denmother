package haaudit

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
)

// ReportConfig holds configuration for report output.
type ReportConfig struct {
	Verbose   bool
	HAURL     string
	Window    time.Duration
	Tolerance time.Duration
}

// PrintReport writes the audit report to stdout.
func PrintReport(results []*AuditResult, config ReportConfig) {
	printReportTo(os.Stdout, results, config)
}

func printReportTo(w io.Writer, results []*AuditResult, config ReportConfig) {
	now := time.Now()
	start := now.Add(-config.Window)

	fmt.Fprintln(w)
	color.New(color.FgCyan, color.Bold).Fprintln(w, "Production Automation Audit")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Home Assistant: %s\n", config.HAURL)
	fmt.Fprintf(w, "Window: %s → %s (%s)\n",
		start.Format("2006-01-02 15:04"),
		now.Format("2006-01-02 15:04"),
		formatDuration(config.Window))
	fmt.Fprintf(w, "Tolerance: %s\n", formatDuration(config.Tolerance))
	fmt.Fprintln(w)

	for _, result := range results {
		if config.Verbose {
			printVerboseResult(w, result, config)
		} else {
			printNormalResult(w, result)
		}
	}

	printSummary(w, results)
}

func printNormalResult(w io.Writer, result *AuditResult) {
	count := len(result.TriggerEvents)
	label := "triggers"
	if result.TriggerEntity == "" {
		label = "traces"
	}
	if result.Summary.Failed > 0 {
		color.New(color.FgRed).Fprintf(w, "%-50s %3d %s, %3d passed, %3d canceled, %3d failed\n",
			result.SpecName, count, label, result.Summary.Passed, result.Summary.Canceled, result.Summary.Failed)
	} else {
		fmt.Fprintf(w, "%-50s %3d %s, %3d passed, %3d canceled\n", result.SpecName, count, label, result.Summary.Passed, result.Summary.Canceled)
	}
}

func printVerboseResult(w io.Writer, result *AuditResult, config ReportConfig) {
	color.New(color.FgCyan, color.Bold).Fprintln(w, result.SpecName)
	if result.Tolerance != 0 && result.Tolerance != config.Tolerance {
		fmt.Fprintf(w, "  Tolerance: %s (spec override)\n", formatDuration(result.Tolerance))
	}

	if len(result.TriggerEvents) == 0 {
		noDataLabel := "No trigger events in window"
		if result.TriggerEntity == "" {
			noDataLabel = "No traces in window"
		}
		color.New(color.FgYellow).Fprintf(w, "  ○ %s\n", noDataLabel)
		fmt.Fprintln(w)
		return
	}

	triggerDisplay := "trace"
	if result.TriggerEntity != "" {
		triggerDisplay = formatEntityName(result.TriggerEntity) + "→" + result.TriggerTo
	}

	for _, ev := range result.TriggerEvents {
		printEventLine(w, triggerDisplay, ev)
	}
	fmt.Fprintln(w)
}

func printEventLine(w io.Writer, triggerDisplay string, ev TriggerEventResult) {
	ts := ev.Timestamp.Format("15:04:05")

	vibeCtx := ""
	if ev.ActiveVibe != "" {
		vibeCtx = fmt.Sprintf(" (vibe: %s)", formatVibeName(ev.ActiveVibe))
	}

	if ev.Outcome == nil {
		// Trigger occurred but no expectation is configured for this vibe.
		color.New(color.FgYellow).Fprintf(w, "  ○ %s %s%s → no expectation configured\n",
			ts, triggerDisplay, vibeCtx)
		return
	}

	switch ev.Outcome.Status {
	case OutcomePass:
		detail := formatResultDetail(ev.Expectation, ev.Outcome)
		color.New(color.FgGreen).Fprintf(w, "  ✓ %s %s%s → %s [+%.1fs]\n",
			ts, triggerDisplay, vibeCtx, detail, ev.Outcome.Delay.Seconds())
	case OutcomePassIdempotent:
		detail := formatResultDetail(ev.Expectation, ev.Outcome)
		color.New(color.FgYellow).Fprintf(w, "  ~ %s %s%s → %s [IDEMPOTENT]\n",
			ts, triggerDisplay, vibeCtx, detail)
	case OutcomeCanceled:
		color.New(color.FgYellow).Fprintf(w, "  ~ %s %s%s → canceled after %.1fs [%s]\n",
			ts, triggerDisplay, vibeCtx, ev.Outcome.Delay.Seconds(), ev.Outcome.Details)
	case OutcomeMissing:
		expected := formatExpectedDetail(ev.Expectation)
		color.New(color.FgRed).Fprintf(w, "  ✗ %s %s%s → %s [MISSING]\n",
			ts, triggerDisplay, vibeCtx, expected)
	case OutcomeWrongState:
		expected := ""
		if ev.Expectation != nil {
			if ev.Expectation.NoChange {
				expected = "(expected no state change)"
			} else {
				expected = fmt.Sprintf("(expected %s)", ev.Expectation.State)
			}
		}
		color.New(color.FgRed).Fprintf(w, "  ✗ %s %s%s → %s %s [WRONG_STATE]\n",
			ts, triggerDisplay, vibeCtx, ev.Outcome.ActualState, expected)
	case OutcomeWrongBrightness:
		actual := formatActualDetail(ev.Outcome)
		expected := formatExpectedDetail(ev.Expectation)
		color.New(color.FgRed).Fprintf(w, "  ✗ %s %s%s → %s (%s) [+%.1fs]\n",
			ts, triggerDisplay, vibeCtx, actual, expected, ev.Outcome.Delay.Seconds())
	case OutcomeTraceError:
		color.New(color.FgRed).Fprintf(w, "  ✗ %s %s%s → %s [TRACE_ERROR]\n",
			ts, triggerDisplay, vibeCtx, ev.Outcome.Details)
	}
	printTraceAnnotation(w, ev.Trace)
}

// shortRunID returns the first 8 characters of a run ID for compact display.
func shortRunID(runID string) string {
	if len(runID) > 8 {
		return runID[:8]
	}
	return runID
}

// printTraceAnnotation prints a dim trace detail line when a matching trace exists.
func printTraceAnnotation(w io.Writer, trace *TraceAnnotation) {
	if trace == nil {
		return
	}
	lagSign := "+"
	lag := trace.Lag
	if lag < 0 {
		lagSign = "-"
		lag = -lag
	}
	detail := trace.State
	if trace.LastStep != "" {
		detail += " @ " + trace.LastStep
	}
	if trace.Error != "" {
		detail += " err=" + trace.Error
	}
	color.New(color.FgHiBlack).Fprintf(w, "       trace: %s [%s%.1fs]  run=%s\n",
		detail, lagSign, lag.Seconds(), shortRunID(trace.RunID))
}

func printSummary(w io.Writer, results []*AuditResult) {
	totalTriggers := 0
	totalPassed := 0
	totalIdempotent := 0
	totalCanceled := 0
	totalFailed := 0
	totalNoData := 0

	for _, r := range results {
		totalTriggers += len(r.TriggerEvents)
		totalPassed += r.Summary.Passed
		totalIdempotent += r.Summary.PassedIdempotent
		totalCanceled += r.Summary.Canceled
		totalFailed += r.Summary.Failed
		totalNoData += r.Summary.NoData
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d audits, %d trigger events\n", len(results), totalTriggers)
	if totalPassed > 0 {
		color.New(color.FgGreen).Fprintf(w, "  Passed: %d\n", totalPassed)
	} else {
		fmt.Fprintf(w, "  Passed: %d\n", totalPassed)
	}
	if totalIdempotent > 0 {
		color.New(color.FgYellow).Fprintf(w, "  Idempotent: %d\n", totalIdempotent)
	} else {
		fmt.Fprintf(w, "  Idempotent: %d\n", totalIdempotent)
	}
	if totalCanceled > 0 {
		color.New(color.FgYellow).Fprintf(w, "  Canceled: %d\n", totalCanceled)
	} else {
		fmt.Fprintf(w, "  Canceled: %d\n", totalCanceled)
	}
	if totalFailed > 0 {
		color.New(color.FgRed).Fprintf(w, "  Failed: %d\n", totalFailed)
	} else {
		fmt.Fprintf(w, "  Failed: %d\n", totalFailed)
	}
	fmt.Fprintf(w, "  No data: %d\n", totalNoData)
	fmt.Fprintln(w)

	if totalFailed > 0 {
		color.New(color.FgRed, color.Bold).Fprintln(w, "AUDIT FAILED")
	} else if totalTriggers > 0 {
		color.New(color.FgGreen, color.Bold).Fprintln(w, "ALL AUDITS PASSED")
	} else {
		color.New(color.FgYellow).Fprintln(w, "NO DATA")
	}
	fmt.Fprintln(w)
}

// PrintTracesOnlyReport prints a standalone report of recent automation traces.
func PrintTracesOnlyReport(automationTraces map[string][]AutomationTrace, config ReportConfig) {
	printTracesOnlyReportTo(os.Stdout, automationTraces, config)
}

func printTracesOnlyReportTo(w io.Writer, automationTraces map[string][]AutomationTrace, config ReportConfig) {
	now := time.Now()
	start := now.Add(-config.Window)

	fmt.Fprintln(w)
	color.New(color.FgCyan, color.Bold).Fprintln(w, "Automation Trace Report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Home Assistant: %s\n", config.HAURL)
	fmt.Fprintf(w, "Window: %s → %s (%s)\n",
		start.Format("2006-01-02 15:04"),
		now.Format("2006-01-02 15:04"),
		formatDuration(config.Window))
	fmt.Fprintln(w)

	if len(automationTraces) == 0 {
		color.New(color.FgYellow).Fprintln(w, "No traces found.")
		fmt.Fprintln(w)
		return
	}

	for automationID, traces := range automationTraces {
		color.New(color.FgCyan, color.Bold).Fprintln(w, automationID)
		if len(traces) == 0 {
			color.New(color.FgYellow).Fprintln(w, "  ○ No traces")
			fmt.Fprintln(w)
			continue
		}
		printed := 0
		for _, tr := range traces {
			if tr.Timestamp.Before(start) {
				continue
			}
			ts := tr.Timestamp.Format("2006-01-02 15:04:05")
			if tr.State == "stopped" {
				color.New(color.FgGreen).Fprintf(w, "  ✓ %s  stopped", ts)
			} else {
				color.New(color.FgRed).Fprintf(w, "  ✗ %s  %-10s", ts, tr.State)
			}
			if tr.LastStep != "" {
				fmt.Fprintf(w, "  @ %-20s", tr.LastStep)
			}
			if tr.Error != "" {
				color.New(color.FgRed).Fprintf(w, "  err=%s", tr.Error)
			}
			fmt.Fprintf(w, "  run=%s\n", shortRunID(tr.RunID))
			printed++
		}
		if printed == 0 {
			color.New(color.FgYellow).Fprintln(w, "  ○ No traces in window")
		}
		fmt.Fprintln(w)
	}
}

// formatVibeName extracts the short vibe name from an entity ID.
func formatVibeName(entityID string) string {
	const prefix = "input_boolean.vibe_"
	if strings.HasPrefix(entityID, prefix) {
		return entityID[len(prefix):]
	}
	if i := strings.LastIndex(entityID, "."); i >= 0 {
		return entityID[i+1:]
	}
	return entityID
}

// formatEntityName returns a short display name for an entity ID.
// For example, "binary_sensor.example_occupancy" becomes "occupancy".
func formatEntityName(entityID string) string {
	objID := entityID
	if i := strings.Index(entityID, "."); i >= 0 {
		objID = entityID[i+1:]
	}
	if i := strings.LastIndex(objID, "_"); i >= 0 {
		return objID[i+1:]
	}
	return objID
}

// formatDuration uses hours or minutes for exact multiples, otherwise Duration.String.
func formatDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

// formatResultDetail formats the outcome detail line for a PASS result.
func formatResultDetail(exp *Expectation, outcome *OutcomeResult) string {
	if exp == nil {
		return outcome.ActualState
	}
	if exp.NoChange {
		return "no state change"
	}
	if brightnessExpr, ok := exp.Attributes["brightness"]; ok {
		actual := formatBrightnessValue(outcome.ActualAttrs)
		expected := formatAttributeExpr(brightnessExpr)
		return fmt.Sprintf("brightness %s (expected %s)", actual, expected)
	}
	return outcome.ActualState
}

// formatExpectedDetail formats the expected value for MISSING/fail lines.
func formatExpectedDetail(exp *Expectation) string {
	if exp == nil {
		return "unknown"
	}
	if exp.NoChange {
		return "no state change"
	}
	if brightnessExpr, ok := exp.Attributes["brightness"]; ok {
		expected := formatAttributeExpr(brightnessExpr)
		return fmt.Sprintf("brightness - (expected %s)", expected)
	}
	return fmt.Sprintf("state %s", exp.State)
}

// formatActualDetail formats the actual value for a WRONG_BRIGHTNESS line.
func formatActualDetail(outcome *OutcomeResult) string {
	if actual := formatBrightnessValue(outcome.ActualAttrs); actual != "" {
		return fmt.Sprintf("brightness %s", actual)
	}
	return outcome.ActualState
}

// formatBrightnessValue extracts and formats brightness from an attributes map.
func formatBrightnessValue(attrs map[string]interface{}) string {
	if v, ok := attrs["brightness"]; ok {
		if f, ok := attributeAsFloat(v); ok {
			return fmt.Sprintf("%.0f", f)
		}
	}
	return "0"
}

// formatAttributeExpr formats an attribute comparison expression for display.
// E.g., ">= 199" → "≥199", "<= 255" → "≤255", "== 204" → "204"
func formatAttributeExpr(expr string) string {
	op, val, err := ParseAttributeComparison(expr)
	if err != nil {
		return strings.TrimSpace(expr)
	}
	valStr := fmt.Sprintf("%g", val)
	switch op {
	case ">=":
		return "≥" + valStr
	case "<=":
		return "≤" + valStr
	case "==":
		return valStr
	default:
		return op + valStr
	}
}
