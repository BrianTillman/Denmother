package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTestPlanSummaryForBlueprintChange(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/blueprints/automation/example/demo.yaml", `
blueprint:
  name: Demo
  domain: automation
`)
	writeTestFile(t, "ha-config/automations/presence/office_lights.yaml", `
- alias: Office Lights
  use_blueprint:
    path: example/demo.yaml
`)
	writeTestFile(t, "ha-config/tests/presence/office_lights_test.yaml", `
version: 1
name: Office Lights
tags:
  - fast
config:
  timeout: 1s
  cleanup: true
tests:
  - name: "smoke"
    trigger:
      - service: automation.trigger
        target:
          entity_id: automation.office_lights
    assertions:
      - entity_id: light.office
        state: "on"
`)

	summary, err := buildTestPlanSummary([]string{"ha-config/blueprints/automation/example/demo.yaml"})
	if err != nil {
		t.Fatalf("buildTestPlanSummary: %v", err)
	}
	if len(summary.TestFiles) != 1 || summary.TestFiles[0].Path != "ha-config/tests/presence/office_lights_test.yaml" {
		t.Fatalf("TestFiles = %+v", summary.TestFiles)
	}
	if !containsString(summary.BroadCommands, "./dm check dev --json") {
		t.Fatalf("BroadCommands = %#v, want dev check", summary.BroadCommands)
	}
	if !containsString(summary.Commands, "./dm test doctor --compact --json") {
		t.Fatalf("Commands = %#v, want compact doctor command", summary.Commands)
	}
	if !containsString(summary.Commands, "./dm test --changed --json") {
		t.Fatalf("Commands = %#v, want changed test command", summary.Commands)
	}
	if len(summary.ExpandedCommands) != 1 || summary.ExpandedCommands[0] == "./dm test --changed --json" {
		t.Fatalf("ExpandedCommands = %#v, want expanded file command", summary.ExpandedCommands)
	}
}

func TestBuildTestPlanSummarySkipsDeletedTestFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/tests/presence/still_here_test.yaml", `
version: 1
name: Still Here
tags:
  - fast
config:
  timeout: 1s
  cleanup: true
tests:
  - name: "smoke"
    trigger:
      - service: homeassistant.update_entity
        target:
          entity_id: light.office
    assertions:
      - entity_id: light.office
        state: "on"
`)

	summary, err := buildTestPlanSummary([]string{
		"ha-config/tests/binding/retired_mirror_test.yaml",
		"ha-config/tests/presence/still_here_test.yaml",
	})
	if err != nil {
		t.Fatalf("buildTestPlanSummary: %v", err)
	}
	if len(summary.TestFiles) != 1 || summary.TestFiles[0].Path != "ha-config/tests/presence/still_here_test.yaml" {
		t.Fatalf("TestFiles = %+v, want only still_here_test.yaml", summary.TestFiles)
	}
	found := false
	for _, reason := range summary.Reasons {
		if strings.Contains(reason, "retired_mirror_test.yaml is deleted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Reasons = %#v, want deleted-test note", summary.Reasons)
	}
}

func TestBuildTestPlanSummarySkipsPresentationOnlyEntityReferences(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/homekit/bridge-switches.yaml", `
- name: HASS Bridge Switches
  filter:
    include_entities:
      - input_boolean.vibe_sleep
`)
	writeTestFile(t, "ha-config/dashboards/kitchen-kiosk.yaml", `
views:
  - cards:
      - type: tile
        entity: input_boolean.vibe_sleep
`)
	writeTestFile(t, "ha-config/tests/presence/office_lights_test.yaml", `
version: 1
name: Office Lights
tests:
  - name: vibe
    assertions:
      - entity_id: input_boolean.vibe_sleep
        state: "off"
`)

	summary, err := buildTestPlanSummary([]string{
		"ha-config/homekit/bridge-switches.yaml",
		"ha-config/dashboards/kitchen-kiosk.yaml",
	})
	if err != nil {
		t.Fatalf("buildTestPlanSummary: %v", err)
	}
	if len(summary.TestFiles) != 0 {
		t.Fatalf("TestFiles = %+v, want none", summary.TestFiles)
	}
	if !containsString(summary.BroadCommands, "./dm check --json") {
		t.Fatalf("BroadCommands = %#v, want config check", summary.BroadCommands)
	}
}

func TestBuildTestDoctorSummaryFindsStaticIssues(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/automations/presence/office_lights.yaml", `
- alias: Office Lights
`)
	writeTestFile(t, "ha-config/tests/presence/office_lights_test.yaml", `
version: 1
name: Office Lights
tags:
  - presence
config:
  timeout: 1s
  cleanup: true
tests:
  - name: "trigger smoke"
    trigger:
      - service: automation.trigger
        target:
          entity_id: automation.office_lights
    assertions:
      - entity_id: light.office
        state: "on"
`)

	summary, err := buildTestDoctorSummary()
	if err != nil {
		t.Fatalf("buildTestDoctorSummary: %v", err)
	}
	for _, id := range []string{"test-missing-fast-tag", "test-uses-automation-trigger", "test-explicit-cleanup-gap"} {
		if !hasAgentIssue(summary.Issues, id) {
			t.Fatalf("expected %s in issues, got %+v", id, summary.Issues)
		}
	}
	for _, id := range []string{"test-missing-fast-tag", "test-uses-automation-trigger", "test-explicit-cleanup-gap"} {
		if !hasDoctorIssueGroup(summary.IssueGroups, id) {
			t.Fatalf("expected %s in issue groups, got %+v", id, summary.IssueGroups)
		}
	}
	if len(summary.FilesWithIssues) != 1 || summary.FilesWithIssues[0].File != "ha-config/tests/presence/office_lights_test.yaml" {
		t.Fatalf("FilesWithIssues = %+v", summary.FilesWithIssues)
	}

	compactStep := testDoctorStep(summary, true)
	if _, ok := compactStep.Details["failures"]; ok {
		t.Fatal("compact doctor step should omit detailed failures")
	}
	if _, ok := compactStep.Details["issue_groups"]; !ok {
		t.Fatal("compact doctor step should include issue_groups")
	}
	fullStep := testDoctorStep(summary, false)
	if _, ok := fullStep.Details["failures"]; !ok {
		t.Fatal("full doctor step should include detailed failures")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasDoctorIssueGroup(groups []testDoctorIssueGroup, id string) bool {
	for _, group := range groups {
		if group.ID == id {
			return true
		}
	}
	return false
}

func TestDoctorDocumentedIntegrationAndManualCoverage(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, "ha-config/automations/time/evening.yaml", "- alias: Evening\n")
	writeTestFile(t, "ha-config/tests/coverage_test.yaml", `
version: 1
name: Deliberate coverage
tags: [fast]
config:
  cleanup: true
tests:
  - name: Derived sensor
    trace_skip_reason: Template sensor test has no automation trace.
    trigger:
      - service: mock_entities.set_state
        data:
          entity_id: sensor.source
          state: "10"
    assertions:
      - entity_id: sensor.derived
        state: "20"
    cleanup:
      - service: mock_entities.set_state
        data:
          entity_id: sensor.source
          state: "0"
  - name: Manual evening entry
    automation_trigger_reason: Intentional manual entry-point test.
    trigger:
      - service: automation.trigger
        target:
          entity_id: automation.evening
    assertions:
      - entity_id: automation.evening
        state: "on"
`)
	summary, err := buildTestDoctorSummary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.IssueCount != 0 {
		t.Fatalf("unexpected findings: %+v", summary.Issues)
	}
}

func TestPublicSourceChangesRecommendGoTests(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"cmd/root.go", "internal/operator/target.go", "main.go", "go.mod", "go.sum"} {
		if goTestCommandForChange(path) != "go test ./..." {
			t.Errorf("missed Go source change %s", path)
		}
	}
	if goTestCommandForChange("ha-config/helpers/input.yaml") != "" {
		t.Fatal("unrelated configuration selected Go suite")
	}
	commands := reviewNextCommands([]string{"cmd/root.go"}, nil)
	found := false
	for _, command := range commands {
		if command == "go test ./..." {
			found = true
		}
	}
	if !found {
		t.Fatal("source review did not recommend standalone Go suite")
	}
}
