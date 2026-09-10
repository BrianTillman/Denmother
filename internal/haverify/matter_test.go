package haverify

import "testing"

func TestResolveMatterEntityExpectationsUsesExplicitEntities(t *testing.T) {
	expectations := ResolveMatterEntityExpectations(map[string]interface{}{
		"button_delay_entity": "select.office_overhead_lights_button_delay",
		"button_delay":        "300ms",
		"switch_type_entity":  "select.office_overhead_lights_switch_type",
		"switch_type":         "Multi-Way with Inovelli Aux Switch",
		"status_bar_entity":   "light.office_overhead_lights_status_bar",
	})

	if len(expectations) != 2 {
		t.Fatalf("expected 2 verifiable expectations, got %d", len(expectations))
	}

	if expectations[0].MQTTParam != "buttonDelay" {
		t.Fatalf("param[0] = %q, want buttonDelay", expectations[0].MQTTParam)
	}
	if expectations[0].EntityID != "select.office_overhead_lights_button_delay" {
		t.Fatalf("entity[0] = %q", expectations[0].EntityID)
	}
	if expectations[0].Value != "300ms" {
		t.Fatalf("value[0] = %q", expectations[0].Value)
	}

	if expectations[1].MQTTParam != "switchType" {
		t.Fatalf("param[1] = %q, want switchType", expectations[1].MQTTParam)
	}
}

func TestFindEntityForExpectationPrefersExplicitEntity(t *testing.T) {
	entityID, state, found := FindEntityForExpectation(EntityLookup{
		"select.office_overhead_lights_button_delay": {
			EntityID: "select.office_overhead_lights_button_delay",
			State:    "300ms",
		},
	}, "ignored_prefix", ParamExpectation{
		MQTTParam: "buttonDelay",
		EntityID:  "select.office_overhead_lights_button_delay",
	})

	if !found {
		t.Fatal("expected entity to be found")
	}
	if entityID != "select.office_overhead_lights_button_delay" {
		t.Fatalf("entityID = %q", entityID)
	}
	if state != "300ms" {
		t.Fatalf("state = %q", state)
	}
}

func TestResolveMatterFanCanopyEntityExpectations(t *testing.T) {
	expectations := ResolveMatterFanCanopyEntityExpectations(map[string]interface{}{
		"light_mode_entity":       "select.kitchen_ceiling_fan_light_mode",
		"light_mode":              "Dimmer+Leading",
		"fan_on_level_entity":     "number.kitchen_ceiling_fan_on_level_2",
		"fan_on_level":            255,
		"led_on_intensity_entity": "",
		"led_on_intensity":        33,
	})

	if len(expectations) != 2 {
		t.Fatalf("expected 2 verifiable expectations, got %d", len(expectations))
	}
	if expectations[0].MQTTParam != "lightMode" || expectations[0].Value != "Dimmer+Leading" {
		t.Fatalf("light mode expectation = %#v", expectations[0])
	}
	if expectations[1].MQTTParam != "fanOnLevel" || expectations[1].Value != "255" {
		t.Fatalf("fan level expectation = %#v", expectations[1])
	}
}
