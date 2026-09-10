package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatorIntegration_ValidConfig(t *testing.T) {
	testdataDir := getTestdataDir(t)
	validDir := filepath.Join(testdataDir, "valid")

	if _, err := os.Stat(validDir); os.IsNotExist(err) {
		t.Fatal("testdata/valid directory not found")
	}

	v := New(validDir, Options{})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() failed on valid config: %v", err)
	}

	if err := v.ValidateConfig(); err != nil {
		t.Errorf("ValidateConfig() failed on valid config: %v", err)
	}

	if err := v.ValidateGuard(); err != nil {
		t.Errorf("ValidateGuard() failed on valid config: %v", err)
	}
}

func TestValidatorIntegration_InvalidYAML(t *testing.T) {
	testdataDir := getTestdataDir(t)
	invalidDir := filepath.Join(testdataDir, "invalid")

	if _, err := os.Stat(invalidDir); os.IsNotExist(err) {
		t.Fatal("testdata/invalid directory not found")
	}

	badYAMLPath := filepath.Join(invalidDir, "bad_yaml.yaml")
	if _, err := os.Stat(badYAMLPath); err == nil {
		err := validateYAMLFile(badYAMLPath)
		if err == nil {
			t.Error("validateYAMLFile() should fail on bad_yaml.yaml")
		}
	}
}

func TestValidatorIntegration_FullPipeline(t *testing.T) {
	tmpDir := t.TempDir()

	setupTestConfig(t, tmpDir)

	v := New(tmpDir, Options{})

	var errors []error

	if err := v.ValidateYAML(); err != nil {
		errors = append(errors, err)
	}
	if err := v.ValidateConfig(); err != nil {
		errors = append(errors, err)
	}
	if err := v.ValidateGuard(); err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		t.Errorf("Full validation pipeline failed with %d errors:", len(errors))
		for _, err := range errors {
			t.Logf("  - %v", err)
		}
	}
}

func TestValidatorIntegration_MissingConfig(t *testing.T) {
	tmpDir := t.TempDir()

	v := New(tmpDir, Options{})

	if err := v.ValidateConfig(); err == nil {
		t.Error("ValidateConfig() should fail when configuration.yaml is missing")
	}
}

func TestValidatorIntegration_EmptyAutomations(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)

	v := New(tmpDir, Options{})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() should pass with empty automations: %v", err)
	}
}

func TestValidatorIntegration_NestedAutomations(t *testing.T) {
	tmpDir := t.TempDir()

	setupTestConfig(t, tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, "automations", "presence"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "automations", "binding"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "automations", "time"), 0755)

	autoContent := `alias: Test Automation
trigger:
  - platform: state
    entity_id: binary_sensor.test
action:
  - service: light.turn_on
`
	os.WriteFile(filepath.Join(tmpDir, "automations", "presence", "office.yaml"), []byte(autoContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "automations", "binding", "switches.yaml"), []byte(autoContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "automations", "time", "morning.yaml"), []byte(autoContent), 0644)

	v := New(tmpDir, Options{})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() failed on nested automations: %v", err)
	}

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() failed on nested automations: %v", err)
	}
}

func TestValidatorIntegration_WithOptions(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestConfig(t, tmpDir)

	v := New(tmpDir, Options{Fix: true})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() with Fix option failed: %v", err)
	}
}

func TestValidatorIntegration_SecretsExcluded(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n  password: !secret admin_password\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\ntrigger:\n  - platform: state\n    entity_id: binary_sensor.test\naction:\n  - service: light.turn_on\n"), 0644)

	// Create secrets.yaml (should be skipped during validation)
	os.WriteFile(filepath.Join(tmpDir, "secrets.yaml"), []byte("admin_password: supersecret\napi_key: 12345\n"), 0644)

	v := New(tmpDir, Options{})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() should pass even with secrets.yaml present: %v", err)
	}
}

func TestValidatorIntegration_HomeKitBridges(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestConfig(t, tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, "homekit"), 0755)
	bridgeContent := `- name: Bridge 1
  port: 21063
  filter:
    include_entities:
      - light.office
`
	os.WriteFile(filepath.Join(tmpDir, "homekit", "bridge1.yaml"), []byte(bridgeContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "homekit", "bridge2.yaml"), []byte(bridgeContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "homekit", "bridge3.yaml"), []byte(bridgeContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "homekit", "bridge4.yaml"), []byte(bridgeContent), 0644)

	v := New(tmpDir, Options{})

	if err := v.ValidateConfig(); err != nil {
		t.Errorf("ValidateConfig() failed with HomeKit bridges: %v", err)
	}
}

func TestValidatorIntegration_Blueprints(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestConfig(t, tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, "blueprints", "automation"), 0755)
	blueprintContent := `blueprint:
  name: Test Blueprint
  domain: automation
  input:
    target_entity:
      name: Target
      selector:
        entity:
trigger:
  - platform: state
    entity_id: !input target_entity
action:
  - service: light.turn_on
`
	os.WriteFile(filepath.Join(tmpDir, "blueprints", "automation", "test.yaml"), []byte(blueprintContent), 0644)

	v := New(tmpDir, Options{})

	if err := v.ValidateYAML(); err != nil {
		t.Errorf("ValidateYAML() failed on blueprints: %v", err)
	}
}

func getTestdataDir(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	paths := []string{
		filepath.Join(wd, "..", "..", "testdata"),
		filepath.Join(wd, "testdata"),
		filepath.Join(wd, "..", "testdata"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return filepath.Join(wd, "..", "..", "testdata")
}

func setupTestConfig(t *testing.T, dir string) {
	t.Helper()

	os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n  name: Test\n"), 0644)

	os.MkdirAll(filepath.Join(dir, "automations"), 0755)
	autoContent := `alias: Test Automation
trigger:
  - platform: state
    entity_id: binary_sensor.test
action:
  - service: light.turn_on
    target:
      entity_id: light.test
`
	os.WriteFile(filepath.Join(dir, "automations", "test.yaml"), []byte(autoContent), 0644)

	os.MkdirAll(filepath.Join(dir, "scenes"), 0755)
	sceneContent := `- name: Test Scene
  entities:
    light.test:
      state: on
`
	os.WriteFile(filepath.Join(dir, "scenes", "test.yaml"), []byte(sceneContent), 0644)

	os.MkdirAll(filepath.Join(dir, "helpers"), 0755)
	helperContent := `input_boolean:
  test_switch:
    name: Test Switch
`
	os.WriteFile(filepath.Join(dir, "helpers", "inputs.yaml"), []byte(helperContent), 0644)
}
