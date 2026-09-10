package hatest

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/hasync"
)

const (
	// PostTriggerDelay is the delay after triggers before checking assertions
	PostTriggerDelay = 100 * time.Millisecond
	// InterEntityDelay is the delay between setting entity states in setup
	InterEntityDelay = 50 * time.Millisecond
	// PollInterval is how often to check entity state during assertions
	PollInterval = 100 * time.Millisecond
	// MaxConsecutiveErrors is the threshold for failing due to repeated API errors
	MaxConsecutiveErrors = 10
)

type phaseError struct {
	Phase         string
	Index         int
	EntityID      string
	Service       string
	EventType     string
	AutomationID  string
	TraceRunID    string
	UnderlyingErr error
}

func (e *phaseError) Error() string {
	if e == nil || e.UnderlyingErr == nil {
		return ""
	}
	return e.UnderlyingErr.Error()
}

func (e *phaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.UnderlyingErr
}

type entitySnapshot struct {
	EntityID   string
	State      string
	Attributes map[string]interface{}
	Exists     bool
	Direct     bool
}

// TestRunner executes test specifications
type TestRunner struct {
	traceContextIDs []string
	traceCapture    bool
	dirty           bool
	client          *TestClient
	verbose         bool
	traceEnabled    bool
	wsClient        *hasync.WSClient
	wsURL           string
	wsToken         string
	resolver        *haaudit.AutomationIDResolver
}

// NewTestRunner creates a test runner
func NewTestRunner(baseURL, token string, verbose bool, traceEnabled bool) (*TestRunner, error) {
	return NewTestRunnerContext(context.Background(), baseURL, token, verbose, traceEnabled)
}

func NewTestRunnerContext(ctx context.Context, baseURL, token string, verbose, traceEnabled bool) (*TestRunner, error) {
	client := NewTestClient(baseURL, token).WithContext(ctx)

	if err := client.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to Home Assistant: %w", err)
	}

	return &TestRunner{
		client:       client,
		verbose:      verbose,
		traceEnabled: traceEnabled,
		wsURL:        baseURL,
		wsToken:      token,
	}, nil
}

// RunTestSpec executes a complete test specification
func (r *TestRunner) RunTestSpec(spec *TestSpec) (*TestResults, error) {
	return r.RunTestSpecWithFilter(spec, "")
}

// RunTestSpecWithFilter executes a test specification, optionally limiting
// execution to cases whose name or "parent > child" name matches caseFilter.
func (r *TestRunner) RunTestSpecWithFilter(spec *TestSpec, caseFilter string) (*TestResults, error) {
	results := &TestResults{
		SpecName:  spec.Name,
		StartTime: time.Now(),
	}

	for _, testCase := range spec.Tests {
		parentName := testCase.Name
		if matchesCaseFilter(parentName, caseFilter) {
			result := r.runTestCase(testCase, spec.Config)
			results.Cases = append(results.Cases, result)
		}

		if len(testCase.Subtests) > 0 {
			for _, subtest := range testCase.Subtests {
				fullName := fmt.Sprintf("%s > %s", testCase.Name, subtest.Name)
				if !matchesCaseFilter(fullName, caseFilter) && !matchesCaseFilter(subtest.Name, caseFilter) {
					continue
				}
				subtestResult := r.runTestCase(subtest, spec.Config)
				subtestResult.Name = fullName
				results.Cases = append(results.Cases, subtestResult)
			}
		}
	}

	results.EndTime = time.Now()
	return results, nil
}

func matchesCaseFilter(name, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	name = strings.TrimSpace(name)
	return name == filter || strings.Contains(strings.ToLower(name), strings.ToLower(filter))
}

