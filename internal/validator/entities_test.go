package validator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BrianTillman/Denmother/internal/util"
)

func TestLoadEntityList(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]bool
		wantErr bool
	}{
		{
			name:    "valid entity list",
			content: "light.office_overhead\nbinary_sensor.garage_occupancy\nswitch.backyard_pump\n",
			want: map[string]bool{
				"light.office_overhead":          true,
				"binary_sensor.garage_occupancy": true,
				"switch.backyard_pump":           true,
			},
			wantErr: false,
		},
		{
			name:    "entity list with comments",
			content: "# Generated entity list\nlight.office_overhead\n# Another comment\nbinary_sensor.garage_occupancy\n",
			want: map[string]bool{
				"light.office_overhead":          true,
				"binary_sensor.garage_occupancy": true,
			},
			wantErr: false,
		},
		{
			name:    "entity list with empty lines",
			content: "light.office_overhead\n\n\nbinary_sensor.garage_occupancy\n\n",
			want: map[string]bool{
				"light.office_overhead":          true,
				"binary_sensor.garage_occupancy": true,
			},
			wantErr: false,
		},
		{
			name:    "entity list with whitespace",
			content: "  light.office_overhead  \n\tbinary_sensor.garage_occupancy\t\n",
			want: map[string]bool{
				"light.office_overhead":          true,
				"binary_sensor.garage_occupancy": true,
			},
			wantErr: false,
		},
		{
			name:    "empty entity list",
			content: "",
			want:    map[string]bool{},
			wantErr: false,
		},
		{
			name:    "entity list with only comments",
			content: "# Comment 1\n# Comment 2\n",
			want:    map[string]bool{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "entity-list.txt")
			err := os.WriteFile(tmpFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}

			got, err := loadEntityList(tmpFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadEntityList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadEntityList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadEntityListFileNotFound(t *testing.T) {
	_, err := loadEntityList("/nonexistent/path/entity-list.txt")
	if err == nil {
		t.Error("loadEntityList() expected error for non-existent file")
	}
}

func TestLoadLocalConfigEntities(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "ha-config")
	writeValidatorTestFile(t, filepath.Join(configPath, "helpers/input-button.yaml"), `
hvac_main_filter_replaced:
  name: HVAC Main Filter Replaced
`)
	writeValidatorTestFile(t, filepath.Join(configPath, "helpers/input-datetime.yaml"), `
hvac_secondary_filter_runtime_last_tallied:
  name: HVAC Secondary Filter Runtime Last Tallied
  has_date: true
  has_time: true
`)
	writeValidatorTestFile(t, filepath.Join(configPath, "helpers/input_number-left-open.yaml"), `
hvac_main_filter_runtime_hours:
  name: HVAC Main Filter Runtime Hours
`)
	writeValidatorTestFile(t, filepath.Join(configPath, "helpers/binary_sensor-groups.yaml"), `
- platform: group
  unique_id: garage_occupancy_sensors
  name: Garage Occupancy Sensors
`)
	writeValidatorTestFile(t, filepath.Join(configPath, "helpers/template-sensors.yaml"), `
- binary_sensor:
    - name: HVAC Main Unit Running
      unique_id: hvac_main_unit_running
- sensor:
    - name: HVAC Main Filter Runtime Remaining
      unique_id: hvac_main_filter_runtime_remaining
    - name: Already Qualified
      unique_id: sensor.already_qualified
`)
	writeValidatorTestFile(t, filepath.Join(configPath, "automations/config/house_policy.yaml"), `
- id: config_inovelli_house_policy
  alias: Config Inovelli House Policy
  triggers: []
  actions: []
`)

	entities := loadLocalConfigEntities(configPath)
	for _, want := range []string{
		"input_button.hvac_main_filter_replaced",
		"input_datetime.hvac_secondary_filter_runtime_last_tallied",
		"input_number.hvac_main_filter_runtime_hours",
		"binary_sensor.garage_occupancy_sensors",
		"binary_sensor.hvac_main_unit_running",
		"sensor.hvac_main_filter_runtime_remaining",
		"sensor.already_qualified",
		"automation.config_inovelli_house_policy",
	} {
		if !entities[want] {
			t.Fatalf("loadLocalConfigEntities missing %q in %#v", want, entities)
		}
	}
}

func TestExtractEntities(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name:     "entity_id with quotes",
			content:  "entity_id: \"light.office_overhead\"\n",
			expected: []string{"light.office_overhead"},
		},
		{
			name:     "entity_id without quotes",
			content:  "entity_id: light.office_overhead\n",
			expected: []string{"light.office_overhead"},
		},
		{
			name:     "entity with quotes",
			content:  "entity: \"binary_sensor.garage_occupancy\"\n",
			expected: []string{"binary_sensor.garage_occupancy"},
		},
		{
			name:     "scene reference",
			content:  "scene: \"scene.evening_night\"\n",
			expected: []string{"scene.evening_night"},
		},
		{
			name:     "list item entity",
			content:  "entities:\n  - light.office_overhead\n",
			expected: []string{"light.office_overhead"},
		},
		{
			name:     "list item with quotes",
			content:  "entities:\n  - \"switch.backyard_pump\"\n",
			expected: []string{"switch.backyard_pump"},
		},
		{
			name:     "multiple entities",
			content:  "entity_id: light.office_overhead\nentity: binary_sensor.garage_occupancy\nentities:\n  - switch.backyard_pump\n",
			expected: []string{"light.office_overhead", "binary_sensor.garage_occupancy", "switch.backyard_pump"},
		},
		{
			name:     "skip template expression",
			content:  "entity_id: '{{ states.light.office_overhead }}'\n",
			expected: []string{},
		},
		{
			name:     "skip comment line",
			content:  "# entity_id: light.office_overhead\n",
			expected: []string{},
		},
		{
			name:     "mixed template and regular",
			content:  "entity_id: light.office_overhead\nstate: \"{{ states('sensor.temp') }}\"\nentity: binary_sensor.garage_occupancy\n",
			expected: []string{"light.office_overhead", "binary_sensor.garage_occupancy"},
		},
		{
			name:     "automation with entities",
			content:  "alias: Test\ntrigger:\n  - platform: state\n    entity_id: binary_sensor.garage_occupancy\naction:\n  - service: light.turn_on\n    target:\n      entity_id: light.office_overhead\n",
			expected: []string{"binary_sensor.garage_occupancy", "light.office_overhead"},
		},
		{
			name:     "single quotes",
			content:  "entity_id: 'light.office_overhead'\n",
			expected: []string{"light.office_overhead"},
		},
		{
			name:     "no entities",
			content:  "alias: Test Automation\nmode: single\n",
			expected: []string{},
		},
		{
			name:     "entity with underscore numbers",
			content:  "entity_id: sensor.office_pm2_5\n",
			expected: []string{"sensor.office_pm2_5"},
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

			refs := extractEntities(tmpFile, tmpDir)
			var got []string
			for _, ref := range refs {
				got = append(got, ref.entity)
			}

			if len(got) != len(tt.expected) {
				t.Errorf("extractEntities() returned %d entities, want %d", len(got), len(tt.expected))
				t.Errorf("got: %v", got)
				t.Errorf("want: %v", tt.expected)
				return
			}

			for i, entity := range tt.expected {
				if got[i] != entity {
					t.Errorf("extractEntities()[%d] = %q, want %q", i, got[i], entity)
				}
			}
		})
	}
}

func writeValidatorTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindSimilar(t *testing.T) {
	validEntities := map[string]bool{
		"light.office_overhead":          true,
		"light.office_overhead_north":    true,
		"light.office_overhead_south":    true,
		"light.living_room_chandelier":   true,
		"binary_sensor.garage_occupancy": true,
		"switch.backyard_pump":           true,
	}

	tests := []struct {
		name   string
		target string
		want   int // minimum number of suggestions expected
	}{
		{
			name:   "typo in light name",
			target: "light.office_overhad",
			want:   1,
		},
		{
			name:   "partial match",
			target: "light.office_over",
			want:   1,
		},
		{
			name:   "wrong domain",
			target: "switch.office_overhead",
			want:   0, // different domain, no matches expected
		},
		{
			name:   "completely different",
			target: "light.xyz_abc",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSimilar(tt.target, validEntities)
			if len(got) < tt.want {
				t.Errorf("findSimilar(%q) returned %d suggestions, want at least %d", tt.target, len(got), tt.want)
			}
		})
	}
}

func TestSimilarityInFindSimilar(t *testing.T) {
	tests := []struct {
		name     string
		a, b     string
		minScore float64
	}{
		{
			name:     "identical strings",
			a:        "light.office_overhead",
			b:        "light.office_overhead",
			minScore: 0.99,
		},
		{
			name:     "similar strings",
			a:        "light.office_overhead",
			b:        "light.office_overhad",
			minScore: 0.8,
		},
		{
			name:     "completely different",
			a:        "light.office",
			b:        "switch.garage",
			minScore: 0.0,
		},
		{
			name:     "empty string a",
			a:        "",
			b:        "light.office",
			minScore: 0.0,
		},
		{
			name:     "empty string b",
			a:        "light.office",
			b:        "",
			minScore: 0.0,
		},
		{
			name:     "both empty",
			a:        "",
			b:        "",
			minScore: 0.99, // util.Similarity returns 1.0 for both empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := util.Similarity(tt.a, tt.b)
			if got < tt.minScore {
				t.Errorf("util.Similarity(%q, %q) = %v, want >= %v", tt.a, tt.b, got, tt.minScore)
			}
		})
	}
}

func TestEntityRef(t *testing.T) {
	ref := entityRef{
		entity:  "light.office_overhead",
		file:    "automations/test.yaml",
		line:    10,
		context: "entity_id: light.office_overhead",
	}

	if ref.entity != "light.office_overhead" {
		t.Errorf("entityRef.entity = %q, want %q", ref.entity, "light.office_overhead")
	}
	if ref.line != 10 {
		t.Errorf("entityRef.line = %d, want %d", ref.line, 10)
	}
}

func TestValidateEntitiesIncompleteWhenListMissing(t *testing.T) {
	configPath := t.TempDir()
	err := validateEntities(configPath, Options{})
	if !IsIncomplete(err) {
		t.Fatalf("missing entity-list.txt returned %v, want incomplete", err)
	}
}

func TestEntityResultRetainsDynamicCountsAndSourceFindings(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "ha-config")
	os.MkdirAll(config, 0755)
	os.MkdirAll(filepath.Join(root, "docs/reference"), 0755)
	os.WriteFile(filepath.Join(root, "docs/reference/entity-list.txt"), []byte("light.study_lamp\n"), 0600)
	os.WriteFile(filepath.Join(config, "configuration.yaml"), []byte("scene:\n - entities: {light.missing: 'on'}\nautomation:\n - actions:\n    - target: {entity_id: '{{ chosen }}'}\n"), 0600)
	result := New(config, Options{}).CheckEntities()
	if result.Status != CheckFailed || len(result.Findings) != 1 || result.Details["dynamic_references"] != 1 {
		t.Fatalf("result lost evidence: %+v", result)
	}
}
