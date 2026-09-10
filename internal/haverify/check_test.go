package haverify

import (
	"testing"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

func TestEvaluateExpectationFlagsInvalidSelectOption(t *testing.T) {
	lookup := BuildEntityLookup([]hasync.EntityState{
		{
			EntityID: "select.dining_room_wall_washers_switchtype",
			State:    "Single-Pole",
			Attributes: map[string]interface{}{
				"options": []interface{}{"Single-Pole", "3-Way Dumb Switch", "3-Way Aux Switch"},
			},
		},
	})

	check := EvaluateExpectation(lookup, "dining_room_wall_washers", ParamExpectation{
		MQTTParam: "switchType",
		Value:     "Multi-Way with Inovelli Aux Switch",
	})

	if !check.InvalidOption {
		t.Fatalf("expected invalid select option to be flagged: %+v", check)
	}
	if check.Pass {
		t.Fatalf("invalid option must not pass: %+v", check)
	}
	if check.EntityID != "select.dining_room_wall_washers_switchtype" {
		t.Fatalf("entityID = %q", check.EntityID)
	}
}

func TestEvaluateExpectationFlagsUnavailableTarget(t *testing.T) {
	lookup := BuildEntityLookup([]hasync.EntityState{
		{
			EntityID: "select.kitchen_counter_south_switch_type",
			State:    "unavailable",
		},
	})

	check := EvaluateExpectation(lookup, "ignored", ParamExpectation{
		MQTTParam: "switchType",
		EntityID:  "select.kitchen_counter_south_switch_type",
		Value:     "Single-Pole",
	})

	if !check.Unavailable {
		t.Fatalf("expected unavailable target to be flagged: %+v", check)
	}
	if check.Pass {
		t.Fatalf("unavailable target must not pass: %+v", check)
	}
}