func (r *TestRunner) runTestCase(tc TestCase, config TestConfig) (result TestCaseResult) {
	result = TestCaseResult{
		Name:      tc.Name,
		StartTime: time.Now(),
	}

	if r.dirty || r.client.context().Err() != nil {
		result.Status = StatusFailed
		result.Phase = "blocked"
		result.Error = "case blocked: previous cleanup failed or operation canceled"
		result.EndTime = time.Now()
		return result
	}
	if tc.Skip {
		result.Status = StatusSkipped
		result.EndTime = time.Now()
		return result
	}

	if r.verbose {
		fmt.Printf("Running: %s\n", tc.Name)
	}

	var resolveErr error
	tc, resolveErr = r.resolveCaseTargets(tc)
	if resolveErr != nil {
		result.Status = StatusFailed
		result.Phase = "target"
		result.Error = resolveErr.Error()
		result.EndTime = time.Now()
		return result
	}
	r.traceContextIDs = nil
	r.traceCapture = false
	snapshots, snapshotErr := r.snapshotTouchedEntities(tc, config)
	if snapshotErr != nil {
		result.Status = StatusFailed
		result.Phase = "snapshot"
		result.Error = fmt.Sprintf("Snapshot failed: %v", snapshotErr)
		result.EndTime = time.Now()
		return result
	}

	// Run configured cleanup and auto-restore after any phase returns, including
	// failures and cancellation.
	defer func() {
		originalClient := r.client
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.client = originalClient.WithContext(cleanupCtx)
		defer func() { r.client = originalClient }()
		var cleanupErrors []string
		if config.Cleanup && len(tc.Cleanup) > 0 {
			if err := r.executeCleanup(tc.Cleanup); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Sprintf("cleanup failed: %v", err))
				if r.verbose {
					fmt.Printf("Warning: Cleanup failed: %v\n", err)
				}
			}
		}
		if config.Cleanup && config.AutoRestoreEnabled() && len(snapshots) > 0 {
			if err := r.restoreSnapshots(snapshots); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Sprintf("auto-restore failed: %v", err))
				if r.verbose {
					fmt.Printf("Warning: Auto-restore failed: %v\n", err)
				}
			}
		}
		if len(cleanupErrors) > 0 {
			result.CleanupErrors = append(result.CleanupErrors, cleanupErrors...)
			r.dirty = true
			if result.Error != "" {
				result.Error += "; " + strings.Join(cleanupErrors, "; ")
			} else {
				result.Error = strings.Join(cleanupErrors, "; ")
			}
			if result.Status == "" || result.Status == StatusPassed {
				result.Status = StatusFailed
				result.Phase = "cleanup"
			}
		}
		result.EndTime = time.Now()
	}()

	if err := r.executeSetup(tc.Setup); err != nil {
		result.Status = StatusFailed
		applyPhaseError(&result, err)
		result.Error = fmt.Sprintf("Setup failed: %v", err)
		return result
	}

	var noTraceBaseline map[string]struct{}
	traceRequired := r.traceEnabled || tc.TraceAssertions != nil
	if traceRequired && tc.TraceAssertions != nil && tc.TraceAssertions.Automation != "" {
		var err error
		noTraceBaseline, err = r.snapshotTraceRunIDs(tc.TraceAssertions.Automation)
		if err != nil {
			result.Status = StatusFailed
			result.Phase = "trace"
			result.TraceAutomation = tc.TraceAssertions.Automation
			result.TraceError = fmt.Sprintf("Trace baseline failed: %v", err)
			return result
		}
	}

	r.traceCapture = tc.TraceAssertions != nil && tc.TraceAssertions.Automation != ""
	defer func() { r.traceCapture = false }()
	// Record the trigger boundary after setup but before the test action.
	triggerTime := time.Now()

	if err := r.executeTriggers(tc.Trigger); err != nil {
		result.Status = StatusFailed
		applyPhaseError(&result, err)
		result.Error = fmt.Sprintf("Trigger failed: %v", err)
		return result
	}

	if err := r.executeEvents(tc.Events); err != nil {
		result.Status = StatusFailed
		applyPhaseError(&result, err)
		result.Error = fmt.Sprintf("Event failed: %v", err)
		return result
	}

	result.TraceContextIDs = append([]string(nil), r.traceContexts(tc.TraceAssertions)...)
	if tc.TraceAssertions != nil {
		result.TraceAttribution = tc.TraceAssertions.Attribution
		if result.TraceAttribution == "" {
			result.TraceAttribution = "context"
		}
		result.TraceAttributionReason = tc.TraceAssertions.AttributionReason
	}
	if err := r.sleep(PostTriggerDelay); err != nil {
		result.Status = StatusFailed
		result.Phase = "trigger"
		result.Error = err.Error()
		return
	}

	if err := r.executeAssertions(tc.Assertions, config); err != nil {
		result.Status = StatusFailed
		applyPhaseError(&result, err)
		result.Error = fmt.Sprintf("Assertion failed: %v", err)
		return result
	}

	// Declared assertions are always part of the test's verification contract.
	if traceRequired {
		r.runTraceValidation(tc, triggerTime, noTraceBaseline, &result)
	}

	if result.Status == "" {
		result.Status = StatusPassed
	}
	return result
}

