package hayaml

import (
	"reflect"
	"testing"
)

func TestEquivalentEntityRepresentations(t *testing.T) {
	for _, document := range []string{
		"entity_id: light.study_lamp\n",
		"entity_id: ['light.study_lamp']\n",
		"entity_id:\n  - light.study_lamp\n",
		"entities:\n  light.study_lamp:\n    state: on\n",
		"target: {entity_id: light.study_lamp}\n",
		"use_blueprint:\n  path: sample/example.yaml\n  input:\n    arbitrary_input: light.study_lamp\n",
		"target: &target {entity_id: light.study_lamp}\nactions:\n - target: *target\n",
	} {
		got, err := ExtractReferences([]byte(document))
		if err != nil || len(got.Static) != 1 || got.Static[0].Entity != "light.study_lamp" || got.Static[0].Line < 1 {
			t.Fatalf("document %q: references=%+v err=%v", document, got, err)
		}
	}
}

func TestReferencesIgnoreCommentsAndTextAndExposeDynamicValues(t *testing.T) {
	got, err := ExtractReferences([]byte(`
# entity_id: light.comment
description: light.description
service: light.turn_on
use_blueprint:
  input:
    pressed:
      - action: light.turn_on
        target: {entity_id: light.first}
name: "uses light.substring here"
entities: [light.first, light.second]
actions:
  - target:
      entity_id: "{{ target_entity }}"
  - target:
      entity_id: !input target_entity
`))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ref := range got.Static {
		ids = append(ids, ref.Entity)
	}
	if !reflect.DeepEqual(ids, []string{"light.first", "light.first", "light.second"}) || len(got.Dynamic) != 2 {
		t.Fatalf("references=%+v", got)
	}
}

func TestReferencesRejectMalformedYAMLAndCycles(t *testing.T) {
	for _, document := range []string{"entity_id: [", "entity_id: &cycle [*cycle]"} {
		if _, err := ExtractReferences([]byte(document)); err == nil {
			t.Fatalf("invalid document accepted: %s", document)
		}
	}
}

func TestDynamicReferencesExplainRuntimeRequirements(t *testing.T) {
	refs, err := ExtractReferences([]byte("actions:\n - target: {entity_id: '{{ states.sensor.target.state }}'}\n - target: {entity_id: !input lamp}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Dynamic) != 2 || refs.Dynamic[0].Kind != "template" || refs.Dynamic[1].Kind != "blueprint_input" {
		t.Fatalf("missing dynamic kinds: %+v", refs.Dynamic)
	}
	for _, ref := range refs.Dynamic {
		if ref.Reason == "" || ref.Line < 1 {
			t.Fatalf("unexplained dynamic target: %+v", ref)
		}
	}
}
