package haaudit

import (
	"fmt"
	"time"
)

const (
	VibeSleep     = "input_boolean.vibe_sleep"
	VibeEntertain = "input_boolean.vibe_entertain"
	VibeRelax     = "input_boolean.vibe_relax"
	VibeNormal    = "input_boolean.vibe_normal"
)

var vibeOrder = []string{VibeSleep, VibeEntertain, VibeRelax}

// History audits triggered by an off transition also recognize these helper aliases.
var vibeAliases = map[string][]string{
	VibeSleep:     {"input_boolean.vibe_sleeping"},
	VibeEntertain: {"input_boolean.vibe_entertaining"},
	VibeRelax:     {"input_boolean.vibe_chilling"},
	VibeNormal:    {"input_boolean.vibe_vibing"},
}

// OutcomeStatus is the result of evaluating a single expectation.
type OutcomeStatus string

// Outcome status constants for OutcomeResult.Status.
const (
	OutcomePass            OutcomeStatus = "pass"
	OutcomePassIdempotent  OutcomeStatus = "pass_idempotent" // target already satisfied the expectation before the trigger
	OutcomeCanceled        OutcomeStatus = "canceled"        // delayed branch was canceled by a declared later trigger
	OutcomeMissing         OutcomeStatus = "missing"
	OutcomeWrongState      OutcomeStatus = "wrong_state"
	OutcomeWrongBrightness OutcomeStatus = "wrong_brightness"
	OutcomeTraceError      OutcomeStatus = "trace_error" // automation trace errored or did not stop cleanly
)

// TriggerEvent is a detected real trigger in entity history.
type TriggerEvent struct {
	Timestamp time.Time
}

// AuditResult holds the full correlation output for a spec.
type AuditResult struct {
	SpecName      string
	AutomationID  string // full automation entity_id, including the domain
	TriggerEntity string // entity_id that was watched for triggers
	TriggerTo     string // state that constitutes a trigger
	Tolerance     time.Duration
	TriggerEvents []TriggerEventResult
	Summary       ResultSummary
}

// TriggerEventResult is the outcome for one detected trigger event.
type TriggerEventResult struct {
	Timestamp   time.Time
	ActiveVibe  string
	Expectation *Expectation // nil when no expectation is configured for the active vibe
	Outcome     *OutcomeResult
	Trace       *TraceAnnotation // nil when no matching trace found
}

// OutcomeResult describes whether an expectation was met.
type OutcomeResult struct {
	Status      OutcomeStatus
	ActualState string
	ActualAttrs map[string]interface{}
	Delay       time.Duration
	Details     string
}

// ResultSummary counts outcomes across all trigger events.
type ResultSummary struct {
	Passed           int
	PassedIdempotent int
	Canceled         int
	Failed           int
	NoData           int
}

// DetectTriggers examines adjacent observations. Unavailable endpoints suppress
// that transition, but the recovered value remains the next transition's baseline.
func DetectTriggers(entries []HistoryEntry, toState string) []TriggerEvent {
	var triggers []TriggerEvent
	for i := 1; i < len(entries); i++ {
		previous, current := entries[i-1], entries[i]
		if invalidHistoryState(previous.State) || invalidHistoryState(current.State) {
			continue
		}
		if previous.LastChangedTime.IsZero() || current.LastChangedTime.IsZero() || !current.LastChangedTime.After(previous.LastChangedTime) {
			continue
		}
		if current.State == toState && previous.State != toState {
			triggers = append(triggers, TriggerEvent{Timestamp: current.LastChangedTime})
		}
	}
	return triggers
}

func invalidHistoryState(state string) bool {
	return state == "unknown" || state == "unavailable" || state == ""
}

// resolveVibe returns the active vibe entity ID at the given timestamp by
// checking vibe history in priority order (sleep > entertain > relax).
// Falls back to VibeNormal if none are on.
func resolveVibe(history EntityTimeline, timestamp time.Time) string {
	return resolveVibeAt(history, timestamp, false)
}

func resolveVibeWithLegacyAliases(history EntityTimeline, timestamp time.Time) string {
	return resolveVibeAt(history, timestamp, true)
}

func resolveVibeAt(history EntityTimeline, timestamp time.Time, includeLegacyAliases bool) string {
	for _, vibe := range vibeOrder {
		for _, entityID := range vibeEntityIDs(vibe, includeLegacyAliases) {
			entry := LastBefore(history[entityID], timestamp)
			if entry != nil && entry.State == "on" {
				return vibe
			}
		}
	}
	return VibeNormal
}

