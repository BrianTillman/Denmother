package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
	"gopkg.in/yaml.v3"
)

func TestAgentLintStepFailsOnErrorIssue(t *testing.T) {
	t.Parallel()

	step := agentLintStep(lintSummary{
		DocsChecked: 1,
		Issues: []agentIssue{
			{
				ID:       "tracked-secret-like-value",
				Severity: "error",
				File:     ".config/settings.json",
				Summary:  "tracked file contains a token-like literal",
			},
		},
	})

	if step.Status != operator.StatusFailure {
		t.Fatalf("step.Status = %q, want failure", step.Status)
	}
	if _, ok := step.Details["failures"]; !ok {
		t.Fatal("expected failures in details")
	}
}

func TestBrokenLinksSkipsExternalTargets(t *testing.T) {
	t.Parallel()

	issues := brokenLinksInFile("docs/agents/README.md", "[Example](https://example.com/guide)")
	if len(issues) != 0 {
		t.Fatalf("expected no issues for external link, got %+v", issues)
	}
}

func TestMissingRootCommandMentions(t *testing.T) {
	t.Parallel()

	missing := missingRootCommandMentions("Run ./dm check and dm observe automation.foo", []string{"check", "observe", "plan"})
	if len(missing) != 1 || missing[0] != "plan" {
		t.Fatalf("missing = %#v, want plan", missing)
	}
}

func TestMissingAgentCommandMentions(t *testing.T) {
	t.Parallel()

	missing := missingAgentCommandMentions("Run ./dm agent next --json and dm agent tasks --json", []string{"next", "tasks", "lint"})
	if len(missing) != 1 || missing[0] != "lint" {
		t.Fatalf("missing = %#v, want lint", missing)
	}
}

func TestDocumentedRootCommandsReflectRegisteredCommands(t *testing.T) {
	t.Parallel()

	commands := documentedRootCommands()
	for _, want := range []string{"agent", "dev", "observe", "plan"} {
		found := false
		for _, got := range commands {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("documentedRootCommands missing %q in %#v", want, commands)
		}
	}
}

func TestExpectedAutomationTest(t *testing.T) {
	t.Parallel()

	got := expectedAutomationTest("ha-config/automations/presence/office_lights.yaml")
	want := "ha-config/tests/presence/office_lights_test.yaml"
	if got != want {
		t.Fatalf("expectedAutomationTest() = %q, want %q", got, want)
	}
}

func TestAgentReviewStepWarnsOnWarningIssue(t *testing.T) {
	t.Parallel()

	step := agentReviewStep(reviewSummary{
		ChangedFiles: []string{"ha-config/zigbee2mqtt/configuration.yaml"},
		Issues: []agentIssue{
			{
				ID:       "production-risk-review",
				Severity: "warning",
				File:     "ha-config/zigbee2mqtt/configuration.yaml",
				Summary:  "Zigbee2MQTT runtime configuration changed",
			},
		},
	})

	if step.Status != operator.StatusWarning {
		t.Fatalf("step.Status = %q, want warning", step.Status)
	}
	if _, ok := step.Details["failures"]; !ok {
		t.Fatal("expected failures in details")
	}
}

func TestReviewAutomationTestsSkipsDeletedAutomation(t *testing.T) {
	t.Parallel()

	issues := reviewAutomationTests([]string{"ha-config/automations/presence/deleted_automation.yaml"})
	if len(issues) != 0 {
		t.Fatalf("len(issues) = %d, want 0", len(issues))
	}
}

func TestFinalAgentReviewStatusAllowsWarningsWhenRequested(t *testing.T) {
	t.Parallel()

	if got := finalAgentReviewStatus(operator.StatusWarning, true); got != operator.StatusSuccess {
		t.Fatalf("finalAgentReviewStatus(warning, true) = %q, want success", got)
	}
	if got := finalAgentReviewStatus(operator.StatusFailure, true); got != operator.StatusFailure {
		t.Fatalf("finalAgentReviewStatus(failure, true) = %q, want failure", got)
	}
	if got := finalAgentReviewStatus(operator.StatusWarning, false); got != operator.StatusWarning {
		t.Fatalf("finalAgentReviewStatus(warning, false) = %q, want warning", got)
	}
}

