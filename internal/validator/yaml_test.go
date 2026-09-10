package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateYAMLFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "valid simple YAML",
			content: "key: value\n",
			wantErr: false,
		},
		{
			name:    "valid nested YAML",
			content: "parent:\n  child: value\n  list:\n    - item1\n    - item2\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with !include tag",
			content: "automation: !include automations.yaml\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with !secret tag",
			content: "password: !secret db_password\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with !input tag",
			content: "entity_id: !input target_entity\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with !include_dir tag",
			content: "automation: !include_dir_merge_list automations/\n",
			wantErr: false,
		},
		{
			name:    "valid multi-document YAML",
			content: "doc1: value\n---\ndoc2: value\n",
			wantErr: false,
		},
		{
			name:    "valid empty YAML",
			content: "",
			wantErr: false,
		},
		{
			name:    "valid YAML with comments",
			content: "# This is a comment\nkey: value  # inline comment\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with anchors",
			content: "defaults: &defaults\n  timeout: 30\nserver:\n  <<: *defaults\n  host: localhost\n",
			wantErr: false,
		},
		{
			name:    "invalid YAML - unbalanced braces",
			content: "key: {nested: value\n",
			wantErr: true,
		},
		{
			name:    "invalid YAML - tab indentation mixed",
			content: "parent:\n\t- item\n  - item2\n",
			wantErr: true,
		},
		{
			name:    "valid YAML - apparent bad indentation is valid",
			content: "parent:\nchild: value\n",
			wantErr: false, // parent and child are separate root keys.
		},
		{
			name:    "valid YAML with special characters",
			content: "message: \"Hello, World!\"\npath: /usr/local/bin\n",
			wantErr: false,
		},
		{
			name:    "valid YAML with unicode",
			content: "emoji: \"\U0001F600\"\ntext: \"日本語\"\n",
			wantErr: false,
		},
		{
			name:    "valid automation YAML",
			content: "alias: Test Automation\ntrigger:\n  - platform: state\n    entity_id: light.office\naction:\n  - service: light.turn_off\n    target:\n      entity_id: light.office\n",
			wantErr: false,
		},
		{
			name:    "valid scene YAML",
			content: "- name: Evening\n  entities:\n    light.living_room:\n      state: on\n      brightness: 128\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.yaml")
			err := os.WriteFile(tmpFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}

			err = validateYAMLFile(tmpFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateYAMLFile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestYAMLValidationRejectsMalformedIncludedConfiguration(t *testing.T) {
	for _, path := range []string{"scripts/evening.yaml", "packages/lights.yml", "nested/custom/inputs.yaml", "automations/evening.yml"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "configuration.yaml"), []byte("default_config: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("broken: [\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := New(root, Options{}).ValidateYAML(); err == nil {
				t.Fatal("malformed configuration passed lint")
			}
		})
	}
}

func TestYAMLTagTextDoesNotSuppressErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("value: !!int \"!secret\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateYAMLFile(path); err == nil {
		t.Fatal("invalid integer passed because its error mentioned a custom tag")
	}
}

func TestYAMLValidationExcludesRuntimeAndTestAssets(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"tests/broken.yaml", "custom_components/demo/broken.yaml", ".storage/broken.yaml", "secrets.yaml"} {
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("broken: [\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "configuration.yaml"), []byte("automation: !include automations.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := New(root, Options{}).ValidateYAML(); err != nil {
		t.Fatal(err)
	}
}
