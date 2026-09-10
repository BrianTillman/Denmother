package hatest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// matchBranch reads trigger.id from the trace's changed_variables and
// compares it to the expected branch name. Returns (matched, actualTriggerID, error).
func matchBranch(trace *FullTrace, branchName string) (bool, string, error) {
	triggerID, found := extractTriggerID(trace)
	if !found {
		return false, "", fmt.Errorf("trigger.id not found in trace changed_variables; check the trace JSON manually")
	}

	return triggerID == branchName, triggerID, nil
}

// extractTriggerID finds the trigger ID from the trace's changed_variables.
// It looks through all trace nodes for a "trigger" key in changed_variables
// that contains an "id" field.
func extractTriggerID(trace *FullTrace) (string, bool) {
	for path, nodes := range trace.Trace {
		if !strings.HasPrefix(path, "trigger") && path != "" {
			continue
		}
		for _, node := range nodes {
			if id, ok := getTriggerIDFromChanged(node.Changed); ok {
				return id, true
			}
		}
	}

	// Trigger variables may also appear in action nodes.
	for _, nodes := range trace.Trace {
		for _, node := range nodes {
			if id, ok := getTriggerIDFromChanged(node.Changed); ok {
				return id, true
			}
		}
	}

	return "", false
}

// getTriggerIDFromChanged extracts trigger.id from a changed_variables map.
// The structure is: {"trigger": {"id": "occupied", ...}}
func getTriggerIDFromChanged(changed map[string]interface{}) (string, bool) {
	if changed == nil {
		return "", false
	}

	triggerRaw, ok := changed["trigger"]
	if !ok {
		return "", false
	}

	triggerMap, ok := triggerRaw.(map[string]interface{})
	if !ok {
		return "", false
	}

	idRaw, ok := triggerMap["id"]
	if !ok {
		return "", false
	}

	idStr, ok := idRaw.(string)
	return idStr, ok
}

// matchActions checks that each expected service call appears in the trace.
// Returns a TraceAssertionResult for each expected action.
func matchActions(trace *FullTrace, expected []ExpectedAction) []TraceAssertionResult {
	serviceCalls := collectServiceCalls(trace)

	var results []TraceAssertionResult
	for _, exp := range expected {
		result := TraceAssertionResult{
			Name: fmt.Sprintf("expect_action: %s", exp.Service),
		}

		found := false
		for _, call := range serviceCalls {
			if matchServiceCall(call, exp) {
				found = true
				break
			}
		}

		if found {
			result.Passed = true
		} else {
			result.Passed = false
			result.Detail = fmt.Sprintf("service %s not found in trace", exp.Service)
			if exp.Target != "" {
				result.Detail += fmt.Sprintf(" targeting %s", exp.Target)
			}
		}
		results = append(results, result)
	}

	return results
}

// rejectActions checks that each forbidden service call is absent from the trace.
// It uses the same partial target and data matching rules as expect_actions.
func rejectActions(trace *FullTrace, rejected []ExpectedAction) []TraceAssertionResult {
	serviceCalls := collectServiceCalls(trace)

	var results []TraceAssertionResult
	for _, reject := range rejected {
		result := TraceAssertionResult{
			Name:   fmt.Sprintf("reject_action: %s", reject.Service),
			Passed: true,
		}
		for _, call := range serviceCalls {
			if matchServiceCall(call, reject) {
				result.Passed = false
				result.Detail = fmt.Sprintf("forbidden service %s found in trace", reject.Service)
				if reject.Target != "" {
					result.Detail += fmt.Sprintf(" targeting %s", reject.Target)
				}
				break
			}
		}
		results = append(results, result)
	}

	return results
}