func TestNormalizeChangedFilesDeduplicatesAndHandlesRenames(t *testing.T) {
	t.Parallel()

	got := normalizeChangedFiles([]string{
		" ha-config/automations/presence/office_lights.yaml ",
		"ha-config/automations/presence/office_lights.yaml",
		"docs/old.md -> docs/new.md",
		"",
	})
	want := []string{
		"docs/new.md",
		"ha-config/automations/presence/office_lights.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeChangedFiles() = %#v, want %#v", got, want)
	}
}

func TestHomeKitBridgeMappingsHandlesSequenceConfig(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
- name: Test Bridge
  filter:
    include_entities:
      - light.office
  entity_config:
    light.office:
      name: Office
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mappings := homeKitBridgeMappings(&doc)
	if len(mappings) != 1 {
		t.Fatalf("len(mappings) = %d, want 1", len(mappings))
	}
	filter := mapValue(mappings[0], "filter")
	entities := yamlStringSequence(mapValue(filter, "include_entities"))
	if len(entities) != 1 || entities[0] != "light.office" {
		t.Fatalf("entities = %#v, want light.office", entities)
	}
}

func TestStaleGeneratedDocIssues(t *testing.T) {
	t.Parallel()

	issues := staleGeneratedDocIssues([]string{"docs/generated/automation-index.md"})
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	if issues[0].ID != "generated-doc-stale" || issues[0].Severity != "error" {
		t.Fatalf("unexpected issue: %+v", issues[0])
	}
}

func TestLintRawStatesEntityState(t *testing.T) {
	t.Parallel()

	issues := lintRawStatesEntityState("ha-config/automations/test.yaml", "{{ states.light.office.state }}")
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	if issues[0].ID != "raw-states-entity-state" {
		t.Fatalf("issue ID = %q, want raw-states-entity-state", issues[0].ID)
	}
}

func TestSensitiveHomeKitEntity(t *testing.T) {
	t.Parallel()

	for _, entity := range []string{"lock.front_door", "cover.garage_main_door", "camera.driveway"} {
		if !isSensitiveHomeKitEntity(entity) {
			t.Fatalf("expected %s to be sensitive", entity)
		}
	}
	if isSensitiveHomeKitEntity("light.office_lamp") {
		t.Fatal("did not expect light.office_lamp to be sensitive")
	}
}

func TestQualityGrade(t *testing.T) {
	t.Parallel()

	tests := map[int]string{
		96: "A",
		90: "B",
		80: "C",
		70: "D",
		40: "F",
	}
	for score, want := range tests {
		if got := qualityGrade(score); got != want {
			t.Fatalf("qualityGrade(%d) = %q, want %q", score, got, want)
		}
	}
}

func TestLocalTestReadinessQualityMetricIsStatic(t *testing.T) {
	t.Parallel()

	metric := localTestReadinessQualityMetric()
	if metric.ID != "local_test_readiness" {
		t.Fatalf("ID = %q, want local_test_readiness", metric.ID)
	}
	if strings.Contains(metric.Summary, "reachable") {
		t.Fatalf("summary should not depend on live HA reachability: %q", metric.Summary)
	}
	if metric.Signals["runtime_check"] != "./dm dev status --json" {
		t.Fatalf("runtime_check = %v", metric.Signals["runtime_check"])
	}
}

func TestLintOvernightTimeWindows(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
condition: time
after: "22:00"
before: "06:00"
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	issues := lintOvernightTimeWindows("ha-config/automations/test.yaml", &doc)
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	if issues[0].ID != "overnight-time-window" {
		t.Fatalf("issue ID = %q", issues[0].ID)
	}
}

func TestLintZigbee2MQTTFilteredUpdateAttribute(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
devices:
  '0x001':
    friendly_name: Test Dimmer
    filtered_attributes:
      - ^linkquality$
      - ^update$
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	issues := lintZigbee2MQTTFilteredUpdateAttribute("ha-config/zigbee2mqtt/configuration.yaml", &doc)
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1", len(issues))
	}
	if issues[0].ID != "z2m-filtered-update-attribute" {
		t.Fatalf("issue ID = %q, want z2m-filtered-update-attribute", issues[0].ID)
	}
	if !strings.Contains(issues[0].Hint, "issue #9") {
		t.Fatalf("hint %q does not mention issue #9", issues[0].Hint)
	}
}

