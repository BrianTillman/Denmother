package hatest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/hasync"
)

const (
	// TracePollInterval is how often to poll trace/list for a matching trace
	TracePollInterval = 200 * time.Millisecond
	// TracePollTimeout is the total time to wait for a trace to appear
	TracePollTimeout = 3 * time.Second
	// TraceClockSkew is the backward window on trigger timestamp for clock skew
	TraceClockSkew = 1 * time.Second
)

// TraceResult holds the outcome of trace validation for a single test case.
type TraceResult struct {
	AutomationID string
	RunID        string
	State        string // "stopped", "running", "aborted"
	LastStep     string
	Error        string        // Non-empty if trace recorded an error
	FetchLatency time.Duration // Time spent polling for the trace
	Assertions   []TraceAssertionResult
}

// TraceAssertionResult holds the outcome of a single trace assertion.
type TraceAssertionResult struct {
	Name   string // e.g., "expect_branch: occupied"
	Passed bool
	Detail string // Human-readable explanation
}

// FullTrace is the parsed response from the trace/get WebSocket command.
type FullTrace struct {
	RunID           string                 `json:"run_id"`
	State           string                 `json:"state"`
	Timestamp       haaudit.TraceTimestamp `json:"timestamp"`
	ScriptExecution string                 `json:"script_execution,omitempty"`
	Trace           map[string][]TraceNode `json:"trace"`
	Context         TraceContext           `json:"context"`
	Error           string                 `json:"error"`
}

