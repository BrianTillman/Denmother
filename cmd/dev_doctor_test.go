package cmd

import (
	"fmt"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"strings"
	"testing"
)

func TestCollectDevLogFindingsDetectsMockEntityServiceTargetFailures(t *testing.T) {
	findings := collectDevLogFindings(`
Entity id already exists - ignoring: vacuum.scoomba
Mock entity service target unavailable: vacuum.scoomba
`)

	want := map[string]int{
		"duplicate-entity-id":        1,
		"service-target-unavailable": 1,
	}
	for _, finding := range findings {
		if count, ok := want[finding.RuleID]; ok {
			if finding.Count != count {
				t.Fatalf("%s count = %d, want %d", finding.RuleID, finding.Count, count)
			}
			delete(want, finding.RuleID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing findings: %v", want)
	}
}

func TestCollectDevLogFindingsIgnoresHealthyMockEntityDiagnostics(t *testing.T) {
	findings := collectDevLogFindings(`
Mock entity service target check passed for 5200 entity references
Mock Entities integration setup complete with testing services
`)
	if len(findings) != 0 {
		t.Fatalf("healthy diagnostics produced findings: %#v", findings)
	}
}

func TestDoctorDistinguishesSyntheticDisplayEvidence(t *testing.T) {
	step := localStateDoctorStep([]hasync.EntityState{{EntityID: "sensor.study", State: "21", Attributes: map[string]any{"denmother_source": "fixture"}}}, []string{"sensor.study"})
	if step.Status != operator.StatusWarning || len(step.Details["synthetic_entity_ids"].([]string)) != 1 {
		t.Fatalf("%+v", step)
	}
}

func TestCollectDevLogFindingsGenericHAErrors(t *testing.T) {
	lines := []string{
		"2026-01-01 12:00:00 ERROR (MainThread) [homeassistant.components.automation.example] Error executing script: invalid option",
		"2026-01-01 12:00:01 ERROR (MainThread) [homeassistant.helpers.script.example] Error for call_service at pos 1",
		"2026-01-01 12:00:02 ERROR (MainThread) [homeassistant.core] Error doing job: Task exception was never retrieved",
	}
	for _, line := range lines {
		findings := collectDevLogFindings(line)
		if len(findings) != 1 || findings[0].Count != 1 {
			t.Fatalf("missing generic error: %q: %+v", line, findings)
		}
	}
	healthy := "2026-01-01 12:00:00 INFO (MainThread) [homeassistant.core] No ERROR records since restart"
	if findings := collectDevLogFindings(healthy); len(findings) != 0 {
		t.Fatalf("message text is not the log level: %+v", findings)
	}
}

func TestCollectDevLogFindingsDeduplicatesAndBoundsSamples(t *testing.T) {
	line := "2026-01-01 ERROR (MainThread) [homeassistant.components.automation.example] failure"
	findings := collectDevLogFindings(strings.Repeat(line+"\n", 20))
	if len(findings) != 1 || findings[0].Count != 20 || len(findings[0].Samples) != 1 {
		t.Fatalf("repeated errors: %+v", findings)
	}
	var logs strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&logs, "%s %d\n", line, i)
	}
	findings = collectDevLogFindings(logs.String())
	if len(findings) != 1 || findings[0].Count != 12 || len(findings[0].Samples) != 8 {
		t.Fatalf("sample bound: %+v", findings)
	}
	findings = collectDevLogFindings("2026-01-01 ERROR [custom_components.example] Traceback (most recent call last):")
	if len(findings) != 1 {
		t.Fatalf("one line counted under multiple rules: %+v", findings)
	}
}