func TestLintZigbee2MQTTInovelliNoisyAttributeFilters(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
devices:
  '0x001':
    friendly_name: Office Bathroom Vanity
    filtered_attributes:
      - ^linkquality$
      - ^mmwave_targets$
  '0x002':
    friendly_name: Living Room Fan Dimmer
    filtered_attributes:
      - ^linkquality$
      - ^mmwave_targets$
      - ^defaultLed[1-7]ColorWhenOff$
      - ^defaultLed[1-7]IntensityWhenOn$
      - ^defaultLed[1-7]IntensityWhenOff$
      - ^mmWaveVersion$
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	issues := lintZigbee2MQTTInovelliNoisyAttributeFilters("ha-config/zigbee2mqtt/configuration.yaml", &doc)
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1: %+v", len(issues), issues)
	}
	if issues[0].ID != "z2m-inovelli-noisy-attribute-filter-missing" {
		t.Fatalf("issue ID = %q", issues[0].ID)
	}
	if !strings.Contains(issues[0].Summary, "Office Bathroom Vanity") {
		t.Fatalf("summary %q does not name the affected device", issues[0].Summary)
	}
}

func TestLintZigbee2MQTTInovelliNoisyDiscoveryOverrides(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
device_options:
  homeassistant: &optional_discovery_disabled
    defaultLed1ColorWhenOff: null
    defaultLed1IntensityWhenOff: null
    defaultLed1IntensityWhenOn: null
    defaultLed2ColorWhenOff: null
    defaultLed2IntensityWhenOff: null
    defaultLed2IntensityWhenOn: null
    defaultLed3ColorWhenOff: null
    defaultLed3IntensityWhenOff: null
    defaultLed3IntensityWhenOn: null
    defaultLed4ColorWhenOff: null
    defaultLed4IntensityWhenOff: null
    defaultLed4IntensityWhenOn: null
    defaultLed5ColorWhenOff: null
    defaultLed5IntensityWhenOff: null
    defaultLed5IntensityWhenOn: null
    defaultLed6ColorWhenOff: null
    defaultLed6IntensityWhenOff: null
    defaultLed6IntensityWhenOn: null
    defaultLed7ColorWhenOff: null
    defaultLed7IntensityWhenOff: null
    defaultLed7IntensityWhenOn: null
    mmWaveVersion: null
devices:
  '0x001':
    friendly_name: Global Defaults
    filtered_attributes:
      - ^mmwave_targets$
  '0x002':
    friendly_name: Merged Defaults
    filtered_attributes:
      - ^mmwave_targets$
    homeassistant:
      <<: *optional_discovery_disabled
      device:
        suggested_area: Office
  '0x003':
    friendly_name: Missing Overrides
    filtered_attributes:
      - ^mmwave_targets$
    homeassistant:
      device:
        suggested_area: Kitchen
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	issues := lintZigbee2MQTTInovelliNoisyDiscoveryOverrides("ha-config/zigbee2mqtt/configuration.yaml", &doc)
	if len(issues) != 1 {
		t.Fatalf("len(issues) = %d, want 1: %+v", len(issues), issues)
	}
	if issues[0].ID != "z2m-inovelli-noisy-discovery-override-missing" {
		t.Fatalf("issue ID = %q", issues[0].ID)
	}
	if !strings.Contains(issues[0].Summary, "Missing Overrides") {
		t.Fatalf("summary %q does not name the affected device", issues[0].Summary)
	}
}

func TestLintBlueprintMetadata(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(`
blueprint:
  name: Test Blueprint
  domain: automation
`), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if issues := lintBlueprintMetadata("ha-config/blueprints/automation/test.yaml", &doc); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}

	var missing yaml.Node
	if err := yaml.Unmarshal([]byte(`blueprint: {name: Missing Domain}`), &missing); err != nil {
		t.Fatalf("unmarshal missing: %v", err)
	}
	issues := lintBlueprintMetadata("ha-config/blueprints/automation/test.yaml", &missing)
	if len(issues) != 1 || issues[0].ID != "blueprint-domain-not-automation" {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestCheckFanLightPairDeviceContractsPassesMatchingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, ".denmother.yaml", "version: 1\nconfig_dir: ha-config\nfan_light_pair_blueprint: example/inovelli_fan_light_dimmer_pair.yaml\n")
	oldConfigPath := configPath
	configPath = "ha-config"
	defer func() { configPath = oldConfigPath }()

	writeFanLightPairSettingsBlueprint(t)
	writeTestFile(t, "ha-config/automations/config/test_dimmer.yaml", `
- id: config_test_dimmer
  alias: Config Test Dimmer
  use_blueprint:
    path: example/z2m_inovelli_blue_dimmer_settings.yaml
    input:
      z2m_friendly_name: Test Dimmer
      smart_bulb_mode: Smart Bulb Mode
`)
	writeTestFile(t, "ha-config/automations/controls/test_pair.yaml", `
- id: controls_test_pair
  alias: Test Pair
  use_blueprint:
    path: example/inovelli_fan_light_dimmer_pair.yaml
    input:
      z2m_friendly_name: Test Dimmer
`)

	if issues := checkFanLightPairDeviceContracts(); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}