func applyPhaseError(result *TestCaseResult, err error) {
	if result == nil || err == nil {
		return
	}
	if pe, ok := err.(*phaseError); ok {
		result.Phase = pe.Phase
		result.ActionIndex = pe.Index
		result.EntityID = pe.EntityID
		result.Service = pe.Service
		result.EventType = pe.EventType
		result.TraceAutomation = pe.AutomationID
		result.TraceRunID = pe.TraceRunID
	}
}

func (r *TestRunner) executeSetup(actions []StateAction) error {
	for idx, action := range actions {
		method := action.Method
		if method == "" {
			method = "auto"
		}

		if r.verbose {
			fmt.Printf("  Setup: Setting %s to %s (method: %s)\n", action.EntityID, action.State, method)
		}

		var err error
		switch method {
		case "direct":
			err = r.client.SetEntityStateDirect(action.EntityID, action.State, action.Attributes)
		case "service":
			err = r.client.setEntityStateViaService(action.EntityID, action.State, action.Attributes)
		case "mock":
			data := map[string]interface{}{
				"entity_id": action.EntityID,
				"state":     action.State,
			}
			if action.Attributes != nil {
				data["attributes"] = action.Attributes
			}
			err = r.client.CallService("mock_entities", "set_state", data)
		case "auto":
			fallthrough
		default:
			err = r.client.SetEntityState(action.EntityID, action.State, action.Attributes)
		}

		if err != nil {
			return &phaseError{
				Phase:         "setup",
				Index:         idx,
				EntityID:      action.EntityID,
				UnderlyingErr: fmt.Errorf("failed to set %s to %s: %w", action.EntityID, action.State, err),
			}
		}

		if err := r.sleep(InterEntityDelay); err != nil {
			return err
		}
	}
	return nil
}

