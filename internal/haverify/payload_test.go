package haverify

import (
	"testing"
)

func TestParsePayloadTemplateUsesTopLevelCompositeKey(t *testing.T) {
	params := parsePayloadTemplate(`{"mmwave_control_commands": {"controlID": "{{ v_mmwave_interference_control_id }}"}}`)

	if len(params) != 1 {
		t.Fatalf("expected one top-level param, got %d: %#v", len(params), params)
	}
	if params[0].Name != "mmwave_control_commands" {
		t.Fatalf("expected mmwave_control_commands, got %q", params[0].Name)
	}
}

func TestParsePayloadTemplateDoesNotTreatJinjaExpressionAsComposite(t *testing.T) {
	params := parsePayloadTemplate(`{"minimumLevel": {{ v_minimum_level }}}`)

	if len(params) != 1 {
		t.Fatalf("expected one scalar param, got %d: %#v", len(params), params)
	}
	if params[0].Name != "minimumLevel" {
		t.Fatalf("expected minimumLevel, got %q", params[0].Name)
	}
	if params[0].ValueExpr != "{{ v_minimum_level }}" {
		t.Fatalf("expected Jinja value expression, got %q", params[0].ValueExpr)
	}
}