func TestFanLightPairRequiresExplicitBlueprintPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	previous := configPath
	configPath = "ha-config"
	t.Cleanup(func() { configPath = previous })
	writeTestFile(t, "ha-config/automations/pair.yaml", `
- alias: Pair
  use_blueprint:
    path: custom/controls.yaml
`)
	if issues := checkFanLightPairDeviceContracts(); len(issues) != 0 {
		t.Fatalf("unconfigured policy inspected an unrelated blueprint: %+v", issues)
	}
	writeTestFile(t, ".denmother.yaml", "version: 1\nconfig_dir: ha-config\nfan_light_pair_blueprint: custom/controls.yaml\n")
	if issues := checkFanLightPairDeviceContracts(); !hasAgentIssue(issues, "fan-light-pair-missing-z2m-name") {
		t.Fatalf("explicit policy did not diagnose the missing device name: %+v", issues)
	}
}

func TestCheckFanLightPairDeviceContractsFlagsMissingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, ".denmother.yaml", "version: 1\nconfig_dir: ha-config\nfan_light_pair_blueprint: example/inovelli_fan_light_dimmer_pair.yaml\n")
	oldConfigPath := configPath
	configPath = "ha-config"
	defer func() { configPath = oldConfigPath }()

	writeTestFile(t, "ha-config/automations/controls/test_pair.yaml", `
- id: controls_test_pair
  alias: Test Pair
  use_blueprint:
    path: example/inovelli_fan_light_dimmer_pair.yaml
    input:
      z2m_friendly_name: Missing Dimmer
`)

	issues := checkFanLightPairDeviceContracts()
	if !hasAgentIssue(issues, "fan-light-pair-config-missing") {
		t.Fatalf("expected missing config issue, got %+v", issues)
	}
}

func TestCheckFanLightPairDeviceContractsFlagsDuplicateConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, ".denmother.yaml", "version: 1\nconfig_dir: ha-config\nfan_light_pair_blueprint: example/inovelli_fan_light_dimmer_pair.yaml\n")
	oldConfigPath := configPath
	configPath = "ha-config"
	defer func() { configPath = oldConfigPath }()

	writeFanLightPairSettingsBlueprint(t)
	writeTestFile(t, "ha-config/automations/config/test_dimmer_one.yaml", `
- id: config_test_dimmer_one
  alias: Config Test Dimmer One
  use_blueprint:
    path: example/z2m_inovelli_blue_dimmer_settings.yaml
    input:
      z2m_friendly_name: Test Dimmer
      smart_bulb_mode: Smart Bulb Mode
      output_mode: Dimmer
      button_delay: 500ms
      fan_control_mode: Disabled
`)
	writeTestFile(t, "ha-config/automations/config/test_dimmer_two.yaml", `
- id: config_test_dimmer_two
  alias: Config Test Dimmer Two
  use_blueprint:
    path: example/z2m_inovelli_blue_dimmer_settings.yaml
    input:
      z2m_friendly_name: Test Dimmer
      smart_bulb_mode: Smart Bulb Mode
      output_mode: Dimmer
      button_delay: 500ms
      fan_control_mode: Disabled
`)
	writeTestFile(t, "ha-config/automations/controls/test_pair.yaml", `
- id: controls_test_pair
  alias: Test Pair
  use_blueprint:
    path: example/inovelli_fan_light_dimmer_pair.yaml
    input:
      z2m_friendly_name: Test Dimmer
`)

	issues := checkFanLightPairDeviceContracts()
	if !hasAgentIssue(issues, "fan-light-pair-config-duplicate") {
		t.Fatalf("expected duplicate config issue, got %+v", issues)
	}
}

