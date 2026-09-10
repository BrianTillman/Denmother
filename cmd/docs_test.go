package cmd

import "testing"

func TestCollectAutomationDocsUsesBlueprintModeAndTriggers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/blueprints/automation/example/mirror.yaml", `
blueprint:
  name: Mirror
  domain: automation
mode: restart
triggers:
  - trigger: state
    entity_id: !input source
    to: "on"
  - trigger: state
    entity_id: !input source
    to: "off"
`)
	writeTestFile(t, "ha-config/automations/binding/example_mirror.yaml", `
- id: example_mirror
  alias: Example Mirror
  use_blueprint:
    path: example/mirror.yaml
    input:
      source: light.example_source
`)

	rows, err := collectAutomationDocs()
	if err != nil {
		t.Fatalf("collectAutomationDocs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Mode != "restart" {
		t.Fatalf("Mode = %q, want restart", rows[0].Mode)
	}
	if rows[0].Triggers != 2 {
		t.Fatalf("Triggers = %d, want 2", rows[0].Triggers)
	}
}

func TestCollectAutomationDocsUsesBlueprintInputModeDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeTestFile(t, "ha-config/blueprints/automation/example/button.yaml", `
blueprint:
  name: Button
  domain: automation
  input:
    mode:
      name: Automation Mode
      default: queued
mode: !input mode
triggers:
  - trigger: state
    entity_id: !input button_entity
`)
	writeTestFile(t, "ha-config/automations/remotes/example_button.yaml", `
- id: example_button
  alias: Example Button
  use_blueprint:
    path: example/button.yaml
    input:
      button_entity: event.example_button
`)

	rows, err := collectAutomationDocs()
	if err != nil {
		t.Fatalf("collectAutomationDocs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Mode != "queued" {
		t.Fatalf("Mode = %q, want queued", rows[0].Mode)
	}
}