func (r *TestRunner) executeTriggers(calls []ServiceCall) error {
	for idx, call := range calls {
		domain, service := parseService(call.Service)

		data, err := serviceCallPayload(call)
		if err != nil {
			return &phaseError{Phase: "trigger", Index: idx, Service: call.Service, UnderlyingErr: err}
		}

		if r.verbose {
			fmt.Printf("  Trigger: %s.%s on %v\n", domain, service, data["entity_id"])
		}

		if err := r.callTriggerService(domain, service, data); err != nil {
			return &phaseError{
				Phase:         "trigger",
				Index:         idx,
				Service:       call.Service,
				EntityID:      firstEntityFromServiceCall(call),
				UnderlyingErr: fmt.Errorf("failed to call %s.%s: %w", domain, service, err),
			}
		}
		if call.Delay > 0 {
			if err := r.sleep(time.Duration(call.Delay)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *TestRunner) executeEvents(events []EventAction) error {
	for idx, event := range events {
		if r.verbose {
			fmt.Printf("  Event: %s with data %+v\n", event.EventType, event.Data)
		}

		if err := r.fireTriggerEvent(event.EventType, event.Data); err != nil {
			return &phaseError{
				Phase:         "event",
				Index:         idx,
				EventType:     event.EventType,
				UnderlyingErr: fmt.Errorf("failed to fire event %s: %w", event.EventType, err),
			}
		}

		if err := r.sleep(InterEntityDelay); err != nil {
			return err
		}
	}
	return nil
}

func (r *TestRunner) executeAssertions(assertions []Assertion, config TestConfig) error {
	for idx, assertion := range assertions {
		if assertion.Skip {
			continue
		}

		timeout := time.Duration(assertion.Timeout)
		if timeout == 0 {
			timeout = time.Duration(config.Timeout)
		}

		if r.verbose {
			fmt.Printf("  Assert: %s should be %s (timeout: %s)\n",
				assertion.EntityID, assertion.State, timeout)
		}

		if err := r.waitForAssertion(assertion, timeout); err != nil {
			return &phaseError{
				Phase:         "assertion",
				Index:         idx,
				EntityID:      assertion.EntityID,
				UnderlyingErr: err,
			}
		}
	}

	return nil
}

func (r *TestRunner) executeCleanup(calls []ServiceCall) error {
	// Attempt every cleanup action, including after a service failure.
	var errs []error
	for idx, call := range calls {
		domain, service := parseService(call.Service)

		data, err := serviceCallPayload(call)
		if err != nil {
			errs = append(errs, &phaseError{Phase: "cleanup", Index: idx, Service: call.Service, UnderlyingErr: err})
			continue
		}

		if r.verbose {
			fmt.Printf("  Cleanup: %s.%s on %v\n", domain, service, data["entity_id"])
		}

		if err := r.client.CallService(domain, service, data); err != nil {
			errs = append(errs, &phaseError{
				Phase:         "cleanup",
				Index:         idx,
				Service:       call.Service,
				EntityID:      firstEntityFromServiceCall(call),
				UnderlyingErr: fmt.Errorf("failed to call %s.%s: %w", domain, service, err),
			})
		}
		if call.Delay > 0 {
			if err := r.sleep(time.Duration(call.Delay)); err != nil {
				return err
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return fmt.Errorf("cleanup incomplete (%d failures): %s", len(errs), strings.Join(parts, "; "))
}

func (r *TestRunner) snapshotTouchedEntities(tc TestCase, config TestConfig) ([]entitySnapshot, error) {
	if !config.Cleanup || !config.AutoRestoreEnabled() {
		return nil, nil
	}
	entities := touchedEntities(tc)
	direct := directlySeededEntities(tc)
	snapshots := make([]entitySnapshot, 0, len(entities))
	for _, entityID := range entities {
		state, err := r.client.GetEntityState(entityID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				snapshots = append(snapshots, entitySnapshot{EntityID: entityID, Exists: false})
				continue
			}
			return nil, fmt.Errorf("snapshot %s: %w", entityID, err)
		}
		if !direct[entityID] && unsupportedRestoreDomain(getDomain(entityID)) {
			return nil, fmt.Errorf("%s requires explicit cleanup with auto_restore: false; automatic service restoration is unavailable", entityID)
		}
		snapshots = append(snapshots, entitySnapshot{
			EntityID:   entityID,
			State:      state.State,
			Attributes: copyAttributes(state.Attributes),
			Exists:     true,
			Direct:     direct[entityID],
		})
	}
	return snapshots, nil
}

func (r *TestRunner) restoreSnapshots(snapshots []entitySnapshot) error {
	var failures []string
	for _, snapshot := range snapshots {
		if snapshot.Exists {
			if err := r.restoreSnapshot(snapshot); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", snapshot.EntityID, err))
			}
			continue
		}
		if err := r.client.DeleteEntityState(snapshot.EntityID); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", snapshot.EntityID, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func touchedEntities(tc TestCase) []string {
	seen := map[string]bool{}
	add := func(entityID string) {
		if entityID != "" {
			seen[entityID] = true
		}
	}
	for _, action := range tc.Setup {
		add(action.EntityID)
	}
	for _, call := range tc.Trigger {
		for _, entityID := range serviceCallEntityIDs(call) {
			add(entityID)
		}
	}
	for _, assertion := range tc.Assertions {
		add(assertion.EntityID)
	}
	for _, call := range tc.Cleanup {
		for _, entityID := range serviceCallEntityIDs(call) {
			add(entityID)
		}
	}
	entities := make([]string, 0, len(seen))
	for entityID := range seen {
		entities = append(entities, entityID)
	}
	sort.Strings(entities)
	return entities
}

func firstEntityFromServiceCall(call ServiceCall) string {
	entities := serviceCallEntityIDs(call)
	if len(entities) == 0 {
		return ""
	}
	return entities[0]
}

func serviceCallEntityIDs(call ServiceCall) []string {
	var entities []string
	if call.Target.EntityID != "" {
		entities = append(entities, call.Target.EntityID)
	}
	entities = append(entities, call.Target.Entities...)
	entities = append(entities, entityIDsFromAny(call.Data["entity_id"])...)
	sort.Strings(entities)
	return uniqueStringsLocal(entities)
}

func entityIDsFromAny(value interface{}) []string {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, ".") {
			return []string{v}
		}
	case []string:
		var entities []string
		for _, item := range v {
			if strings.Contains(item, ".") {
				entities = append(entities, item)
			}
		}
		return entities
	case []interface{}:
		var entities []string
		for _, item := range v {
			entities = append(entities, entityIDsFromAny(item)...)
		}
		return entities
	case map[string]interface{}:
		var entities []string
		for _, item := range v {
			entities = append(entities, entityIDsFromAny(item)...)
		}
		return entities
	}
	return nil
}

func uniqueStringsLocal(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func copyAttributes(attrs map[string]interface{}) map[string]interface{} {
	if attrs == nil {
		return nil
	}
	copied := make(map[string]interface{}, len(attrs))
	for key, value := range attrs {
		copied[key] = value
	}
	return copied
}

// waitForAssertion polls until state and attributes all match from one response.
func (r *TestRunner) waitForAssertion(assertion Assertion, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(r.client.context(), timeout)
	defer cancel()

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	var lastErr error
	var lastState *EntityState
	var lastAttrErr error
	errorCount := 0

	for {
		select {
		case <-ctx.Done():
			if lastState != nil {
				msg := fmt.Sprintf("timeout waiting for %s to match %q\n  Current: %q\n  Attributes: %+v\n  Last Updated: %s",
					assertion.EntityID, assertion.State, lastState.State, lastState.Attributes, lastState.LastUpdated)
				if lastAttrErr != nil {
					msg += fmt.Sprintf("\n  Attribute mismatch: %v", lastAttrErr)
				}
				return fmt.Errorf("%s", msg)
			} else if lastErr != nil {
				return fmt.Errorf("timeout waiting for %s (last error: %w)", assertion.EntityID, lastErr)
			}
			return fmt.Errorf("timeout waiting for %s to match %q (current: unknown)", assertion.EntityID, assertion.State)

		case <-ticker.C:
			state, err := r.client.WithContext(ctx).GetEntityState(assertion.EntityID)
			if err != nil {
				lastErr = err
				errorCount++
				if errorCount >= MaxConsecutiveErrors {
					return fmt.Errorf("repeated failures getting state for %s: %w", assertion.EntityID, err)
				}
				continue
			}

			errorCount = 0
			lastState = state
			lastAttrErr = nil

			if !matchesStateExpression(state.State, assertion.State) {
				continue
			}
			if len(assertion.Attributes) > 0 {
				if err := verifyAttributes(state.Attributes, assertion.Attributes); err != nil {
					lastAttrErr = err
					continue
				}
			}
			return nil
		}
	}
}

// matchesStateExpression checks if actualState matches the expectedExpr.
// Supports: exact match, ==, !=, >, >=, <, <=, contains, matches (regex)
//
// "contains" ignores case; "matches" uses Go regexp syntax, including inline
// flags, and returns false for invalid patterns. Other operators use matchValue.
func matchesStateExpression(actualState, expectedExpr string) bool {
	op, valueStr := parseExpression(expectedExpr)

	switch op {
	case "contains":
		return strings.Contains(strings.ToLower(actualState), strings.ToLower(valueStr))
	case "matches":
		matched, err := regexp.MatchString(valueStr, actualState)
		return err == nil && matched
	}

	return matchValue(actualState, valueStr, op)
}

// verifyAttributes checks if actual attributes match expected conditions
func verifyAttributes(actual map[string]interface{}, expected map[string]string) error {
	for key, expectedExpr := range expected {
		actualValue, ok := actual[key]
		if !ok {
			return fmt.Errorf("attribute %q not found", key)
		}

		if err := evaluateExpression(actualValue, expectedExpr); err != nil {
			return fmt.Errorf("attribute %q: %w", key, err)
		}
	}
	return nil
}

// evaluateExpression evaluates comparison expressions like "== 255", ">= 100", "370"
// Returns an error if the expression is empty or if the comparison fails.
func evaluateExpression(actual interface{}, expr string) error {
	op, valueStr := parseExpression(expr)

	if valueStr == "" && op == "" {
		return fmt.Errorf("empty expression")
	}

	if op == "" {
		op = "=="
	}

	if matchValue(actual, valueStr, op) {
		return nil
	}

	actualStr := fmt.Sprintf("%v", actual)
	switch op {
	case "==":
		return fmt.Errorf("expected %s, got %s", valueStr, actualStr)
	case "!=":
		return fmt.Errorf("expected not %s, got %s", valueStr, actualStr)
	case ">":
		return fmt.Errorf("expected > %s, got %s", valueStr, actualStr)
	case ">=":
		return fmt.Errorf("expected >= %s, got %s", valueStr, actualStr)
	case "<":
		return fmt.Errorf("expected < %s, got %s", valueStr, actualStr)
	case "<=":
		return fmt.Errorf("expected <= %s, got %s", valueStr, actualStr)
	default:
		return fmt.Errorf("unknown operator %q", op)
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// parseExpression extracts the operator and value from an expression string.
// Returns ("", expr) if no operator is found (treating expr as an exact match value).
// Examples: ">= 50" -> (">=", "50"), "!= off" -> ("!=", "off"), "on" -> ("", "on")
func parseExpression(expr string) (op, value string) {
	expr = strings.TrimSpace(expr)

	// Check two-character operators first
	if strings.HasPrefix(expr, ">=") {
		return ">=", strings.TrimSpace(expr[2:])
	}
	if strings.HasPrefix(expr, "<=") {
		return "<=", strings.TrimSpace(expr[2:])
	}
	if strings.HasPrefix(expr, "==") {
		return "==", strings.TrimSpace(expr[2:])
	}
	if strings.HasPrefix(expr, "!=") {
		return "!=", strings.TrimSpace(expr[2:])
	}
	if strings.HasPrefix(expr, ">") {
		return ">", strings.TrimSpace(expr[1:])
	}
	if strings.HasPrefix(expr, "<") {
		return "<", strings.TrimSpace(expr[1:])
	}
	if strings.HasPrefix(expr, "contains ") {
		return "contains", strings.TrimSpace(expr[9:])
	}
	if strings.HasPrefix(expr, "matches ") {
		return "matches", strings.TrimSpace(expr[8:])
	}

	return "", expr
}

// matchValue compares actual and expected values. Ordering operators require
// numeric values on both sides, so "unavailable >= 17.9" fails. Equality and
// inequality also accept strings.
func matchValue(actual, expected interface{}, op string) bool {
	actualFloat, actualIsNum := toFloat(actual)
	expectedFloat, expectedIsNum := toFloat(expected)

	if actualIsNum && expectedIsNum {
		switch op {
		case "", "==":
			return actualFloat == expectedFloat
		case "!=":
			return actualFloat != expectedFloat
		case ">":
			return actualFloat > expectedFloat
		case ">=":
			return actualFloat >= expectedFloat
		case "<":
			return actualFloat < expectedFloat
		case "<=":
			return actualFloat <= expectedFloat
		default:
			return false
		}
	}

	// Ordering operators require numeric values on both sides.
	switch op {
	case ">", ">=", "<", "<=":
		return false
	}

	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)
	switch op {
	case "", "==":
		return actualStr == expectedStr
	case "!=":
		return actualStr != expectedStr
	default:
		return false
	}
}

// runTraceValidation performs trace validation for a test case.
func (r *TestRunner) runTraceValidation(tc TestCase, triggerTime time.Time, noTraceBaseline map[string]struct{}, result *TestCaseResult) {
	automationID := ""
	if tc.TraceAssertions != nil && tc.TraceAssertions.Automation != "" {
		automationID = tc.TraceAssertions.Automation
	}

	if automationID == "" {
		if r.verbose {
			fmt.Printf("  Trace: skipped (no trace_assertions.automation)\n")
		}
		return
	}
	if tc.TraceAssertions.ExpectNoTrace {
		fetchStart := time.Now()
		if err := r.assertNoTrace(automationID, noTraceBaseline, r.traceContexts(tc.TraceAssertions)...); err != nil {
			result.Status = StatusFailed
			result.Phase = "trace"
			result.TraceAutomation = automationID
			result.TraceError = fmt.Sprintf("Trace assertion failed: expect_no_trace — %v", err)
			if r.verbose {
				fmt.Printf("  Trace: %s EXPECTED NO TRACE (%v)\n", automationID, err)
			}
			return
		}
		result.TraceAutomation = automationID
		result.TraceSummary = fmt.Sprintf("%s no trace observed (waited %.1fs)", automationID, time.Since(fetchStart).Seconds())
		if r.verbose {
			fmt.Printf("  Trace: %s\n", result.TraceSummary)
		}
		return
	}

	fetchStart := time.Now()
	trace, fullTrace, err := r.fetchTraceForValidation(automationID, triggerTime, noTraceBaseline, tc.TraceAssertions)
	fetchLatency := time.Since(fetchStart)

	if err != nil {
		result.Status = StatusFailed
		result.Phase = "trace"
		result.TraceAutomation = automationID
		result.TraceError = fmt.Sprintf("Trace validation failed: %v", err)
		if r.verbose {
			fmt.Printf("  Trace: %s FAILED (%v)\n", automationID, err)
		}
		return
	}

	// Full traces may carry execution errors omitted by the summary response.
	if fullTrace != nil && fullTrace.Error != "" && trace.Error == "" {
		copy := *trace
		copy.Error = fullTrace.Error
		trace = &copy
	}
	traceResult := validateBaseTrace(trace, automationID, fetchLatency)

	if traceResult.Error != "" {
		result.Status = StatusFailed
		result.Phase = "trace"
		result.TraceAutomation = automationID
		result.TraceRunID = trace.RunID
		result.TraceError = traceResult.Error
		if r.verbose {
			fmt.Printf("  Trace: %s run=%s %s\n", automationID, shortRunID(trace.RunID), traceResult.Error)
		}
		return
	}

	if tc.TraceAssertions != nil && hasStructuredAssertions(tc.TraceAssertions) {
		if fullTrace == nil {
			fullTrace, err = r.fetchFullTrace(automationID, trace.RunID)
			if err != nil {
				result.Status = StatusFailed
				result.Phase = "trace"
				result.TraceAutomation = automationID
				result.TraceRunID = trace.RunID
				result.TraceError = fmt.Sprintf("Trace assertion failed: could not fetch full trace: %v", err)
				return
			}
		}

		assertionResults := evaluateTraceAssertions(fullTrace, tc.TraceAssertions)
		traceResult.Assertions = assertionResults

		for _, ar := range assertionResults {
			if !ar.Passed {
				result.Status = StatusFailed
				result.Phase = "trace"
				result.TraceAutomation = automationID
				result.TraceRunID = trace.RunID
				result.TraceError = fmt.Sprintf("Trace assertion failed: %s — %s", ar.Name, ar.Detail)
				if r.verbose {
					fmt.Printf("  Trace: %s run=%s ASSERTION FAILED: %s — %s\n",
						automationID, shortRunID(trace.RunID), ar.Name, ar.Detail)
				}
				return
			}
		}
	}

	summary := fmt.Sprintf("%s run=%s %s @ %s (fetch: %.1fs)",
		automationID, shortRunID(trace.RunID), trace.State, trace.LastStep, fetchLatency.Seconds())
	result.TraceSummary = summary
	result.TraceAutomation = automationID
	result.TraceRunID = trace.RunID

	if r.verbose {
		fmt.Printf("  Trace: %s\n", summary)
	}
}

// hasStructuredAssertions returns true if TraceAssertions has any fields
// beyond the automation ID that require full trace inspection.
func hasStructuredAssertions(ta *TraceAssertions) bool {
	if ta == nil {
		return false
	}
	return ta.ExpectCompleted != nil ||
		ta.ExpectNoTrace ||
		ta.ExpectNoErrors != nil ||
		ta.ExpectBranch != "" ||
		len(ta.ExpectActions) > 0 ||
		len(ta.ExpectActionsInOrder) > 0 ||
		len(ta.RejectActions) > 0
}

// shortRunID returns the first 16 characters of a run ID for display.
func shortRunID(runID string) string {
	if len(runID) > 16 {
		return runID[:16]
	}
	return runID
}

// Close cleans up resources including the WebSocket client if initialized.
func (r *TestRunner) Close() error {
	if r.wsClient != nil {
		return r.wsClient.Close()
	}
	return nil
}

func (r *TestRunner) sleep(delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-r.client.context().Done():
		return r.client.context().Err()
	case <-timer.C:
		return nil
	}
}
