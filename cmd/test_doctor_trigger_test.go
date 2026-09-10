package cmd

import (
	"testing"

	"github.com/BrianTillman/Denmother/internal/hatest"
)

func TestFireMQTTMessageIsNaturalSupportedTrigger(t *testing.T) {
	testCase := hatest.TestCase{
		Trigger: []hatest.ServiceCall{{Service: "mock_entities.fire_mqtt_message"}},
	}

	if !testCaseUsesNaturalTrigger(testCase) {
		t.Fatal("mock_entities.fire_mqtt_message must count as a natural trigger")
	}
	if hasUnsupportedTriggerPattern(testCase) {
		t.Fatal("mock_entities.fire_mqtt_message must be a supported trigger")
	}
}

func TestHelperServiceTriggersRequireTraceCoverage(t *testing.T) {
	for _, service := range []string{"input_boolean.turn_on", "input_boolean.turn_off", "input_boolean.toggle"} {
		if !testCaseUsesNaturalTrigger(hatest.TestCase{Trigger: []hatest.ServiceCall{{Service: service}}}) {
			t.Fatalf("portable helper state trigger not recognized: %s", service)
		}
	}
	if testCaseUsesNaturalTrigger(hatest.TestCase{Trigger: []hatest.ServiceCall{{Service: "automation.trigger"}}}) {
		t.Fatal("direct automation invocation is not a natural trigger")
	}
}