// TraceNode is a single execution node within a full trace.
type TraceNode struct {
	Path      string                 `json:"path"`
	Timestamp string                 `json:"timestamp"`
	Changed   map[string]interface{} `json:"changed_variables,omitempty"`
	Result    *TraceActionResult     `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// TraceActionResult holds the result of a service call action in a trace.
type TraceActionResult struct {
	Params  map[string]interface{} `json:"params"`
	Running interface{}            `json:"running_script,omitempty"`
}

// TraceContext holds the automation execution context.
type TraceContext struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	UserID   string `json:"user_id"`
}

type fullTraceFetcher func(runID string) (*FullTrace, error)

func (r *TestRunner) snapshotTraceRunIDs(automationID string) (map[string]struct{}, error) {
	ws, err := r.getWSClient()
	if err != nil {
		return nil, fmt.Errorf("could not connect WebSocket for traces: %w", err)
	}
	resolver, err := r.getResolver()
	if err != nil {
		return nil, fmt.Errorf("could not build automation ID resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)
	traces, err := haaudit.FetchTraceListWS(ws, itemID)
	if err != nil {
		return nil, fmt.Errorf("trace/list %s: %w", automationID, err)
	}
	return traceRunIDSet(traces), nil
}

func (r *TestRunner) assertNoTrace(automationID string, baseline map[string]struct{}, contexts ...string) error {
	ws, err := r.getWSClient()
	if err != nil {
		return fmt.Errorf("could not connect WebSocket for traces: %w", err)
	}
	resolver, err := r.getResolver()
	if err != nil {
		return fmt.Errorf("could not build automation ID resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)
	deadline := time.Now().Add(TracePollTimeout)

	for time.Now().Before(deadline) {
		traces, err := haaudit.FetchTraceListWS(ws, itemID)
		if err != nil {
			return fmt.Errorf("trace/list %s: %w", automationID, err)
		}
		var unexpected *haaudit.AutomationTrace
		if len(contexts) > 0 {
			unexpected, err = r.unexpectedAttributedTrace(traces, baseline, contexts, automationID)
			if err != nil {
				return err
			}
		} else {
			unexpected = unexpectedTraceNotInBaseline(traces, baseline)
		}
		if unexpected != nil {
			return fmt.Errorf("unexpected trace %s created for %s at %s", unexpected.RunID, automationID, unexpected.Timestamp.Format(time.RFC3339Nano))
		}
		if err := r.sleep(TracePollInterval); err != nil {
			return err
		}
	}
	return nil
}

func traceRunIDSet(traces []haaudit.AutomationTrace) map[string]struct{} {
	runIDs := make(map[string]struct{}, len(traces))
	for _, trace := range traces {
		runIDs[trace.RunID] = struct{}{}
	}
	return runIDs
}

func unexpectedTraceNotInBaseline(traces []haaudit.AutomationTrace, baseline map[string]struct{}) *haaudit.AutomationTrace {
	for i := range traces {
		trace := &traces[i]
		if _, existed := baseline[trace.RunID]; !existed {
			return trace
		}
	}
	return nil
}

// fetchTraceForValidation attributes new runs to captured action contexts.
// Explicit fresh_unique attribution observes the full window and rejects any
// competing run. Legacy callers without captured contexts retain time filtering.
func (r *TestRunner) fetchTraceForValidation(automationID string, triggerTime time.Time, baseline map[string]struct{}, assertions *TraceAssertions) (*haaudit.AutomationTrace, *FullTrace, error) {
	ws, err := r.getWSClient()
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect WebSocket for traces: %w", err)
	}

	resolver, err := r.getResolver()
	if err != nil {
		return nil, nil, fmt.Errorf("could not build automation ID resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)

	deadline := time.Now().Add(TracePollTimeout)
	cutoff := triggerTime.Add(-TraceClockSkew)
	var lastTraces []haaudit.AutomationTrace
	var fallbackTrace *haaudit.AutomationTrace
	var uniqueTrace *haaudit.AutomationTrace
	observedFresh := map[string]haaudit.AutomationTrace{}

	for time.Now().Before(deadline) {
		traces, err := haaudit.FetchTraceListWS(ws, itemID)
		if err != nil {
			return nil, nil, fmt.Errorf("trace/list %s: %w", automationID, err)
		}
		lastTraces = traces
		if assertions != nil && assertions.Attribution == "fresh_unique" {
			// Keep all observed runs, even if HA's bounded trace storage evicts one.
			for _, trace := range traces {
				if _, old := baseline[trace.RunID]; !old {
					observedFresh[trace.RunID] = trace
				}
			}
			observed := make([]haaudit.AutomationTrace, 0, len(observedFresh))
			for _, trace := range observedFresh {
				observed = append(observed, trace)
			}
			uniqueTrace, err = uniqueFreshTrace(observed, baseline)
			if err != nil {
				return nil, nil, err
			}
			if err := r.sleep(TracePollInterval); err != nil {
				return nil, nil, err
			}
			continue
		}

		fetch := func(runID string) (*FullTrace, error) { return r.fetchFullTrace(automationID, runID) }
		var best *haaudit.AutomationTrace
		var fullTrace *FullTrace
		contexts := r.traceContexts(assertions)
		if len(contexts) > 0 {
			best, fullTrace, err = selectAttributedTrace(traces, baseline, contexts, assertions, fetch)
		} else {
			best, fullTrace, err = selectTraceCandidate(traces, cutoff, baseline, assertions, fetch)
		}
		if err != nil {
			return nil, nil, err
		}
		if best != nil {
			if hasStructuredAssertions(assertions) && fullTrace == nil {
				fallbackTrace = best
			} else {
				return best, fullTrace, nil
			}
		}

		if err := r.sleep(TracePollInterval); err != nil {
			return nil, nil, err
		}
	}

	if uniqueTrace != nil {
		if uniqueTrace.State != "stopped" && uniqueTrace.State != "aborted" && uniqueTrace.Error == "" {
			return nil, nil, fmt.Errorf("unique fresh trace %s did not finish within observation window", uniqueTrace.RunID)
		}
		full, err := r.fetchFullTrace(automationID, uniqueTrace.RunID)
		return uniqueTrace, full, err
	}
	if fallbackTrace != nil {
		return fallbackTrace, nil, nil
	}

	if len(lastTraces) > 0 {
		var details []string
		for _, t := range lastTraces {
			details = append(details, fmt.Sprintf("  run=%s state=%s ts=%s", t.RunID, t.State, t.Timestamp.Format(time.RFC3339)))
		}
		return nil, nil, fmt.Errorf("no completed trace found for %s within %s (cutoff: %s)\nAvailable traces:\n%s",
			automationID, TracePollTimeout, cutoff.Format(time.RFC3339Nano), strings.Join(details, "\n"))
	}

	return nil, nil, fmt.Errorf("no traces found for %s within %s", automationID, TracePollTimeout)
}

func selectTraceCandidate(traces []haaudit.AutomationTrace, cutoff time.Time, baseline map[string]struct{}, assertions *TraceAssertions, fetchFull fullTraceFetcher) (*haaudit.AutomationTrace, *FullTrace, error) {
	var latest *haaudit.AutomationTrace
	for i := range traces {
		trace := &traces[i]
		if !isCompletedTraceAfter(trace, cutoff) {
			continue
		}
		// Exclude runs that existed before the trigger.
		if baseline != nil {
			if _, existed := baseline[trace.RunID]; existed {
				continue
			}
		}
		if latest == nil || trace.Timestamp.After(latest.Timestamp) {
			latest = trace
		}
	}

	if latest == nil || !hasStructuredAssertions(assertions) {
		return latest, nil, nil
	}
	if fetchFull == nil {
		return latest, nil, nil
	}
	selectionAssertions := *assertions
	selectionAssertions.RejectActions = nil
	selectionAssertions.ExpectActionsInOrder = nil
	if !hasStructuredAssertions(&selectionAssertions) {
		fullTrace, err := fetchFull(latest.RunID)
		if err != nil {
			return nil, nil, err
		}
		return latest, fullTrace, nil
	}

	var bestMatch *haaudit.AutomationTrace
	var bestFullTrace *FullTrace
	for i := range traces {
		trace := &traces[i]
		if !isCompletedTraceAfter(trace, cutoff) {
			continue
		}
		if baseline != nil {
			if _, existed := baseline[trace.RunID]; existed {
				continue
			}
		}
		fullTrace, err := fetchFull(trace.RunID)
		if err != nil {
			return nil, nil, err
		}
		if !allTraceAssertionsPass(fullTrace, &selectionAssertions) {
			continue
		}
		if bestMatch == nil || trace.Timestamp.After(bestMatch.Timestamp) {
			bestMatch = trace
			bestFullTrace = fullTrace
		}
	}

	if bestMatch != nil {
		return bestMatch, bestFullTrace, nil
	}
	return latest, nil, nil
}

func isCompletedTraceAfter(trace *haaudit.AutomationTrace, cutoff time.Time) bool {
	if trace == nil || trace.Timestamp.Before(cutoff) {
		return false
	}
	return trace.State == "stopped" || trace.Error != ""
}

func allTraceAssertionsPass(fullTrace *FullTrace, assertions *TraceAssertions) bool {
	for _, result := range evaluateTraceAssertions(fullTrace, assertions) {
		if !result.Passed {
			return false
		}
	}
	return true
}

// validateBaseTrace copies trace metadata and reports execution errors or aborts.
// Completion assertions are evaluated separately.
func validateBaseTrace(trace *haaudit.AutomationTrace, automationID string, fetchLatency time.Duration) *TraceResult {
	result := &TraceResult{
		AutomationID: automationID,
		RunID:        trace.RunID,
		State:        trace.State,
		LastStep:     trace.LastStep,
		FetchLatency: fetchLatency,
	}

	if trace.Error != "" {
		result.Error = fmt.Sprintf("automation trace error: %s", trace.Error)
	} else if trace.State != "stopped" {
		// mode: restart can abort an earlier run; report that abort as an error.
		if trace.State == "aborted" {
			result.Error = "trace was aborted (mode: restart may have cancelled it)"
		}
	}

	return result
}

// fetchFullTrace retrieves the complete execution trace via trace/get.
func (r *TestRunner) fetchFullTrace(automationID, runID string) (*FullTrace, error) {
	ws, err := r.getWSClient()
	if err != nil {
		return nil, fmt.Errorf("could not connect WebSocket for trace/get: %w", err)
	}

	resolver, err := r.getResolver()
	if err != nil {
		return nil, fmt.Errorf("could not build automation ID resolver: %w", err)
	}
	itemID := resolver.Resolve(automationID)
	raw, err := ws.SendCommandWithParams("trace/get", map[string]interface{}{
		"domain":  "automation",
		"item_id": itemID,
		"run_id":  runID,
	})
	if err != nil {
		return nil, fmt.Errorf("trace/get %s run=%s: %w", itemID, runID, err)
	}

	var trace FullTrace
	if err := json.Unmarshal(raw, &trace); err != nil {
		return nil, fmt.Errorf("trace/get %s: unmarshal: %w", itemID, err)
	}

	if err := r.resolveTraceTargets(&trace); err != nil {
		return nil, err
	}
	return &trace, nil
}

// getWSClient returns the cached WSClient, connecting on first call.
func (r *TestRunner) getWSClient() (*hasync.WSClient, error) {
	if r.wsClient != nil {
		r.wsClient.SetContext(r.client.context())
		return r.wsClient, nil
	}

	ws := hasync.NewWSClient(r.wsURL, r.wsToken)
	ws.SetContext(r.client.context())
	if err := ws.ConnectContext(r.client.context()); err != nil {
		return nil, err
	}
	r.wsClient = ws
	return ws, nil
}

// getResolver returns the cached AutomationIDResolver, creating it on first call.
func (r *TestRunner) getResolver() (*haaudit.AutomationIDResolver, error) {
	if r.resolver != nil {
		return r.resolver, nil
	}

	ws, err := r.getWSClient()
	if err != nil {
		return nil, err
	}
	resolver, err := haaudit.NewAutomationIDResolver(ws)
	if err != nil {
		return nil, err
	}
	r.resolver = resolver
	return resolver, nil
}

// evaluateTraceAssertions checks completion, errors, branch, required actions,
// action order, and rejected actions against a full trace.
func evaluateTraceAssertions(fullTrace *FullTrace, assertions *TraceAssertions) []TraceAssertionResult {
	var results []TraceAssertionResult

	if assertions.ExpectCompleted != nil {
		expected := *assertions.ExpectCompleted
		actual := fullTrace.State == "stopped"
		result := TraceAssertionResult{
			Name:   fmt.Sprintf("expect_completed: %v", expected),
			Passed: actual == expected,
		}
		if !result.Passed {
			result.Detail = fmt.Sprintf("expected completed=%v, got state=%q", expected, fullTrace.State)
		}
		results = append(results, result)
	}

	if assertions.ExpectNoErrors != nil {
		expected := *assertions.ExpectNoErrors
		actual := fullTrace.Error == ""
		result := TraceAssertionResult{
			Name:   fmt.Sprintf("expect_no_errors: %v", expected),
			Passed: actual == expected,
		}
		if !result.Passed {
			if expected {
				result.Detail = fmt.Sprintf("expected no errors, got: %s", fullTrace.Error)
			} else {
				result.Detail = "expected an error but trace completed cleanly"
			}
		}
		results = append(results, result)
	}

	if assertions.ExpectBranch != "" {
		matched, actualID, err := matchBranch(fullTrace, assertions.ExpectBranch)
		result := TraceAssertionResult{
			Name: fmt.Sprintf("expect_branch: %s", assertions.ExpectBranch),
		}
		if err != nil {
			result.Passed = false
			result.Detail = err.Error()
		} else if matched {
			result.Passed = true
		} else {
			result.Passed = false
			result.Detail = fmt.Sprintf("expected branch %q, got trigger.id=%q", assertions.ExpectBranch, actualID)
		}
		results = append(results, result)
	}

	if len(assertions.ExpectActions) > 0 {
		actionResults := matchActions(fullTrace, assertions.ExpectActions)
		results = append(results, actionResults...)
	}
	if len(assertions.ExpectActionsInOrder) > 0 {
		actionResults := matchActionsInOrder(fullTrace, assertions.ExpectActionsInOrder)
		results = append(results, actionResults...)
	}
	if len(assertions.RejectActions) > 0 {
		actionResults := rejectActions(fullTrace, assertions.RejectActions)
		results = append(results, actionResults...)
	}

	return results
}