func TestCheckFanLightPairDeviceContractsFlagsWrongResolvedParams(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, ".denmother.yaml", "version: 1\nconfig_dir: ha-config\nfan_light_pair_blueprint: example/inovelli_fan_light_dimmer_pair.yaml\n")
	oldConfigPath := configPath
	configPath = "ha-config"
	defer func() { configPath = oldConfigPath }()

	writeFanLightPairSettingsBlueprint(t)
	writeTestFile(t, "ha-config/automations/config/test_dimmer.yaml", `
- id: config_test_dimmer
  alias: Config Test Dimmer
  use_blueprint:
    path: example/z2m_inovelli_blue_dimmer_settings.yaml
    input:
      z2m_friendly_name: Test Dimmer
      smart_bulb_mode: Smart Bulb Mode
      output_mode: On/Off
      button_delay: 0ms
      fan_control_mode: Disabled
`)
	writeTestFile(t, "ha-config/automations/controls/test_pair.yaml", `
- id: controls_test_pair
  alias: Test Pair
  use_blueprint:
    path: example/inovelli_fan_light_dimmer_pair.yaml
    input:
      z2m_friendly_name: Test Dimmer
`)

	issues := checkFanLightPairDeviceContracts()
	if !hasAgentIssue(issues, "fan-light-pair-config-param-mismatch") {
		t.Fatalf("expected param mismatch issue, got %+v", issues)
	}
}

func TestCheckInovelliHousePolicyScheduleCoversEveryConsumerOnce(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	oldConfigPath := configPath
	configPath = "ha-config"
	defer func() { configPath = oldConfigPath }()

	writeTestFile(t, "ha-config/automations/config/test_dimmer.yaml", `
- id: config_test_dimmer
  alias: Config Test Dimmer
  use_blueprint:
    path: example/z2m_inovelli_blue_dimmer_settings.yaml
    input:
      device_model: VZM31-SN
      z2m_friendly_name: Test Dimmer
`)
	writeTestFile(t, "ha-config/automations/config/test_canopy.yaml", `
- id: config_test_canopy
  alias: Config Test Canopy
  use_blueprint:
    path: example/z2m_inovelli_fan_canopy_settings.yaml
    input:
      z2m_friendly_name: Test Canopy
`)
	writeTestFile(t, "ha-config/automations/config/inovelli_z2m_house_policy_schedule.yaml", `
- id: config_inovelli_z2m_house_policy_schedule
  alias: Config Inovelli Z2M House Policy Schedule
  actions:
    - variables:
        policy_automations:
          - automation.config_test_dimmer
          - automation.config_test_canopy
`)

	if issues := checkInovelliHousePolicySchedule(); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}

	writeTestFile(t, "ha-config/automations/config/inovelli_z2m_house_policy_schedule.yaml", `
- id: config_inovelli_z2m_house_policy_schedule
  alias: Config Inovelli Z2M House Policy Schedule
  actions:
    - variables:
        policy_automations:
          - automation.config_test_dimmer
          - automation.config_test_dimmer
          - automation.config_retired
`)
	issues := checkInovelliHousePolicySchedule()
	for _, id := range []string{
		"inovelli-house-policy-automation-unscheduled",
		"inovelli-house-policy-automation-duplicated",
		"inovelli-house-policy-automation-unknown",
	} {
		if !hasAgentIssue(issues, id) {
			t.Fatalf("expected %s, got %+v", id, issues)
		}
	}
}

