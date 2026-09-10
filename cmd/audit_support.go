package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
)

type auditExecutionOptions struct {
	Args         []string
	Pattern      string
	Verbose      bool
	TracesOnly   bool
	Window       time.Duration
	Tolerance    time.Duration
	PrintResults bool
}

type auditSummary struct {
	SpecsDiscovered int
	SpecsLoaded     int
	TraceOnly       bool
	TraceCount      int
	TriggerEvents   int
	Passed          int
	Idempotent      int
	Canceled        int
	Failed          int
	NoData          int
	Warnings        []string
}

func (s *auditSummary) Status() operator.Status {
	switch {
	case s.Failed > 0:
		return operator.StatusFailure
	case len(s.Warnings) > 0:
		return operator.StatusPartial
	case s.NoData > 0 && s.Passed+s.Idempotent+s.Canceled == 0:
		return operator.StatusWarning
	case s.NoData > 0:
		return operator.StatusPartial
	case s.TraceOnly && s.TraceCount == 0:
		return operator.StatusWarning
	case !s.TraceOnly && s.TriggerEvents == 0:
		return operator.StatusWarning
	default:
		return operator.StatusSuccess
	}
}

func (s *auditSummary) Step(id, title string) operator.Step {
	details := map[string]any{
		"specs_discovered": s.SpecsDiscovered,
		"specs_loaded":     s.SpecsLoaded,
		"trigger_events":   s.TriggerEvents,
		"passed":           s.Passed,
		"idempotent":       s.Idempotent,
		"canceled":         s.Canceled,
		"failed":           s.Failed,
		"no_data":          s.NoData,
		"trace_only":       s.TraceOnly,
		"trace_count":      s.TraceCount,
	}

	step := operator.Step{
		ID:      id,
		Title:   title,
		Status:  s.Status(),
		Summary: s.describe(),
		Details: details,
		Hints:   append([]string{}, s.Warnings...),
	}

	if step.Status == operator.StatusWarning {
		if s.NoData > 0 {
			step.Hints = append(step.Hints, "Trigger events were observed, but expectations could not be evaluated. Inspect spec coverage and required history for those events.")
		} else if s.TraceOnly {
			step.Hints = append(step.Hints, "Broaden the audit window or run `dm audit` without `--traces-only` for expectation-based results.")
		} else {
			step.Hints = append(step.Hints, "No production activity matched the current audit window. Run `dm audit --window 48h` or target a specific spec for deeper inspection.")
		}
	}
	if step.Status == operator.StatusPartial && s.NoData > 0 {
		step.Hints = append(step.Hints, "Some trigger events were not evaluated. Inspect spec coverage and required history before treating the audit as complete.")
	}

	return step
}

func (s *auditSummary) describe() string {
	if s.TraceOnly {
		return fmt.Sprintf("%d traces across %d loaded spec(s)", s.TraceCount, s.SpecsLoaded)
	}
	return fmt.Sprintf("%d passed, %d idempotent, %d canceled, %d failed, %d not evaluated across %d trigger events", s.Passed, s.Idempotent, s.Canceled, s.Failed, s.NoData, s.TriggerEvents)
}

func executeAudit(config *haconfig.HAConfig, opts auditExecutionOptions, errOut io.Writer) (*auditSummary, error) {
	specFiles, err := discoverAuditSpecsWithPattern(opts.Args, opts.Pattern)
	if err != nil {
		return nil, err
	}

	specs := make([]*haaudit.AuditSpec, 0, len(specFiles))
	for _, path := range specFiles {
		spec, err := haaudit.LoadAuditSpec(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", path, err)
		}
		specs = append(specs, spec)
	}

	summary := &auditSummary{
		SpecsDiscovered: len(specFiles),
		SpecsLoaded:     len(specs),
		TraceOnly:       opts.TracesOnly,
	}

	reportConfig := haaudit.ReportConfig{
		Verbose:   opts.Verbose,
		HAURL:     config.URL,
		Window:    opts.Window,
		Tolerance: opts.Tolerance,
	}

	warnf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		summary.Warnings = append(summary.Warnings, msg)
		if opts.PrintResults && errOut != nil {
			fmt.Fprintf(errOut, "Warning: %s\n", msg)
		}
	}

	if opts.TracesOnly {
		automationTraces, err := fetchTraceOnlyAuditData(config, specs, warnf)
		if err != nil {
			return nil, err
		}

		start := time.Now().Add(-opts.Window)
		for _, traces := range automationTraces {
			for _, trace := range traces {
				if !trace.Timestamp.Before(start) {
					summary.TraceCount++
				}
			}
		}

		if opts.PrintResults {
			haaudit.PrintTracesOnlyReport(automationTraces, reportConfig)
		}
		return summary, nil
	}

	results, err := runAuditCorrelation(config, specs, opts, warnf)
	if err != nil {
		return nil, err
	}

	for _, result := range results {
		summary.TriggerEvents += len(result.TriggerEvents)
		summary.Passed += result.Summary.Passed
		summary.Idempotent += result.Summary.PassedIdempotent
		summary.Canceled += result.Summary.Canceled
		summary.Failed += result.Summary.Failed
		summary.NoData += result.Summary.NoData
	}

	if opts.PrintResults {
		haaudit.PrintReport(results, reportConfig)
	}

	return summary, nil
}

