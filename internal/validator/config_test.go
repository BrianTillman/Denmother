package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(dir string)
		wantErr bool
	}{
		{
			name: "valid minimal config",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
				os.MkdirAll(filepath.Join(dir, "automations"), 0755)
				os.WriteFile(filepath.Join(dir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)
			},
			wantErr: false,
		},
		{
			name: "valid full config",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "automations"), 0755)
				os.WriteFile(filepath.Join(dir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "scenes"), 0755)
				os.WriteFile(filepath.Join(dir, "scenes", "evening.yaml"), []byte("name: Evening\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "blueprints"), 0755)
				os.WriteFile(filepath.Join(dir, "blueprints", "motion.yaml"), []byte("blueprint:\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "homekit"), 0755)
				os.WriteFile(filepath.Join(dir, "homekit", "bridge.yaml"), []byte("filter:\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "helpers"), 0755)
				os.WriteFile(filepath.Join(dir, "helpers", "inputs.yaml"), []byte("input_boolean:\n"), 0644)
			},
			wantErr: false,
		},
		{
			name: "missing configuration.yaml",
			setup: func(dir string) {
				os.MkdirAll(filepath.Join(dir, "automations"), 0755)
				os.WriteFile(filepath.Join(dir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)
			},
			wantErr: true,
		},
		{
			name: "missing automations",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
			},
			wantErr: false,
		},
		{
			name: "automations.yaml instead of directory",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
				os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte("- alias: Test\n"), 0644)
			},
			wantErr: false,
		},
		{
			name: "empty automations directory",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
				os.MkdirAll(filepath.Join(dir, "automations"), 0755)
			},
			wantErr: false,
		},
		{
			name: "scenes.yaml instead of directory",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
				os.MkdirAll(filepath.Join(dir, "automations"), 0755)
				os.WriteFile(filepath.Join(dir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)
				os.WriteFile(filepath.Join(dir, "scenes.yaml"), []byte("- name: Evening\n"), 0644)
			},
			wantErr: false,
		},
		{
			name: "nested automations structure",
			setup: func(dir string) {
				os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)

				os.MkdirAll(filepath.Join(dir, "automations", "presence"), 0755)
				os.MkdirAll(filepath.Join(dir, "automations", "binding"), 0755)
				os.WriteFile(filepath.Join(dir, "automations", "presence", "office.yaml"), []byte("alias: Office\n"), 0644)
				os.WriteFile(filepath.Join(dir, "automations", "binding", "switches.yaml"), []byte("alias: Switches\n"), 0644)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tt.setup(tmpDir)

			err := validateConfig(tmpDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigDirectoryStructure(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)

	dirs := []string{
		"automations",
		"automations/presence",
		"automations/binding",
		"automations/remotes",
		"automations/time",
		"scenes",
		"homekit",
		"helpers",
		"blueprints",
		"dashboards",
	}

	for _, dir := range dirs {
		os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		os.WriteFile(filepath.Join(tmpDir, dir, "test.yaml"), []byte("key: value\n"), 0644)
	}

	err := validateConfig(tmpDir)
	if err != nil {
		t.Errorf("validateConfig() failed with complete structure: %v", err)
	}
}

func TestValidateConfigHomeKit(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "homekit"), 0755)
	for i := 1; i <= 4; i++ {
		content := "filter:\n  include_entities:\n    - light.office\n"
		os.WriteFile(filepath.Join(tmpDir, "homekit", "bridge"+string(rune('0'+i))+".yaml"), []byte(content), 0644)
	}

	err := validateConfig(tmpDir)
	if err != nil {
		t.Errorf("validateConfig() with HomeKit bridges failed: %v", err)
	}
}

func TestValidateConfigBlueprints(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "blueprints", "automation"), 0755)
	os.MkdirAll(filepath.Join(tmpDir, "blueprints", "script"), 0755)

	blueprintContent := "blueprint:\n  name: Test Blueprint\n  domain: automation\n"
	os.WriteFile(filepath.Join(tmpDir, "blueprints", "automation", "motion_light.yaml"), []byte(blueprintContent), 0644)

	err := validateConfig(tmpDir)
	if err != nil {
		t.Errorf("validateConfig() with blueprints failed: %v", err)
	}
}

func TestValidateConfigHelpers(t *testing.T) {
	tmpDir := t.TempDir()

	os.WriteFile(filepath.Join(tmpDir, "configuration.yaml"), []byte("homeassistant:\n"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "automations"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "automations", "test.yaml"), []byte("alias: Test\n"), 0644)

	os.MkdirAll(filepath.Join(tmpDir, "helpers"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "helpers", "input_booleans.yaml"), []byte("input_boolean:\n  test_switch:\n    name: Test Switch\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "helpers", "input_numbers.yaml"), []byte("input_number:\n  test_value:\n    name: Test Value\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "helpers", "groups.yaml"), []byte("group:\n  all_lights:\n    entities:\n      - light.office\n"), 0644)

	err := validateConfig(tmpDir)
	if err != nil {
		t.Errorf("validateConfig() with helpers failed: %v", err)
	}
}
