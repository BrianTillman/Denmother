package cmd

import (
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
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