func TestCheckConfigReferenceDefects(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/configuration.yaml", `
lovelace:
  dashboards:
    existing-yaml:
      mode: yaml
      filename: dashboards/existing.yaml
    missing-yaml:
      mode: yaml
      filename: dashboards/missing.yaml
`)
	writeTestFile(t, "ha-config/dashboards/existing.yaml", "title: Existing\n")
	writeTestFile(t, "ha-config/blueprints/automation/example/used.yaml", `
blueprint:
  name: Used
  domain: automation
`)
	writeTestFile(t, "ha-config/blueprints/automation/example/unused.yaml", `
blueprint:
  name: Unused
  domain: automation
`)
	writeTestFile(t, "ha-config/automations/test.yaml", `
- alias: Uses Existing Blueprint
  use_blueprint:
    path: example/used.yaml
- alias: Uses Missing Blueprint
  use_blueprint:
    path: example/missing.yaml
`)
	writeTestFile(t, "docs/guide.md", "Use `example/missing_doc.yaml` for the old flow.\n")

	issues := checkConfigReferenceDefects()
	for _, id := range []string{
		"lovelace-dashboard-missing-file",
		"automation-blueprint-missing-file",
		"automation-blueprint-unused",
		"blueprint-doc-missing-file",
	} {
		if !hasAgentIssue(issues, id) {
			t.Fatalf("expected %s in issues, got %+v", id, issues)
		}
	}
}

func TestReadActiveTechDebtItems(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tech-debt.md")
	if err := os.WriteFile(path, []byte(`# Agent Tech Debt

## Active

- First item
  continued
- Second item

## Completed

- Done
`), 0644); err != nil {
		t.Fatalf("write tech debt: %v", err)
	}
	items, err := readActiveTechDebtItems(path)
	if err != nil {
		t.Fatalf("readActiveTechDebtItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0] != "First item continued" || items[1] != "Second item" {
		t.Fatalf("items = %#v", items)
	}
}

func TestNormalizePlanSlug(t *testing.T) {
	t.Parallel()

	got, err := normalizePlanSlug("My_Plan.md")
	if err != nil {
		t.Fatalf("normalizePlanSlug returned error: %v", err)
	}
	if got != "my-plan" {
		t.Fatalf("normalizePlanSlug = %q, want my-plan", got)
	}
	if _, err := normalizePlanSlug("../bad"); err == nil {
		t.Fatal("expected invalid slug error")
	}
}

func TestDevProjectNameIsStableAndScoped(t *testing.T) {
	t.Parallel()

	root := "/tmp/Home Assistant Config"
	first := devProjectName(root)
	second := devProjectName(root)
	if first != second {
		t.Fatalf("project name not stable: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "hass-dev-home-assistant-config-") {
		t.Fatalf("project name = %q", first)
	}
}

func TestAutomationRowMatchesObserveTarget(t *testing.T) {
	t.Parallel()

	row := automationDocRow{
		Path:  "ha-config/automations/presence/office_lights.yaml",
		ID:    "office_lights",
		Alias: "Office Lights",
	}
	if !automationRowMatches(row, "automation.office_lights", "office_lights") {
		t.Fatal("expected row to match automation target")
	}
	if automationRowMatches(row, "automation.kitchen_lights", "kitchen_lights") {
		t.Fatal("did not expect row to match unrelated target")
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasAgentIssue(issues []agentIssue, id string) bool {
	for _, issue := range issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func writeFanLightPairSettingsBlueprint(t *testing.T) {
	t.Helper()
	writeTestFile(t, "ha-config/blueprints/automation/example/z2m_inovelli_blue_dimmer_settings.yaml", `
blueprint:
  name: Test Settings
  domain: automation
  input:
    z2m_friendly_name:
      default: Test Dimmer
    smart_bulb_mode:
      default: Disabled
    output_mode:
      default: Dimmer
    button_delay:
      default: 500ms
    fan_control_mode:
      default: Disabled
variables:
  z2m_name: !input z2m_friendly_name
  v_smart_bulb_mode: !input smart_bulb_mode
  v_output_mode: !input output_mode
  v_button_delay: !input button_delay
  v_fan_control_mode: !input fan_control_mode
actions:
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/set"
      payload: '{"smartBulbMode": "{{ v_smart_bulb_mode }}"}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/get"
      payload: '{"smartBulbMode": ""}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/set"
      payload: '{"outputMode": "{{ v_output_mode }}"}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/get"
      payload: '{"outputMode": ""}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/set"
      payload: '{"buttonDelay": "{{ v_button_delay }}"}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/get"
      payload: '{"buttonDelay": ""}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/set"
      payload: '{"localProtection": "Disabled"}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/get"
      payload: '{"localProtection": ""}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/set"
      payload: '{"fanControlMode": "{{ v_fan_control_mode }}"}'
  - action: mqtt.publish
    data:
      topic: "zigbee2mqtt/{{ z2m_name }}/get"
      payload: '{"fanControlMode": ""}'
`)
}
