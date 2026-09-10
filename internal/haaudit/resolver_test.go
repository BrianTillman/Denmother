package haaudit

import "testing"

func TestResolverResolve(t *testing.T) {
	r := &AutomationIDResolver{
		entityToConfigID: map[string]string{
			"automation.office_occupied":                  "1751512590941",
			"automation.office_bathroom_occupancy_lights": "1750957290157",
			"automation.vibe_default_fallback":            "vibe_default_fallback",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		// Registry hit with timestamp ID
		{"automation.office_occupied", "1751512590941"},
		{"automation.office_bathroom_occupancy_lights", "1750957290157"},
		// Registry hit where config id matches entity suffix
		{"automation.vibe_default_fallback", "vibe_default_fallback"},
		// Registry miss — falls back to prefix stripping
		{"automation.unknown_automation", "unknown_automation"},
		// No prefix — passes through unchanged
		{"bare_id", "bare_id"},
	}

	for _, tt := range tests {
		got := r.Resolve(tt.input)
		if got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolverNil(t *testing.T) {
	// A nil resolver falls back to removing the automation prefix.
	var r *AutomationIDResolver
	got := r.Resolve("automation.office_occupied")
	if got != "office_occupied" {
		t.Errorf("nil resolver Resolve() = %q, want %q", got, "office_occupied")
	}
}