// matchActionsInOrder checks that each expected service call appears after the
// previous match in strictly increasing trace timestamps (start order).
// Missing or equal timestamps fail the order check.
func matchActionsInOrder(trace *FullTrace, expected []ExpectedAction) []TraceAssertionResult {
	serviceCalls := collectServiceCalls(trace)
	sort.SliceStable(serviceCalls, func(i, j int) bool { return serviceCalls[i].Timestamp.Before(serviceCalls[j].Timestamp) })
	var previous time.Time
	var missingTime bool
	for _, call := range serviceCalls {
		if call.Timestamp.IsZero() {
			for _, exp := range expected {
				if matchServiceCall(call, exp) {
					missingTime = true
				}
			}
		}
	}
	nextCall := 0
	results := make([]TraceAssertionResult, 0, len(expected))

	for i, exp := range expected {
		result := TraceAssertionResult{Name: fmt.Sprintf("expect_action_in_order[%d]: %s", i, exp.Service)}
		for nextCall < len(serviceCalls) {
			call := serviceCalls[nextCall]
			nextCall++
			if !missingTime && call.Timestamp.After(previous) && matchServiceCall(call, exp) {
				result.Passed = true
				previous = call.Timestamp
				break
			}
		}
		if !result.Passed {
			result.Detail = fmt.Sprintf("service %s lacks evidence of starting strictly after the prior ordered action (missing or equal timestamps cannot prove order)", exp.Service)
		}
		results = append(results, result)
	}

	return results
}

// traceServiceCall is a service call extracted from a trace node.
type traceServiceCall struct {
	Timestamp time.Time
	Service   string
	Target    string
	Targets   []string
	Data      map[string]interface{}
}

func (c *traceServiceCall) addTargets(raw interface{}) {
	for _, id := range normalizeEntityIDs(raw) {
		if id == "" {
			continue
		}
		if c.Target == "" {
			c.Target = id
		}
		seen := false
		for _, existing := range c.Targets {
			if existing == id {
				seen = true
				break
			}
		}
		if !seen {
			c.Targets = append(c.Targets, id)
		}
	}
}

func (c traceServiceCall) hasTarget(expected string) bool {
	for _, target := range c.Targets {
		if target == expected {
			return true
		}
	}
	return c.Target == expected
}

func normalizeEntityIDs(raw interface{}) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []interface{}:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if id, ok := item.(string); ok {
				ids = append(ids, id)
			}
		}
		return ids
	default:
		return nil
	}
}

// collectServiceCalls extracts all service calls from trace action nodes.
func collectServiceCalls(trace *FullTrace) []traceServiceCall {
	var calls []traceServiceCall

	paths := make([]string, 0, len(trace.Trace))
	for path := range trace.Trace {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		return tracePathLess(paths[i], paths[j])
	})
	for _, path := range paths {
		nodes := trace.Trace[path]
		for _, node := range nodes {
			if node.Result == nil || node.Result.Params == nil {
				continue
			}

			params := node.Result.Params

			domain, _ := params["domain"].(string)
			service, _ := params["service"].(string)
			if domain == "" || service == "" {
				continue
			}

			timestamp, _ := time.Parse(time.RFC3339Nano, node.Timestamp)
			call := traceServiceCall{
				Timestamp: timestamp,
				Service:   domain + "." + service,
				Data:      make(map[string]interface{}),
			}

			if serviceData, ok := params["service_data"].(map[string]interface{}); ok {
				for k, v := range serviceData {
					call.Data[k] = v
				}

				call.addTargets(serviceData["entity_id"])
			}

			if target, ok := params["target"].(map[string]interface{}); ok {
				call.addTargets(target["entity_id"])
			}

			calls = append(calls, call)
		}
	}

	return calls
}

func tracePathLess(left, right string) bool {
	leftSegments := strings.Split(left, "/")
	rightSegments := strings.Split(right, "/")
	limit := len(leftSegments)
	if len(rightSegments) < limit {
		limit = len(rightSegments)
	}

	for i := 0; i < limit; i++ {
		if leftSegments[i] == rightSegments[i] {
			continue
		}
		leftIndex, leftErr := strconv.Atoi(leftSegments[i])
		rightIndex, rightErr := strconv.Atoi(rightSegments[i])
		if leftErr == nil && rightErr == nil && leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		return leftSegments[i] < rightSegments[i]
	}

	return len(leftSegments) < len(rightSegments)
}

// matchServiceCall checks if a trace service call matches an expected action.
func matchServiceCall(call traceServiceCall, expected ExpectedAction) bool {
	if call.Service != expected.Service {
		return false
	}

	if expected.Target != "" && !call.hasTarget(expected.Target) {
		return false
	}

	// Partial data match: every key in expected.Data must match in call.Data
	for key, expectedVal := range expected.Data {
		actualVal, ok := call.Data[key]
		if !ok {
			return false
		}
		if !matchValue(actualVal, expectedVal, "==") {
			return false
		}
	}

	return true
}