func fetchTraceOnlyAuditData(config *haconfig.HAConfig, specs []*haaudit.AuditSpec, warnf func(string, ...any)) (map[string][]haaudit.AutomationTrace, error) {
	ws := hasync.NewWSClient(config.URL, config.Token)
	ws.SetContext(commandContext())
	if err := ws.ConnectContext(commandContext()); err != nil {
		return nil, fmt.Errorf("traces-only: WebSocket connect: %w", err)
	}
	defer ws.Close()

	resolver, err := haaudit.NewAutomationIDResolver(ws)
	if err != nil {
		return nil, fmt.Errorf("traces-only: could not build automation ID resolver: %w", err)
	}

	automationTraces := make(map[string][]haaudit.AutomationTrace)
	for _, spec := range specs {
		if spec.Automation == "" {
			continue
		}
		itemID := resolver.Resolve(spec.Automation)
		traces, err := haaudit.FetchTraceListWS(ws, itemID)
		if err != nil {
			warnf("could not fetch traces for %s: %v", spec.Automation, err)
			continue
		}
		automationTraces[spec.Automation] = traces
	}

	return automationTraces, nil
}

func runAuditCorrelation(config *haconfig.HAConfig, specs []*haaudit.AuditSpec, opts auditExecutionOptions, warnf func(string, ...any)) ([]*haaudit.AuditResult, error) {
	end := time.Now()
	start := end.Add(-opts.Window)

	var historySpecs, traceSpecs []*haaudit.AuditSpec
	seen := make(map[string]struct{})
	seenNoChange := make(map[string]struct{})
	var allEntityIDs []string
	var noChangeEntityIDs []string

	for _, spec := range specs {
		if spec.IsTraceBased() {
			traceSpecs = append(traceSpecs, spec)
		} else {
			historySpecs = append(historySpecs, spec)
		}
		for _, id := range spec.AllEntityIDs() {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			allEntityIDs = append(allEntityIDs, id)
		}
		for _, id := range spec.NoChangeEntityIDs() {
			if _, ok := seenNoChange[id]; ok {
				continue
			}
			seenNoChange[id] = struct{}{}
			noChangeEntityIDs = append(noChangeEntityIDs, id)
		}
	}

	var history haaudit.EntityTimeline
	var err error
	if len(allEntityIDs) > 0 {
		history, err = haaudit.FetchHistoryContext(commandContext(), config.URL, config.Token, allEntityIDs, start, end)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch history: %w", err)
		}
		if len(noChangeEntityIDs) > 0 {
			if err := haaudit.EnrichHistoryContextsContext(commandContext(), config.URL, config.Token, history, noChangeEntityIDs, start, end); err != nil {
				warnf("could not attribute no-change target history: %v", err)
			}
		}
	}

	results := make([]*haaudit.AuditResult, 0, len(specs))
	for _, spec := range historySpecs {
		results = append(results, haaudit.Correlate(spec, history, spec.EffectiveTolerance(opts.Tolerance)))
	}

	traceWS := hasync.NewWSClient(config.URL, config.Token)
	traceWS.SetContext(commandContext())
	traceWSConnected := false
	var traceResolver *haaudit.AutomationIDResolver
	if err := traceWS.ConnectContext(commandContext()); err != nil {
		warnf("could not connect for traces: %v", err)
	} else {
		defer traceWS.Close()
		traceWSConnected = true

		traceResolver, err = haaudit.NewAutomationIDResolver(traceWS)
		if err != nil {
			warnf("could not build automation ID resolver: %v", err)
		}
	}

	if len(traceSpecs) > 0 && traceWSConnected && traceResolver != nil {
		for _, spec := range traceSpecs {
			itemID := traceResolver.Resolve(spec.Automation)
			traces, fetchErr := haaudit.FetchTraceListWS(traceWS, itemID)
			if fetchErr != nil {
				warnf("could not fetch traces for %s: %v", spec.Automation, fetchErr)
				continue
			}
			results = append(results, haaudit.CorrelateFromTraces(spec, traces, history, spec.EffectiveTolerance(opts.Tolerance), start, end))
		}
	} else if len(traceSpecs) > 0 {
		warnf("skipped %d trace-based audit spec(s) because trace resolution was unavailable", len(traceSpecs))
	}

	haaudit.ResolveIdempotent(results, history)

	if traceWSConnected && traceResolver != nil {
		haaudit.EnrichWithTraces(results, traceWS, traceResolver)
	}

	return results, nil
}

func discoverAuditSpecsWithPattern(args []string, pattern string) ([]string, error) {
	original := auditPattern
	auditPattern = pattern
	defer func() { auditPattern = original }()
	return discoverAuditSpecs(args)
}

func auditSummaryText(summary *auditSummary) string {
	if summary == nil {
		return "audit finished with no summary"
	}

	switch summary.Status() {
	case operator.StatusSuccess:
		if summary.TraceOnly {
			return fmt.Sprintf("trace audit passed: %d traces across %d spec(s)", summary.TraceCount, summary.SpecsLoaded)
		}
		return fmt.Sprintf("audit passed: %d passed, %d idempotent, %d canceled across %d trigger events", summary.Passed, summary.Idempotent, summary.Canceled, summary.TriggerEvents)
	case operator.StatusWarning:
		if summary.NoData > 0 {
			return "audit completed without evaluated expectations: " + summary.describe()
		}
		if summary.TraceOnly {
			return "trace audit completed, but no traces were found in the selected window"
		}
		return "audit completed, but no production activity matched the selected window"
	case operator.StatusFailure:
		return "audit failed: " + summary.describe()
	default:
		parts := []string{summary.describe()}
		if len(summary.Warnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d warning(s)", len(summary.Warnings)))
		}
		return "audit completed with follow-up needed: " + strings.Join(parts, "; ")
	}
}