func vibeEntityIDs(vibe string, includeLegacyAliases bool) []string {
	ids := []string{vibe}
	if includeLegacyAliases {
		ids = append(ids, vibeAliases[vibe]...)
	}
	return ids
}

// attributeAsFloat converts a JSON-unmarshaled number to float64.
// JSON numbers are float64; int is handled defensively.
func attributeAsFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// compareAttribute evaluates actual op expected, where op is >=, <=, >, <, or ==.
func compareAttribute(actual interface{}, op string, expected float64) bool {
	f, ok := attributeAsFloat(actual)
	if !ok {
		return false
	}

	switch op {
	case ">=":
		return f >= expected
	case "<=":
		return f <= expected
	case ">":
		return f > expected
	case "<":
		return f < expected
	case "==":
		return f == expected
	default:
		return false
	}
}

// firstCancelInWindow returns the first configured cancel transition that falls
// within a delayed audit window.
func firstCancelInWindow(history EntityTimeline, cancels []CancelDef, triggerTime time.Time) *OutcomeResult {
	for _, cancel := range cancels {
		within, err := time.ParseDuration(cancel.Within)
		if err != nil {
			continue
		}
		for _, entry := range DetectTriggers(history[cancel.EntityID], cancel.To) {
			if entry.Timestamp.After(triggerTime) && !entry.Timestamp.After(triggerTime.Add(within)) {
				return &OutcomeResult{
					Status:  OutcomeCanceled,
					Delay:   entry.Timestamp.Sub(triggerTime),
					Details: fmt.Sprintf("canceled by %s→%s", cancel.EntityID, cancel.To),
				}
			}
		}
	}
	return nil
}

// matchOutcome searches targetEntries for a matching entry in
// [triggerTime, triggerTime+tolerance]. Transient intermediate states are
// tolerated when the expected final state appears within the same window.
func matchOutcome(targetEntries []HistoryEntry, expectation Expectation, triggerTime time.Time, tolerance time.Duration) *OutcomeResult {
	windowEnd := triggerTime.Add(tolerance)
	var firstWrongState *HistoryEntry
	var firstWrongBrightness *OutcomeResult
	foundAny := false

	for i := range targetEntries {
		entry := &targetEntries[i]
		t := entry.LastChangedTime
		if t.Before(triggerTime) || t.After(windowEnd) {
			continue
		}
		foundAny = true

		if entry.State != expectation.State {
			if firstWrongState == nil {
				firstWrongState = entry
			}
			continue
		}

		wrongBrightness := false
		for attrKey, expr := range expectation.Attributes {
			op, expected, err := ParseAttributeComparison(expr)
			if err != nil {
				return &OutcomeResult{
					Status:  OutcomeWrongBrightness,
					Details: fmt.Sprintf("unparseable attribute expression %q: %v", expr, err),
				}
			}
			actual, ok := entry.Attributes[attrKey]
			if !ok || !compareAttribute(actual, op, expected) {
				wrongBrightness = true
				if firstWrongBrightness == nil {
					firstWrongBrightness = &OutcomeResult{
						Status:      OutcomeWrongBrightness,
						ActualState: entry.State,
						ActualAttrs: entry.Attributes,
						Delay:       entry.LastChangedTime.Sub(triggerTime),
						Details:     fmt.Sprintf("attribute %q: expected %s %v, got %v", attrKey, op, expected, actual),
					}
				}
				break
			}
		}
		if wrongBrightness {
			continue
		}

		return &OutcomeResult{
			Status:      OutcomePass,
			ActualState: entry.State,
			ActualAttrs: entry.Attributes,
			Delay:       entry.LastChangedTime.Sub(triggerTime),
		}
	}

	if !foundAny {
		return &OutcomeResult{Status: OutcomeMissing}
	}

	if firstWrongBrightness != nil {
		return firstWrongBrightness
	}

	if firstWrongState != nil {
		return &OutcomeResult{
			Status:      OutcomeWrongState,
			ActualState: firstWrongState.State,
			ActualAttrs: firstWrongState.Attributes,
			Delay:       firstWrongState.LastChangedTime.Sub(triggerTime),
		}
	}

	return &OutcomeResult{Status: OutcomeMissing}
}

