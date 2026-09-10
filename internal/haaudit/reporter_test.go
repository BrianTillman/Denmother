package haaudit

import (
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
)

func init() {
	// Disable ANSI codes in test output so assertions match plain text.
	color.NoColor = true
}

func TestFormatVibeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"input_boolean.vibe_sleep", "sleep"},
		{"input_boolean.vibe_entertain", "entertain"},
		{"input_boolean.vibe_relax", "relax"},
		{"input_boolean.vibe_normal", "normal"},
		{"input_boolean.other", "other"},
		{"no_dot", "no_dot"},
	}
	for _, c := range cases {
		got := formatVibeName(c.in)
		if got != c.want {
			t.Errorf("formatVibeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatEntityName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"binary_sensor.office_bay_window_dimmer_occupancy", "occupancy"},
		{"binary_sensor.garage_occupancy_sensors", "sensors"},
		{"binary_sensor.occupancy", "occupancy"},
		{"occupancy", "occupancy"},
	}
	for _, c := range cases {
		got := formatEntityName(c.in)
		if got != c.want {
			t.Errorf("formatEntityName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatAttributeExpr(t *testing.T) {
	cases := []struct{ in, want string }{
		{">= 199", "≥199"},
		{"<= 255", "≤255"},
		{"== 204", "204"},
		{"> 0", ">0"},
		{"< 100", "<100"},
		{"84", "84"},
	}
	for _, c := range cases {
		got := formatAttributeExpr(c.in)
		if got != c.want {
			t.Errorf("formatAttributeExpr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{24 * time.Hour, "24h"},
		{6 * time.Hour, "6h"},
		{30 * time.Second, "30s"},
		{10 * time.Second, "10s"},
		{5 * time.Minute, "5m"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		got := formatDuration(c.d)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func makePassResult(name string, n int) *AuditResult {
	events := make([]TriggerEventResult, n)
	for i := range events {
		events[i] = TriggerEventResult{
			Timestamp:  t0.Add(time.Duration(i) * time.Minute),
			ActiveVibe: VibeNormal,
			Expectation: &Expectation{
				State:      "on",
				Attributes: map[string]string{"brightness": ">= 100"},
			},
			Outcome: &OutcomeResult{
				Status:      OutcomePass,
				ActualState: "on",
				ActualAttrs: map[string]interface{}{"brightness": float64(204)},
				Delay:       2 * time.Second,
			},
		}
	}
	return &AuditResult{
		SpecName:      name,
		TriggerEntity: "binary_sensor.office_occupancy",
		TriggerTo:     "on",
		TriggerEvents: events,
		Summary:       ResultSummary{Passed: n},
	}
}

func TestPrintReport_NormalMode_AllPass(t *testing.T) {
	results := []*AuditResult{
		makePassResult("Office Occupied Audit", 12),
		makePassResult("Garage Audit", 6),
	}
	cfg := ReportConfig{
		Verbose:   false,
		HAURL:     "https://hass.example.com",
		Window:    24 * time.Hour,
		Tolerance: 30 * time.Second,
	}

	var buf strings.Builder
	printReportTo(&buf, results, cfg)
	out := buf.String()

	assertContains(t, out, "Production Automation Audit")
	assertContains(t, out, "hass.example.com")
	assertContains(t, out, "24h")
	assertContains(t, out, "30s")
	assertContains(t, out, "Office Occupied Audit")
	assertContains(t, out, "12 triggers")
	assertContains(t, out, "12 passed")
	assertContains(t, out, "Garage Audit")
	assertContains(t, out, "ALL AUDITS PASSED")
}

func TestPrintReport_NormalMode_WithFailures(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Office Audit",
		TriggerEntity: "binary_sensor.occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{
			{
				ActiveVibe:  VibeNormal,
				Expectation: &Expectation{State: "on"},
				Outcome:     &OutcomeResult{Status: OutcomePass, ActualState: "on"},
			},
			{
				ActiveVibe:  VibeNormal,
				Expectation: &Expectation{State: "on"},
				Outcome:     &OutcomeResult{Status: OutcomeMissing},
			},
		},
		Summary: ResultSummary{Passed: 1, Failed: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		HAURL: "https://hass.example.com", Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "failed")
	assertContains(t, out, "AUDIT FAILED")
}

func TestPrintReport_VerboseMode_Pass(t *testing.T) {
	result := makePassResult("Office Audit", 1)
	result.TriggerEvents[0].Timestamp = t0

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "✓")
	assertContains(t, out, "occupancy→on")
	assertContains(t, out, "vibe: normal")
	assertContains(t, out, "brightness 204")
	assertContains(t, out, "≥100")
	assertContains(t, out, "[+2.0s]")
}

func TestPrintReport_VerboseMode_Missing(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Office Audit",
		TriggerEntity: "binary_sensor.office_occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:  t0,
				ActiveVibe: VibeSleep,
				Expectation: &Expectation{
					State:      "on",
					Attributes: map[string]string{"brightness": ">= 84"},
				},
				Outcome: &OutcomeResult{Status: OutcomeMissing},
			},
		},
		Summary: ResultSummary{Failed: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "✗")
	assertContains(t, out, "vibe: sleep")
	assertContains(t, out, "[MISSING]")
	assertContains(t, out, "≥84")
}

func TestPrintReport_VerboseMode_WrongState(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Office Audit",
		TriggerEntity: "binary_sensor.office_occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   t0,
				ActiveVibe:  VibeNormal,
				Expectation: &Expectation{State: "on"},
				Outcome: &OutcomeResult{
					Status:      OutcomeWrongState,
					ActualState: "off",
					Delay:       time.Second,
				},
			},
		},
		Summary: ResultSummary{Failed: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "✗")
	assertContains(t, out, "off")
	assertContains(t, out, "[WRONG_STATE]")
}

func TestPrintReport_VerboseMode_NoChange(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Vanity Audit",
		TriggerEntity: "binary_sensor.vanity_occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{{
			Timestamp:   t0,
			ActiveVibe:  VibeSleep,
			Expectation: &Expectation{EntityID: "light.vanity", NoChange: true},
			Outcome:     &OutcomeResult{Status: OutcomePass},
		}},
		Summary: ResultSummary{Passed: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	assertContains(t, buf.String(), "no state change")
}

func TestPrintReport_VerboseMode_WrongBrightness(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Office Audit",
		TriggerEntity: "binary_sensor.office_occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:  t0,
				ActiveVibe: VibeRelax,
				Expectation: &Expectation{
					State:      "on",
					Attributes: map[string]string{"brightness": ">= 186"},
				},
				Outcome: &OutcomeResult{
					Status:      OutcomeWrongBrightness,
					ActualState: "on",
					ActualAttrs: map[string]interface{}{"brightness": float64(100)},
					Delay:       500 * time.Millisecond,
				},
			},
		},
		Summary: ResultSummary{Failed: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "✗")
	assertContains(t, out, "brightness 100")
	assertContains(t, out, "≥186")
	assertContains(t, out, "vibe: relax")
}

