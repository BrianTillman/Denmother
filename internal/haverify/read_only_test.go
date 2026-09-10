package haverify

import "testing"

func TestAppendReadOnlyInputExpectationsAddsDimmingMode(t *testing.T) {
	expectations := AppendReadOnlyInputExpectations(nil, map[string]interface{}{
		"dimming_mode": "Trailing edge",
	}, map[string]bool{
		"dimmingMode": true,
	})

	if len(expectations) != 1 {
		t.Fatalf("expected 1 expectation, got %d", len(expectations))
	}
	if expectations[0].MQTTParam != "dimmingMode" {
		t.Fatalf("expected dimmingMode param, got %q", expectations[0].MQTTParam)
	}
	if expectations[0].Value != "Trailing edge" {
		t.Fatalf("expected Trailing edge value, got %q", expectations[0].Value)
	}
}

func TestAppendReadOnlyInputExpectationsDoesNotDuplicateSetExpectation(t *testing.T) {
	existing := []ParamExpectation{{MQTTParam: "dimmingMode", Value: "Leading edge"}}
	expectations := AppendReadOnlyInputExpectations(existing, map[string]interface{}{
		"dimming_mode": "Trailing edge",
	}, map[string]bool{
		"dimmingMode": true,
	})

	if len(expectations) != 1 {
		t.Fatalf("expected existing expectation only, got %d", len(expectations))
	}
	if expectations[0].Value != "Leading edge" {
		t.Fatalf("expected existing value to be preserved, got %q", expectations[0].Value)
	}
}

func TestAppendReadOnlyInputExpectationsAddsPowerType(t *testing.T) {
	expectations := AppendReadOnlyInputExpectations(nil, map[string]interface{}{
		"power_type": "Neutral",
	}, map[string]bool{
		"powerType": true,
	})

	if len(expectations) != 1 {
		t.Fatalf("expected 1 expectation, got %d", len(expectations))
	}
	if expectations[0].MQTTParam != "powerType" {
		t.Fatalf("expected powerType param, got %q", expectations[0].MQTTParam)
	}
	if expectations[0].Value != "Neutral" {
		t.Fatalf("expected Neutral value, got %q", expectations[0].Value)
	}
}