// checkAllAttrs returns true iff every attribute expression in attrs is satisfied
// by the corresponding value in actual. Used by both matchOutcome and ResolveIdempotent.
func checkAllAttrs(attrs map[string]string, actual map[string]interface{}) bool {
	for attrKey, expr := range attrs {
		op, expected, err := ParseAttributeComparison(expr)
		if err != nil {
			return false
		}
		v, ok := actual[attrKey]
		if !ok || !compareAttribute(v, op, expected) {
			return false
		}
	}
	return true
}

// checkNoStateChange returns pass when the audited automation does not change
// the entity. Same-state refreshes and changes attributed to another source are
// ignored; unattributed changes fail conservatively.
func checkNoStateChange(targetEntries []HistoryEntry, automationID string, triggerTime time.Time, tolerance time.Duration) *OutcomeResult {
	baseline := LastBefore(targetEntries, triggerTime)
	windowEnd := triggerTime.Add(tolerance)
	for i := range targetEntries {
		entry := &targetEntries[i]
		if entry.LastChangedTime.Before(triggerTime) || entry.LastChangedTime.After(windowEnd) {
			continue
		}
		if entry.ContextKnown && entry.ContextEntityID != automationID {
			continue
		}
		if baseline != nil && entry.State == baseline.State {
			continue
		}
		return &OutcomeResult{
			Status:      OutcomeWrongState,
			ActualState: entry.State,
			ActualAttrs: entry.Attributes,
			Delay:       entry.LastChangedTime.Sub(triggerTime),
			Details:     "expected no state change",
		}
	}
	return &OutcomeResult{Status: OutcomePass}
}

// expectsNoChange supports the explicit no_change contract and the legacy
// brightness == 0 convention used by older audit specifications.
func expectsNoChange(e Expectation) bool {
	if e.NoChange {
		return true
	}
	expr, ok := e.Attributes["brightness"]
	if !ok {
		return false
	}
	op, val, err := ParseAttributeComparison(expr)
	if err != nil {
		return false
	}
	return op == "==" && val == 0
}

// ResolveIdempotent upgrades MISSING outcomes to PASS_IDEMPOTENT when the target's
// state and all expected attributes already matched before the trigger.
// It updates results and summary counts in place without fetching traces.
func ResolveIdempotent(results []*AuditResult, history EntityTimeline) {
	for _, result := range results {
		for i := range result.TriggerEvents {
			ev := &result.TriggerEvents[i]
			if ev.Outcome == nil || ev.Outcome.Status != OutcomeMissing || ev.Expectation == nil {
				continue
			}

			prior := LastBefore(history[ev.Expectation.EntityID], ev.Timestamp)
			if prior == nil || prior.State != ev.Expectation.State {
				continue
			}

			if !checkAllAttrs(ev.Expectation.Attributes, prior.Attributes) {
				continue
			}

			ev.Outcome.Status = OutcomePassIdempotent
			ev.Outcome.ActualState = prior.State
			ev.Outcome.ActualAttrs = prior.Attributes
			if result.Summary.Failed > 0 {
				result.Summary.Failed--
			}
			result.Summary.PassedIdempotent++
		}
	}
}

