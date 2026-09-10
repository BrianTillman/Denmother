package haaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSpec(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit_spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write spec file: %v", err)
	}
	return path
}

func TestLoadAuditSpec(t *testing.T) {
	t.Run("valid spec round-trip", func(t *testing.T) {
		path := writeSpec(t, `
version: 1
name: Office Lights Audit
automation: automation.office_presence_lights
trigger:
  entity_id: binary_sensor.office_occupancy
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.office_overhead
    state: "on"
    attributes:
      brightness: ">= 199"
  - vibe: input_boolean.vibe_sleep
    entity_id: light.office_overhead
    state: "on"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if spec.Version != 1 {
			t.Errorf("version: got %d, want 1", spec.Version)
		}
		if spec.Name != "Office Lights Audit" {
			t.Errorf("name: got %q, want %q", spec.Name, "Office Lights Audit")
		}
		if spec.Automation != "automation.office_presence_lights" {
			t.Errorf("automation: got %q", spec.Automation)
		}
		if spec.Trigger.EntityID != "binary_sensor.office_occupancy" {
			t.Errorf("trigger.entity_id: got %q", spec.Trigger.EntityID)
		}
		if spec.Trigger.To != "on" {
			t.Errorf("trigger.to: got %q", spec.Trigger.To)
		}
		if spec.EffectiveTolerance(30*time.Second) != 30*time.Second {
			t.Errorf("effective tolerance should use fallback when unset")
		}
		if len(spec.Expectations) != 2 {
			t.Fatalf("expectations count: got %d, want 2", len(spec.Expectations))
		}
		if spec.Expectations[0].Attributes["brightness"] != ">= 199" {
			t.Errorf("attribute preserved: got %q", spec.Expectations[0].Attributes["brightness"])
		}
	})

	t.Run("version defaults to 1 when omitted", func(t *testing.T) {
		path := writeSpec(t, `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if spec.Version != 1 {
			t.Errorf("version: got %d, want 1", spec.Version)
		}
	})

	t.Run("spec-specific tolerance", func(t *testing.T) {
		path := writeSpec(t, `
name: Office Lights Off Audit
automation: automation.office_presence_lights
tolerance: 3m
trigger:
  entity_id: binary_sensor.office_occupancy
  to: "off"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.office_overhead
    state: "off"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if spec.EffectiveTolerance(30*time.Second) != 3*time.Minute {
			t.Errorf("effective tolerance did not use spec override: got %s", spec.EffectiveTolerance(30*time.Second))
		}
	})

	t.Run("cancel transition", func(t *testing.T) {
		path := writeSpec(t, `
name: Vanity Vacancy Audit
automation: automation.vanity_presence_lights
tolerance: 11m
trigger:
  entity_id: binary_sensor.vanity_occupancy
  to: "off"
canceled_by:
  - entity_id: binary_sensor.vanity_occupancy
    to: "on"
    within: 10m
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.vanity
    state: "on"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(spec.CanceledBy) != 1 {
			t.Fatalf("canceled_by count: got %d, want 1", len(spec.CanceledBy))
		}
		if spec.CanceledBy[0].EntityID != "binary_sensor.vanity_occupancy" {
			t.Errorf("canceled_by entity: got %q", spec.CanceledBy[0].EntityID)
		}
	})

	t.Run("multi-entity spec (two target lights)", func(t *testing.T) {
		path := writeSpec(t, `
name: Office Lounge Audit
automation: automation.office_lounge_lights
trigger:
  entity_id: binary_sensor.office_lounge_occupancy
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.office_lounge_overhead
    state: "on"
    attributes:
      brightness: ">= 199"
  - vibe: input_boolean.vibe_normal
    entity_id: light.office_lounge_accent
    state: "on"
    attributes:
      brightness: ">= 199"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(spec.Expectations) != 2 {
			t.Errorf("expectations count: got %d, want 2", len(spec.Expectations))
		}
	})

	t.Run("sleep brightness zero (no attributes)", func(t *testing.T) {
		path := writeSpec(t, `
name: Sleep Audit
automation: automation.sleep_lights
trigger:
  entity_id: binary_sensor.bedroom_occupancy
  to: "on"
expectations:
  - vibe: input_boolean.vibe_sleep
    entity_id: light.bedroom_overhead
    state: "on"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if spec.Expectations[0].Attributes != nil {
			t.Errorf("expected nil attributes, got %v", spec.Expectations[0].Attributes)
		}
	})

	t.Run("explicit no change expectation", func(t *testing.T) {
		path := writeSpec(t, `
name: Sleep Audit
automation: automation.sleep_lights
trigger:
  entity_id: binary_sensor.bedroom_occupancy
  to: "on"
expectations:
  - vibe: input_boolean.vibe_sleep
    entity_id: light.bedroom_overhead
    no_change: true
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !spec.Expectations[0].NoChange {
			t.Error("expected no_change to be preserved")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := LoadAuditSpec("/nonexistent/path/spec.yaml")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}

func TestLoadAuditSpec_Missing_Fields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing name",
			yaml: `
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "name",
		},
		{
			name: "missing automation",
			yaml: `
name: Test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "automation",
		},
		{
			name: "missing trigger.entity_id",
			yaml: `
name: Test
automation: automation.test
trigger:
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "trigger",
		},
		{
			name: "missing trigger.to",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "trigger",
		},
		{
			name: "no expectations",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
`,
			wantErr: "expectation",
		},
		{
			name: "expectation missing vibe",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - entity_id: light.test
    state: "on"
`,
			wantErr: "vibe",
		},
		{
			name: "expectation missing entity_id",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    state: "on"
`,
			wantErr: "entity_id",
		},
		{
			name: "expectation missing state",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
`,
			wantErr: "state",
		},
		{
			name: "no change combined with state",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "off"
    no_change: true
`,
			wantErr: "cannot be combined",
		},
		{
			name: "no change combined with attributes",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "on"
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    no_change: true
    attributes:
      brightness: ">= 1"
`,
			wantErr: "cannot be combined",
		},
		{
			name: "cancel missing entity_id",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "off"
canceled_by:
  - to: "on"
    within: 10m
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "canceled_by",
		},
		{
			name: "cancel invalid duration",
			yaml: `
name: Test
automation: automation.test
trigger:
  entity_id: binary_sensor.test
  to: "off"
canceled_by:
  - entity_id: binary_sensor.test
    to: "on"
    within: soon
expectations:
  - vibe: input_boolean.vibe_normal
    entity_id: light.test
    state: "on"
`,
			wantErr: "within",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSpec(t, tc.yaml)
			_, err := LoadAuditSpec(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseAttributeComparison(t *testing.T) {
	tests := []struct {
		expr    string
		wantOp  string
		wantVal float64
		wantErr bool
	}{
		{expr: ">= 199", wantOp: ">=", wantVal: 199.0},
		{expr: "<= 255", wantOp: "<=", wantVal: 255.0},
		{expr: "== 204", wantOp: "==", wantVal: 204.0},
		{expr: "199", wantOp: "==", wantVal: 199.0},
		{expr: "> 0", wantOp: ">", wantVal: 0.0},
		{expr: "< 100", wantOp: "<", wantVal: 100.0},
		{expr: "not_a_number", wantErr: true},
		{expr: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			op, val, err := ParseAttributeComparison(tc.expr)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tc.expr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.expr, err)
			}
			if op != tc.wantOp {
				t.Errorf("op: got %q, want %q", op, tc.wantOp)
			}
			if val != tc.wantVal {
				t.Errorf("val: got %f, want %f", val, tc.wantVal)
			}
		})
	}
}

func TestAllEntityIDs(t *testing.T) {
	t.Run("deduplicates across trigger and expectations", func(t *testing.T) {
		spec := &AuditSpec{
			Trigger: TriggerDef{EntityID: "binary_sensor.office_occupancy"},
			CanceledBy: []CancelDef{
				{EntityID: "binary_sensor.office_occupancy", To: "on", Within: "2m"},
			},
			Expectations: []Expectation{
				{Vibe: "input_boolean.vibe_normal", EntityID: "light.office_overhead"},
				{Vibe: "input_boolean.vibe_entertain", EntityID: "light.office_overhead"},
				{Vibe: "input_boolean.vibe_relax", EntityID: "light.office_overhead"},
				{Vibe: "input_boolean.vibe_sleep", EntityID: "light.office_overhead"},
			},
		}

		ids := spec.AllEntityIDs()

		// Trigger + 4 vibes + 1 unique target = 6 total
		if len(ids) != 6 {
			t.Errorf("count: got %d, want 6; ids: %v", len(ids), ids)
		}
		if hasDuplicates(ids) {
			t.Errorf("duplicate entity IDs found: %v", ids)
		}
	})

	t.Run("multi-light spec includes both targets", func(t *testing.T) {
		spec := &AuditSpec{
			Trigger: TriggerDef{EntityID: "binary_sensor.lounge_occupancy"},
			Expectations: []Expectation{
				{Vibe: "input_boolean.vibe_normal", EntityID: "light.lounge_overhead"},
				{Vibe: "input_boolean.vibe_normal", EntityID: "light.lounge_accent"},
			},
		}

		ids := spec.AllEntityIDs()

		// Trigger + 1 vibe + 2 targets = 4
		if len(ids) != 4 {
			t.Errorf("count: got %d, want 4; ids: %v", len(ids), ids)
		}
		if !containsAll(ids, "light.lounge_overhead", "light.lounge_accent") {
			t.Errorf("missing target lights in %v", ids)
		}
	})

	t.Run("vacancy specs include legacy vibe aliases", func(t *testing.T) {
		spec := &AuditSpec{
			Trigger: TriggerDef{EntityID: "binary_sensor.room_occupancy", To: "off"},
			Expectations: []Expectation{
				{Vibe: "input_boolean.vibe_sleep", EntityID: "light.room"},
			},
		}

		ids := spec.AllEntityIDs()
		if len(ids) != 4 {
			t.Errorf("count: got %d, want 4; ids: %v", len(ids), ids)
		}
		if !containsAll(ids, "binary_sensor.room_occupancy", "input_boolean.vibe_sleep", "input_boolean.vibe_sleeping", "light.room") {
			t.Errorf("missing vacancy IDs in %v", ids)
		}
	})

	t.Run("no duplicates in result", func(t *testing.T) {
		spec := &AuditSpec{
			Trigger: TriggerDef{EntityID: "binary_sensor.test"},
			Expectations: []Expectation{
				{Vibe: "input_boolean.vibe_normal", EntityID: "light.test"},
				{Vibe: "input_boolean.vibe_normal", EntityID: "light.test"},
			},
		}

		ids := spec.AllEntityIDs()
		if hasDuplicates(ids) {
			t.Errorf("duplicate entity IDs: %v", ids)
		}
	})
}

func TestLoadAuditSpec_TraceBased(t *testing.T) {
	t.Run("valid trace-sourced spec (health check, no expectations)", func(t *testing.T) {
		path := writeSpec(t, `
name: Fan Dimmer Audit
automation: automation.controls_fan_dimmer
trigger:
  source: traces
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !spec.IsTraceBased() {
			t.Error("expected IsTraceBased() to return true")
		}
		if spec.Trigger.EntityID != "" {
			t.Errorf("expected empty trigger.entity_id, got %q", spec.Trigger.EntityID)
		}
		if len(spec.Expectations) != 0 {
			t.Errorf("expected 0 expectations, got %d", len(spec.Expectations))
		}
	})

	t.Run("trace-sourced spec with expectations (no vibe)", func(t *testing.T) {
		path := writeSpec(t, `
name: Fan Dimmer Audit
automation: automation.controls_fan_dimmer
trigger:
  source: traces
expectations:
  - entity_id: timer.fan_mode
    state: "active"
  - entity_id: fan.ceiling_fan
    state: "on"
`)
		spec, err := LoadAuditSpec(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(spec.Expectations) != 2 {
			t.Errorf("expected 2 expectations, got %d", len(spec.Expectations))
		}
		if spec.Expectations[0].Vibe != "" {
			t.Errorf("expected empty vibe, got %q", spec.Expectations[0].Vibe)
		}
	})

	t.Run("trace-sourced spec rejects missing entity_id in expectation", func(t *testing.T) {
		path := writeSpec(t, `
name: Bad Spec
automation: automation.test
trigger:
  source: traces
expectations:
  - state: "on"
`)
		_, err := LoadAuditSpec(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "entity_id") {
			t.Errorf("error %q does not mention entity_id", err.Error())
		}
	})

	t.Run("invalid trigger source", func(t *testing.T) {
		path := writeSpec(t, `
name: Bad Source
automation: automation.test
trigger:
  source: invalid
`)
		_, err := LoadAuditSpec(path)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "source") {
			t.Errorf("error %q does not mention source", err.Error())
		}
	})
}

func TestAllEntityIDs_TraceBased(t *testing.T) {
	spec := &AuditSpec{
		Trigger: TriggerDef{Source: "traces"},
		Expectations: []Expectation{
			{EntityID: "timer.fan_mode"},
			{Vibe: "input_boolean.vibe_normal", EntityID: "fan.ceiling_fan"},
		},
	}

	ids := spec.AllEntityIDs()

	// No trigger entity, 1 vibe + 2 targets = 3
	if len(ids) != 3 {
		t.Errorf("count: got %d, want 3; ids: %v", len(ids), ids)
	}
	if !containsAll(ids, "timer.fan_mode", "fan.ceiling_fan", "input_boolean.vibe_normal") {
		t.Errorf("missing expected IDs in %v", ids)
	}
}

func hasDuplicates(ids []string) bool {
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}

func containsAll(ids []string, targets ...string) bool {
	set := make(map[string]bool)
	for _, id := range ids {
		set[id] = true
	}
	for _, t := range targets {
		if !set[t] {
			return false
		}
	}
	return true
}
