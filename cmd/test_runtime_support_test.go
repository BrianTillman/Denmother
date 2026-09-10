package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTestPlanExplainsDynamicTargetCoverage(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	oldConfig, oldExplicit := configPath, configExplicit
	configPath, configExplicit = filepath.Join(root, "ha-config"), true
	defer func() { configPath, configExplicit = oldConfig, oldExplicit }()
	path := filepath.Join(configPath, "scripts", "dynamic.yaml")
	writeTestFile(t, path, "dynamic:\n  sequence:\n    - action: light.turn_on\n      target:\n        entity_id: '{{ target_light }}'\n")
	summary, err := buildTestPlanSummary([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, reason := range summary.Reasons {
		if strings.Contains(reason, "Jinja") && strings.Contains(reason, "runtime") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unexplained dynamic coverage: %v", summary.Reasons)
	}
	if !containsString(summary.BroadCommands, "./dm test --json") {
		t.Fatalf("dynamic target did not broaden verification: %v", summary.BroadCommands)
	}
}

func TestDoctorExplainsNativeTargetAndDelayedAttribution(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	oldConfig, oldExplicit := configPath, configExplicit
	configPath, configExplicit = filepath.Join(root, "ha-config"), true
	defer func() { configPath, configExplicit = oldConfig, oldExplicit }()
	writeTestFile(t, filepath.Join(configPath, "tests", "device_test.yaml"), `version: 1
name: Runtime targets
tags: [fast]
config:
  cleanup: false
tests:
  - name: area timer
    trigger:
      - service: timer.start
        target:
          area_id: test_room
    assertions:
      - entity_id: input_boolean.result
        state: "on"
    trace_assertions:
      automation: automation.test_timer
      attribution: fresh_unique
      attribution_reason: isolated timer.finished loses command context
`)
	summary, err := buildTestDoctorSummary()
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.RuntimeRequirements) != 2 {
		t.Fatalf("missing runtime explanations: %+v", summary.RuntimeRequirements)
	}
	step := testDoctorStep(summary, true)
	if _, ok := step.Details["runtime_requirements"]; !ok {
		t.Fatal("compact JSON omits runtime limits")
	}
}