// CorrelateFromTraces uses automation traces as the trigger source instead of
// entity state history. Each trace becomes a trigger event.
//
// Health check mode (no expectations): passes only traces that stopped without
// an error. Running, aborted, other non-stopped, and errored traces fail.
//
// Expectation mode: for each trace timestamp, optionally resolves the active vibe
// (when expectation has a Vibe field) and checks entity history for expected states.
func CorrelateFromTraces(spec *AuditSpec, traces []AutomationTrace, history EntityTimeline, tolerance time.Duration, start, end time.Time) *AuditResult {
	result := &AuditResult{
		SpecName:     spec.Name,
		AutomationID: spec.Automation,
		Tolerance:    tolerance,
		// TriggerEntity and TriggerTo left empty for trace-sourced specs.
	}

	var windowTraces []AutomationTrace
	for _, tr := range traces {
		if !tr.Timestamp.Before(start) && !tr.Timestamp.After(end) {
			windowTraces = append(windowTraces, tr)
		}
	}

	hasExpectations := len(spec.Expectations) > 0

	for _, tr := range windowTraces {
		annotation := &TraceAnnotation{
			RunID:    tr.RunID,
			State:    tr.State,
			LastStep: tr.LastStep,
			Error:    tr.Error,
		}

		if !hasExpectations {
			var outcome *OutcomeResult
			if tr.Error != "" {
				outcome = &OutcomeResult{
					Status:  OutcomeTraceError,
					Details: tr.Error,
				}
				result.Summary.Failed++
			} else if tr.State != "stopped" {
				outcome = &OutcomeResult{
					Status:      OutcomeTraceError,
					ActualState: tr.State,
					Details:     fmt.Sprintf("trace state %q did not stop cleanly", tr.State),
				}
				result.Summary.Failed++
			} else {
				outcome = &OutcomeResult{
					Status:      OutcomePass,
					ActualState: tr.State,
				}
				result.Summary.Passed++
			}
			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp: tr.Timestamp,
				Outcome:   outcome,
				Trace:     annotation,
			})
			continue
		}

		activeVibe := ""
		hasVibeExpectations := false
		for _, exp := range spec.Expectations {
			if exp.Vibe != "" {
				hasVibeExpectations = true
				break
			}
		}
		if hasVibeExpectations {
			activeVibe = resolveVibe(history, tr.Timestamp)
		}

		var matched []Expectation
		for _, exp := range spec.Expectations {
			if exp.Vibe == "" || exp.Vibe == activeVibe {
				matched = append(matched, exp)
			}
		}

		if len(matched) == 0 {
			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp:  tr.Timestamp,
				ActiveVibe: activeVibe,
				Outcome:    nil,
				Trace:      annotation,
			})
			result.Summary.NoData++
			continue
		}

		for _, exp := range matched {
			var outcome *OutcomeResult
			expCopy := exp
			expPtr := &expCopy

			if expectsNoChange(exp) {
				outcome = checkNoStateChange(history[exp.EntityID], spec.Automation, tr.Timestamp, tolerance)
			} else {
				outcome = matchOutcome(history[exp.EntityID], exp, tr.Timestamp, tolerance)
			}

			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp:   tr.Timestamp,
				ActiveVibe:  activeVibe,
				Expectation: expPtr,
				Outcome:     outcome,
				Trace:       annotation,
			})

			if outcome.Status == OutcomePass {
				result.Summary.Passed++
			} else {
				result.Summary.Failed++
			}
		}
	}

	return result
}

// Correlate runs the full correlation: detect triggers, resolve vibe, match outcomes.
func Correlate(spec *AuditSpec, history EntityTimeline, tolerance time.Duration) *AuditResult {
	result := &AuditResult{
		SpecName:      spec.Name,
		AutomationID:  spec.Automation,
		TriggerEntity: spec.Trigger.EntityID,
		TriggerTo:     spec.Trigger.To,
		Tolerance:     tolerance,
	}

	triggers := DetectTriggers(history[spec.Trigger.EntityID], spec.Trigger.To)

	for _, trigger := range triggers {
		activeVibe := resolveVibe(history, trigger.Timestamp)
		if spec.UsesLegacyVibeAliases() {
			activeVibe = resolveVibeWithLegacyAliases(history, trigger.Timestamp)
		}

		if canceled := firstCancelInWindow(history, spec.CanceledBy, trigger.Timestamp); canceled != nil {
			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp:  trigger.Timestamp,
				ActiveVibe: activeVibe,
				Outcome:    canceled,
			})
			result.Summary.Canceled++
			continue
		}

		var matched []Expectation
		for _, exp := range spec.Expectations {
			if exp.Vibe == activeVibe {
				matched = append(matched, exp)
			}
		}

		if len(matched) == 0 {
			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp:   trigger.Timestamp,
				ActiveVibe:  activeVibe,
				Expectation: nil,
				Outcome:     nil,
			})
			result.Summary.NoData++
			continue
		}

		for _, exp := range matched {
			var outcome *OutcomeResult
			expCopy := exp
			expPtr := &expCopy

			if expectsNoChange(exp) {
				outcome = checkNoStateChange(history[exp.EntityID], spec.Automation, trigger.Timestamp, tolerance)
			} else {
				outcome = matchOutcome(history[exp.EntityID], exp, trigger.Timestamp, tolerance)
			}

			result.TriggerEvents = append(result.TriggerEvents, TriggerEventResult{
				Timestamp:   trigger.Timestamp,
				ActiveVibe:  activeVibe,
				Expectation: expPtr,
				Outcome:     outcome,
			})

			if outcome.Status == OutcomePass {
				result.Summary.Passed++
			} else {
				result.Summary.Failed++
			}
		}
	}

	return result
}
