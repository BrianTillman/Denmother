package hatest

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BrianTillman/Denmother/internal/haaudit"
)

// HA returns the command context for call_service and fire_event. Keeping it
// links a trace to this case even when another client runs the same automation.
// https://developers.home-assistant.io/docs/api/websocket/
func (r *TestRunner) captureCommandContext(command string, params map[string]interface{}) error {
	ws, err := r.getWSClient()
	if err != nil {
		return err
	}
	raw, err := ws.SendCommandContext(r.client.context(), command, params)
	if err != nil {
		return err
	}
	var result struct {
		Context TraceContext `json:"context"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode command attribution: %w", err)
	}
	if result.Context.ID == "" {
		return fmt.Errorf("HA %s did not return a context ID; trace attribution is unavailable", command)
	}
	r.traceContextIDs = append(r.traceContextIDs, result.Context.ID)
	return nil
}

func (r *TestRunner) callTriggerService(domain, service string, data map[string]interface{}) error {
	if !r.traceCapture {
		return r.client.CallService(domain, service, data)
	}
	target := targetFromPayload(data)
	serviceData := copyAttributes(data)
	for key := range target {
		delete(serviceData, key)
	}
	params := map[string]interface{}{"domain": domain, "service": service, "service_data": serviceData}
	if len(target) > 0 {
		params["target"] = target
	}
	return r.captureCommandContext("call_service", params)
}

func (r *TestRunner) fireTriggerEvent(eventType string, data map[string]interface{}) error {
	if !r.traceCapture {
		return r.client.FireEvent(eventType, data)
	}
	params := map[string]interface{}{"event_type": eventType}
	if data != nil {
		params["event_data"] = data
	}
	return r.captureCommandContext("fire_event", params)
}

func (r *TestRunner) traceContexts(assertions *TraceAssertions) []string {
	if assertions != nil && assertions.Attribution == "fresh_unique" {
		return nil
	}
	if assertions != nil && assertions.TriggerIndex != nil {
		index := *assertions.TriggerIndex
		if index >= 0 && index < len(r.traceContextIDs) {
			return []string{r.traceContextIDs[index]}
		}
		return nil
	}
	return r.traceContextIDs
}

func traceBelongsToContext(trace *FullTrace, contexts []string) bool {
	for _, id := range contexts {
		if id != "" && (trace.Context.ID == id || trace.Context.ParentID == id) {
			return true
		}
	}
	return false
}

// selectAttributedTrace never chooses a passing run by its expected actions.
// Context identifies the action; assertions are evaluated only afterward.
func selectAttributedTrace(traces []haaudit.AutomationTrace, baseline map[string]struct{}, contexts []string, assertions *TraceAssertions, fetch fullTraceFetcher) (*haaudit.AutomationTrace, *FullTrace, error) {
	type candidate struct {
		summary *haaudit.AutomationTrace
		full    *FullTrace
	}
	var candidates []candidate
	for i := range traces {
		summary := &traces[i]
		if _, old := baseline[summary.RunID]; old {
			continue
		}
		full, err := fetch(summary.RunID)
		if err != nil {
			return nil, nil, err
		}
		if full.Context.ID == "" {
			return nil, nil, fmt.Errorf("trace %s has no context; cannot attribute concurrent runs", summary.RunID)
		}
		if !traceBelongsToContext(full, contexts) {
			continue
		}
		candidates = append(candidates, candidate{summary, full})
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	if len(candidates) > 1 && assertions != nil && assertions.ExpectBranch != "" {
		var filtered []candidate
		for _, candidate := range candidates {
			if matched, _, _ := matchBranch(candidate.full, assertions.ExpectBranch); matched {
				filtered = append(filtered, candidate)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}
	if len(candidates) > 1 {
		ids := []string{}
		for _, candidate := range candidates {
			ids = append(ids, candidate.summary.RunID)
		}
		sort.Strings(ids)
		return nil, nil, fmt.Errorf("ambiguous traces for this case's action contexts: %v; select trace_assertions.trigger_index or split the case", ids)
	}
	selected := candidates[0]
	if selected.summary.State != "stopped" && selected.summary.State != "aborted" && selected.summary.Error == "" {
		return nil, nil, nil
	}
	return selected.summary, selected.full, nil
}

func (r *TestRunner) unexpectedAttributedTrace(traces []haaudit.AutomationTrace, baseline map[string]struct{}, contexts []string, automationID string) (*haaudit.AutomationTrace, error) {
	for i := range traces {
		summary := &traces[i]
		if _, old := baseline[summary.RunID]; old {
			continue
		}
		full, err := r.fetchFullTrace(automationID, summary.RunID)
		if err != nil {
			return nil, err
		}
		if full.Context.ID == "" {
			return nil, fmt.Errorf("trace %s has no context; cannot verify absence for this case", summary.RunID)
		}
		if traceBelongsToContext(full, contexts) {
			return summary, nil
		}
	}
	return nil, nil
}

// uniqueFreshTrace rejects multiple runs absent from the baseline. Callers must
// observe the full trigger window before accepting a unique run.
func uniqueFreshTrace(traces []haaudit.AutomationTrace, baseline map[string]struct{}) (*haaudit.AutomationTrace, error) {
	var fresh *haaudit.AutomationTrace
	for i := range traces {
		trace := &traces[i]
		if _, old := baseline[trace.RunID]; old {
			continue
		}
		if fresh != nil && fresh.RunID != trace.RunID {
			return nil, fmt.Errorf("ambiguous fresh traces %s and %s; fresh_unique requires an isolated trigger window", fresh.RunID, trace.RunID)
		}
		fresh = trace
	}
	return fresh, nil
}