func TestPrintReport_VerboseMode_NoData(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Office Audit",
		TriggerEntity: "binary_sensor.office_occupancy",
		TriggerTo:     "on",
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   t0,
				ActiveVibe:  VibeEntertain,
				Expectation: nil,
				Outcome:     nil,
			},
		},
		Summary: ResultSummary{NoData: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "○")
	assertContains(t, out, "entertain")
}

func TestPrintReport_VerboseMode_Canceled(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Vanity Vacancy Audit",
		TriggerEntity: "binary_sensor.vanity_occupancy",
		TriggerTo:     "off",
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:  t0,
				ActiveVibe: VibeNormal,
				Outcome: &OutcomeResult{
					Status:  OutcomeCanceled,
					Delay:   time.Minute,
					Details: "canceled by binary_sensor.vanity_occupancy→on",
				},
			},
		},
		Summary: ResultSummary{Canceled: 1},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		Verbose: true, HAURL: "https://hass.example.com",
		Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "[canceled by binary_sensor.vanity_occupancy→on]")
	assertContains(t, out, "Canceled: 1")
}

func TestPrintReport_Summary_ZeroTriggers(t *testing.T) {
	result := &AuditResult{
		SpecName:      "Empty Audit",
		TriggerEntity: "binary_sensor.occupancy",
		TriggerTo:     "on",
		TriggerEvents: nil,
		Summary:       ResultSummary{},
	}

	var buf strings.Builder
	printReportTo(&buf, []*AuditResult{result}, ReportConfig{
		HAURL: "https://hass.example.com", Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "1 audits, 0 trigger events")
	assertContains(t, out, "NO DATA")
}

func TestPrintReport_Summary_Totals(t *testing.T) {
	results := []*AuditResult{
		makePassResult("Spec A", 4),
		makePassResult("Spec B", 8),
	}

	var buf strings.Builder
	printReportTo(&buf, results, ReportConfig{
		HAURL: "https://hass.example.com", Window: 24 * time.Hour, Tolerance: 30 * time.Second,
	})
	out := buf.String()

	assertContains(t, out, "2 audits, 12 trigger events")
	assertContains(t, out, "Passed: 12")
	assertContains(t, out, "Failed: 0")
}

func assertContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("output does not contain %q\nFull output:\n%s", substr, output)
	}
}
