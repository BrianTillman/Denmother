package haaudit

import (
	"testing"
	"time"
)

// makeEntry creates a HistoryEntry with the given state and LastChangedTime.
func makeEntry(state string, t time.Time) HistoryEntry {
	return HistoryEntry{
		State:           state,
		LastChangedTime: t,
	}
}

// makeEntryWithAttrs creates a HistoryEntry with state, time, and attributes.
func makeEntryWithAttrs(state string, t time.Time, attrs map[string]interface{}) HistoryEntry {
	return HistoryEntry{
		State:           state,
		Attributes:      attrs,
		LastChangedTime: t,
	}
}

var t0 = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

func TestDetectTriggers_SimpleTransition(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("off", t0),
		makeEntry("on", t0.Add(time.Second)),
	}
	triggers := DetectTriggers(entries, "on")
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if !triggers[0].Timestamp.Equal(t0.Add(time.Second)) {
		t.Errorf("unexpected trigger timestamp: %v", triggers[0].Timestamp)
	}
}

func TestDetectTriggers_NoTrigger_FirstEntryOn(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("on", t0),
		makeEntry("on", t0.Add(time.Second)),
	}
	triggers := DetectTriggers(entries, "on")
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestDetectTriggers_MultipleTransitions(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("off", t0),
		makeEntry("on", t0.Add(1*time.Second)),
		makeEntry("off", t0.Add(2*time.Second)),
		makeEntry("on", t0.Add(3*time.Second)),
	}
	triggers := DetectTriggers(entries, "on")
	if len(triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(triggers))
	}
}

func TestDetectTriggers_NoTriggers_EmptyWindow(t *testing.T) {
	triggers := DetectTriggers(nil, "on")
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestDetectTriggers_NoTriggers_NoStateChange(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("on", t0),
		makeEntry("on", t0.Add(time.Second)),
		makeEntry("on", t0.Add(2*time.Second)),
	}
	triggers := DetectTriggers(entries, "on")
	if len(triggers) != 0 {
		t.Fatalf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestDetectTriggersRetainsRecoveryBaseline(t *testing.T) {
	for _, states := range [][]string{
		{"on", "unavailable", "off", "on"},
		{"on", "unknown", "unavailable", "off", "on"},
		{"unavailable", "off", "on"},
	} {
		var entries []HistoryEntry
		for i, state := range states {
			entries = append(entries, makeEntry(state, t0.Add(time.Duration(i)*time.Second)))
		}
		triggers := DetectTriggers(entries, "on")
		if len(triggers) != 1 || !triggers[0].Timestamp.Equal(entries[len(entries)-1].LastChangedTime) {
			t.Fatalf("states %v: expected only final trigger, got %v", states, triggers)
		}
		if got := DetectTriggers(entries[:len(entries)-1], "off"); len(got) != 0 {
			t.Fatalf("recovery itself must not create cancellation/trigger events: %v", got)
		}
	}
}

func TestResolveVibe_Sleep(t *testing.T) {
	history := EntityTimeline{
		VibeSleep: {makeEntry("on", t0.Add(-time.Minute))},
	}
	got := resolveVibe(history, t0)
	if got != VibeSleep {
		t.Errorf("expected %q, got %q", VibeSleep, got)
	}
}

func TestResolveVibe_LegacySleepAlias(t *testing.T) {
	history := EntityTimeline{
		"input_boolean.vibe_sleeping": {makeEntry("on", t0.Add(-time.Minute))},
	}
	got := resolveVibeWithLegacyAliases(history, t0)
	if got != VibeSleep {
		t.Errorf("expected %q, got %q", VibeSleep, got)
	}
}

func TestResolveVibe_LegacyAliasIgnoredByDefault(t *testing.T) {
	history := EntityTimeline{
		"input_boolean.vibe_sleeping": {makeEntry("on", t0.Add(-time.Minute))},
	}
	got := resolveVibe(history, t0)
	if got != VibeNormal {
		t.Errorf("expected %q, got %q", VibeNormal, got)
	}
}

func TestResolveVibe_Fallback(t *testing.T) {
	history := EntityTimeline{}
	got := resolveVibe(history, t0)
	if got != VibeNormal {
		t.Errorf("expected %q, got %q", VibeNormal, got)
	}
}

func TestResolveVibe_Priority_SleepWins(t *testing.T) {
	history := EntityTimeline{
		VibeSleep:     {makeEntry("on", t0.Add(-time.Minute))},
		VibeEntertain: {makeEntry("on", t0.Add(-time.Minute))},
	}
	got := resolveVibe(history, t0)
	if got != VibeSleep {
		t.Errorf("expected %q, got %q", VibeSleep, got)
	}
}

func TestResolveVibe_Relax(t *testing.T) {
	history := EntityTimeline{
		VibeSleep:     {makeEntry("off", t0.Add(-time.Minute))},
		VibeEntertain: {makeEntry("off", t0.Add(-time.Minute))},
		VibeRelax:     {makeEntry("on", t0.Add(-time.Minute))},
	}
	got := resolveVibe(history, t0)
	if got != VibeRelax {
		t.Errorf("expected %q, got %q", VibeRelax, got)
	}
}

func TestMatchOutcome_Pass(t *testing.T) {
	exp := Expectation{State: "on", Attributes: map[string]string{"brightness": ">= 100"}}
	entries := []HistoryEntry{
		makeEntryWithAttrs("on", t0.Add(2*time.Second), map[string]interface{}{"brightness": float64(150)}),
	}
	outcome := matchOutcome(entries, exp, t0, 10*time.Second)
	if outcome.Status != OutcomePass {
		t.Errorf("expected pass, got %q (details: %s)", outcome.Status, outcome.Details)
	}
	if outcome.Delay != 2*time.Second {
		t.Errorf("expected 2s delay, got %v", outcome.Delay)
	}
}

func TestMatchOutcome_Missing(t *testing.T) {
	exp := Expectation{State: "on"}
	outcome := matchOutcome(nil, exp, t0, 10*time.Second)
	if outcome.Status != OutcomeMissing {
		t.Errorf("expected missing, got %q", outcome.Status)
	}
}

func TestMatchOutcome_WrongState(t *testing.T) {
	exp := Expectation{State: "on"}
	entries := []HistoryEntry{
		makeEntry("off", t0.Add(2*time.Second)),
	}
	outcome := matchOutcome(entries, exp, t0, 10*time.Second)
	if outcome.Status != OutcomeWrongState {
		t.Errorf("expected wrong_state, got %q", outcome.Status)
	}
}

func TestMatchOutcome_TransientWrongStateThenPass(t *testing.T) {
	exp := Expectation{State: "on"}
	entries := []HistoryEntry{
		makeEntry("off", t0.Add(2*time.Second)),
		makeEntry("on", t0.Add(3*time.Second)),
	}
	outcome := matchOutcome(entries, exp, t0, 10*time.Second)
	if outcome.Status != OutcomePass {
		t.Errorf("expected pass after transient wrong state, got %q", outcome.Status)
	}
	if outcome.Delay != 3*time.Second {
		t.Errorf("expected 3s delay, got %v", outcome.Delay)
	}
}

func TestMatchOutcome_WrongBrightness(t *testing.T) {
	exp := Expectation{State: "on", Attributes: map[string]string{"brightness": ">= 200"}}
	entries := []HistoryEntry{
		makeEntryWithAttrs("on", t0.Add(2*time.Second), map[string]interface{}{"brightness": float64(100)}),
	}
	outcome := matchOutcome(entries, exp, t0, 10*time.Second)
	if outcome.Status != OutcomeWrongBrightness {
		t.Errorf("expected wrong_brightness, got %q (details: %s)", outcome.Status, outcome.Details)
	}
}

func TestMatchOutcome_TransientWrongBrightnessThenPass(t *testing.T) {
	exp := Expectation{State: "on", Attributes: map[string]string{"brightness": ">= 200"}}
	entries := []HistoryEntry{
		makeEntryWithAttrs("on", t0.Add(2*time.Second), map[string]interface{}{"brightness": float64(100)}),
		makeEntryWithAttrs("on", t0.Add(4*time.Second), map[string]interface{}{"brightness": float64(220)}),
	}
	outcome := matchOutcome(entries, exp, t0, 10*time.Second)
	if outcome.Status != OutcomePass {
		t.Errorf("expected pass after transient wrong brightness, got %q (details: %s)", outcome.Status, outcome.Details)
	}
	if outcome.Delay != 4*time.Second {
		t.Errorf("expected 4s delay, got %v", outcome.Delay)
	}
}

func TestMatchOutcome_AtWindowEdge(t *testing.T) {
	exp := Expectation{State: "on"}
	tolerance := 10 * time.Second
	entries := []HistoryEntry{
		makeEntry("on", t0.Add(tolerance)), // exactly at window edge
	}
	outcome := matchOutcome(entries, exp, t0, tolerance)
	if outcome.Status != OutcomePass {
		t.Errorf("expected pass at window edge, got %q", outcome.Status)
	}
}

func TestCheckNoStateChange_Pass(t *testing.T) {
	outcome := checkNoStateChange(nil, "automation.test", t0, 10*time.Second)
	if outcome.Status != OutcomePass {
		t.Errorf("expected pass, got %q", outcome.Status)
	}
}

func TestCheckNoStateChange_Fail(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("on", t0.Add(2*time.Second)),
	}
	outcome := checkNoStateChange(entries, "automation.test", t0, 10*time.Second)
	if outcome.Status != OutcomeWrongState {
		t.Errorf("expected wrong_state, got %q", outcome.Status)
	}
}

func TestCheckNoStateChange_IgnoresExternallyAttributedChange(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("off", t0.Add(-time.Second)),
		makeEntry("on", t0.Add(2*time.Second)),
	}
	entries[1].ContextKnown = true
	entries[1].ContextEntityID = "automation.dimmer_mirror"

	outcome := checkNoStateChange(entries, "automation.test", t0, 10*time.Second)
	if outcome.Status != OutcomePass {
		t.Errorf("expected externally attributed change to pass, got %q", outcome.Status)
	}
}

func TestCheckNoStateChange_FailsChangeAttributedToAuditedAutomation(t *testing.T) {
	entries := []HistoryEntry{
		makeEntry("off", t0.Add(-time.Second)),
		makeEntry("on", t0.Add(2*time.Second)),
	}
	entries[1].ContextKnown = true
	entries[1].ContextEntityID = "automation.test"

	outcome := checkNoStateChange(entries, "automation.test", t0, 10*time.Second)
	if outcome.Status != OutcomeWrongState {
		t.Errorf("expected audited automation change to fail, got %q", outcome.Status)
	}
}

func TestCompareAttribute_AllOps(t *testing.T) {
	cases := []struct {
		actual   float64
		op       string
		expected float64
		want     bool
	}{
		{200, ">=", 199, true},
		{198, ">=", 199, false},
		{199, "<=", 200, true},
		{201, "<=", 200, false},
		{200, "==", 200, true},
		{201, "==", 200, false},
		{200, ">", 199, true},
		{199, ">", 199, false},
		{198, "<", 199, true},
		{199, "<", 199, false},
	}

	for _, c := range cases {
		got := compareAttribute(c.actual, c.op, c.expected)
		if got != c.want {
			t.Errorf("compareAttribute(%v, %q, %v) = %v, want %v", c.actual, c.op, c.expected, got, c.want)
		}
	}
}

func TestCorrelate_FullFlow(t *testing.T) {
	spec := &AuditSpec{
		Name: "Test Spec",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "on",
		},
		Expectations: []Expectation{
			{
				Vibe:       VibeNormal,
				EntityID:   "light.room",
				State:      "on",
				Attributes: map[string]string{"brightness": ">= 100"},
			},
			{
				Vibe:       VibeRelax,
				EntityID:   "light.room",
				State:      "on",
				Attributes: map[string]string{"brightness": ">= 50"},
			},
		},
	}

	trigger1 := t0.Add(10 * time.Second)
	trigger2 := t0.Add(60 * time.Second)

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
			makeEntry("on", trigger1),
			makeEntry("off", trigger1.Add(30*time.Second)),
			makeEntry("on", trigger2),
		},
		VibeNormal: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		VibeRelax: {
			makeEntry("on", t0.Add(50*time.Second)),
		},
		"light.room": {
			makeEntryWithAttrs("on", trigger1.Add(2*time.Second), map[string]interface{}{"brightness": float64(150)}),
			makeEntryWithAttrs("on", trigger2.Add(2*time.Second), map[string]interface{}{"brightness": float64(80)}),
		},
	}

	result := Correlate(spec, history, 10*time.Second)

	if result.SpecName != "Test Spec" {
		t.Errorf("unexpected SpecName: %q", result.SpecName)
	}
	if len(result.TriggerEvents) != 2 {
		t.Fatalf("expected 2 trigger events, got %d", len(result.TriggerEvents))
	}
	if result.Summary.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", result.Summary.Passed)
	}
	if result.Summary.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Summary.Failed)
	}
}

func TestCorrelate_NoTriggers(t *testing.T) {
	spec := &AuditSpec{
		Name: "No Triggers",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "on",
		},
		Expectations: []Expectation{
			{Vibe: VibeNormal, EntityID: "light.room", State: "on"},
		},
	}

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
		},
	}

	result := Correlate(spec, history, 10*time.Second)

	if len(result.TriggerEvents) != 0 {
		t.Errorf("expected 0 trigger events, got %d", len(result.TriggerEvents))
	}
	if result.Summary.Passed != 0 || result.Summary.Failed != 0 || result.Summary.NoData != 0 {
		t.Errorf("expected all-zero summary, got %+v", result.Summary)
	}
}

func TestCorrelate_BrightnessZero(t *testing.T) {
	spec := &AuditSpec{
		Name: "Brightness Zero",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "on",
		},
		Expectations: []Expectation{
			{
				Vibe:       VibeSleep,
				EntityID:   "light.closet",
				State:      "on",
				Attributes: map[string]string{"brightness": "== 0"},
			},
		},
	}

	triggerTime := t0.Add(10 * time.Second)

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
			makeEntry("on", triggerTime),
		},
		VibeSleep: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.closet": {},
	}

	result := Correlate(spec, history, 10*time.Second)

	if len(result.TriggerEvents) != 1 {
		t.Fatalf("expected 1 trigger event, got %d", len(result.TriggerEvents))
	}
	ev := result.TriggerEvents[0]
	if ev.Outcome == nil {
		t.Fatal("expected non-nil outcome")
	}
	if ev.Outcome.Status != OutcomePass {
		t.Errorf("expected pass, got %q (details: %s)", ev.Outcome.Status, ev.Outcome.Details)
	}
	if ev.Expectation == nil {
		t.Fatal("expected the no-change expectation to be retained")
	}
	if result.Summary.Passed != 1 {
		t.Errorf("expected 1 passed, got %d", result.Summary.Passed)
	}
}

func TestCorrelate_ExplicitNoChangePreservesInitialState(t *testing.T) {
	for _, initialState := range []string{"off", "on"} {
		t.Run(initialState, func(t *testing.T) {
			spec := &AuditSpec{
				Name: "Explicit No Change",
				Trigger: TriggerDef{
					EntityID: "binary_sensor.occupancy",
					To:       "on",
				},
				Expectations: []Expectation{{
					Vibe:     VibeSleep,
					EntityID: "light.vanity",
					NoChange: true,
				}},
			}
			triggerTime := t0.Add(10 * time.Second)
			history := EntityTimeline{
				"binary_sensor.occupancy": {
					makeEntry("off", t0),
					makeEntry("on", triggerTime),
				},
				VibeSleep: {
					makeEntry("on", t0.Add(-time.Minute)),
				},
				"light.vanity": {
					makeEntry(initialState, t0),
				},
			}

			result := Correlate(spec, history, 10*time.Second)
			if got := result.TriggerEvents[0].Outcome.Status; got != OutcomePass {
				t.Fatalf("expected pass from initial state %q, got %q", initialState, got)
			}
			if result.TriggerEvents[0].Expectation == nil || !result.TriggerEvents[0].Expectation.NoChange {
				t.Fatal("expected explicit no-change expectation on result")
			}
		})
	}
}

func TestCorrelate_ExplicitNoChangeIgnoresAttributeOnlyHistory(t *testing.T) {
	triggerTime := t0.Add(10 * time.Second)
	spec := &AuditSpec{
		Name:    "Explicit No Change",
		Trigger: TriggerDef{EntityID: "binary_sensor.occupancy", To: "on"},
		Expectations: []Expectation{
			{Vibe: VibeSleep, EntityID: "light.room", NoChange: true},
		},
	}
	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
			makeEntry("on", triggerTime),
		},
		VibeSleep: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.room": {
			makeEntryWithAttrs("on", t0, map[string]interface{}{"brightness": float64(100)}),
			makeEntryWithAttrs("on", triggerTime.Add(time.Second), map[string]interface{}{"brightness": float64(101)}),
		},
	}

	result := Correlate(spec, history, 10*time.Second)

	if got := result.TriggerEvents[0].Outcome.Status; got != OutcomePass {
		t.Fatalf("expected attribute-only update to pass, got %q", got)
	}
}

func TestCorrelate_ExplicitNoChangeDetectsStateTransition(t *testing.T) {
	triggerTime := t0.Add(10 * time.Second)
	spec := &AuditSpec{
		Name:    "Explicit No Change",
		Trigger: TriggerDef{EntityID: "binary_sensor.occupancy", To: "on"},
		Expectations: []Expectation{
			{Vibe: VibeSleep, EntityID: "light.room", NoChange: true},
		},
	}
	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
			makeEntry("on", triggerTime),
		},
		VibeSleep: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.room": {
			makeEntry("on", t0),
			makeEntry("off", triggerTime.Add(time.Second)),
		},
	}

	result := Correlate(spec, history, 10*time.Second)

	if got := result.TriggerEvents[0].Outcome.Status; got != OutcomeWrongState {
		t.Fatalf("expected real state transition to fail, got %q", got)
	}
}

func TestCorrelate_MultiLight(t *testing.T) {
	spec := &AuditSpec{
		Name: "Multi Light",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "on",
		},
		Expectations: []Expectation{
			{Vibe: VibeNormal, EntityID: "light.room_a", State: "on"},
			{Vibe: VibeNormal, EntityID: "light.room_b", State: "on"},
		},
	}

	triggerTime := t0.Add(5 * time.Second)

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("off", t0),
			makeEntry("on", triggerTime),
		},
		VibeNormal: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.room_a": {
			makeEntry("on", triggerTime.Add(time.Second)),
		},
		"light.room_b": {
			makeEntry("on", triggerTime.Add(2*time.Second)),
		},
	}

	result := Correlate(spec, history, 10*time.Second)

	if len(result.TriggerEvents) != 2 {
		t.Fatalf("expected 2 TriggerEventResults, got %d", len(result.TriggerEvents))
	}
	if result.Summary.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", result.Summary.Passed)
	}
}

func TestCorrelate_CanceledDelayedTrigger(t *testing.T) {
	spec := &AuditSpec{
		Name: "Delayed Vacancy",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "off",
		},
		CanceledBy: []CancelDef{
			{EntityID: "binary_sensor.occupancy", To: "on", Within: "10m"},
		},
		Expectations: []Expectation{
			{Vibe: VibeNormal, EntityID: "light.room", State: "on"},
		},
	}

	triggerTime := t0.Add(5 * time.Second)

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("on", t0),
			makeEntry("off", triggerTime),
			makeEntry("on", triggerTime.Add(time.Minute)),
		},
		VibeNormal: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.room": {
			makeEntry("off", triggerTime.Add(2*time.Minute)),
		},
	}

	result := Correlate(spec, history, 11*time.Minute)

	if len(result.TriggerEvents) != 1 {
		t.Fatalf("expected 1 trigger event, got %d", len(result.TriggerEvents))
	}
	if result.TriggerEvents[0].Outcome.Status != OutcomeCanceled {
		t.Errorf("expected canceled, got %q", result.TriggerEvents[0].Outcome.Status)
	}
	if result.Summary.Canceled != 1 {
		t.Errorf("expected Canceled=1, got %d", result.Summary.Canceled)
	}
	if result.Summary.Failed != 0 {
		t.Errorf("expected Failed=0, got %d", result.Summary.Failed)
	}
}

func TestCorrelate_CancelAfterWindowIgnored(t *testing.T) {
	spec := &AuditSpec{
		Name: "Delayed Vacancy",
		Trigger: TriggerDef{
			EntityID: "binary_sensor.occupancy",
			To:       "off",
		},
		CanceledBy: []CancelDef{
			{EntityID: "binary_sensor.occupancy", To: "on", Within: "10m"},
		},
		Expectations: []Expectation{
			{Vibe: VibeNormal, EntityID: "light.room", State: "on"},
		},
	}

	triggerTime := t0.Add(5 * time.Second)

	history := EntityTimeline{
		"binary_sensor.occupancy": {
			makeEntry("on", t0),
			makeEntry("off", triggerTime),
			makeEntry("on", triggerTime.Add(11*time.Minute)),
		},
		VibeNormal: {
			makeEntry("on", t0.Add(-time.Minute)),
		},
		"light.room": {
			makeEntry("off", triggerTime.Add(time.Minute)),
		},
	}

	result := Correlate(spec, history, 11*time.Minute)

	if result.Summary.Canceled != 0 {
		t.Errorf("expected Canceled=0, got %d", result.Summary.Canceled)
	}
	if result.Summary.Failed != 1 {
		t.Errorf("expected Failed=1, got %d", result.Summary.Failed)
	}
	if result.TriggerEvents[0].Outcome.Status != OutcomeWrongState {
		t.Errorf("expected wrong_state, got %q", result.TriggerEvents[0].Outcome.Status)
	}
}

func TestCorrelate_SleepVacancyOutcomes(t *testing.T) {
	newSpec := func() *AuditSpec {
		return &AuditSpec{
			Name:    "Sleep Vacancy",
			Trigger: TriggerDef{EntityID: "binary_sensor.vanity_occupancy", To: "off"},
			CanceledBy: []CancelDef{
				{EntityID: "binary_sensor.vanity_occupancy", To: "on", Within: "10m"},
			},
			Expectations: []Expectation{
				{Vibe: VibeSleep, EntityID: "light.vanity", State: "off"},
			},
		}
	}

	triggerTime := t0.Add(5 * time.Second)
	baseHistory := func() EntityTimeline {
		return EntityTimeline{
			"binary_sensor.vanity_occupancy": {
				makeEntry("on", t0),
				makeEntry("off", triggerTime),
			},
			VibeSleep: {
				makeEntry("on", t0.Add(-time.Minute)),
			},
		}
	}

	t.Run("already off is idempotent", func(t *testing.T) {
		history := baseHistory()
		history["light.vanity"] = []HistoryEntry{makeEntry("off", t0)}

		result := Correlate(newSpec(), history, 11*time.Minute)
		ResolveIdempotent([]*AuditResult{result}, history)

		if got := result.TriggerEvents[0].Outcome.Status; got != OutcomePassIdempotent {
			t.Fatalf("expected pass_idempotent, got %q", got)
		}
	})

	t.Run("delayed off passes", func(t *testing.T) {
		history := baseHistory()
		history["light.vanity"] = []HistoryEntry{
			makeEntry("on", t0),
			makeEntry("off", triggerTime.Add(10*time.Minute)),
		}

		result := Correlate(newSpec(), history, 11*time.Minute)

		if got := result.TriggerEvents[0].Outcome.Status; got != OutcomePass {
			t.Fatalf("expected pass, got %q", got)
		}
	})

	t.Run("renewed occupancy cancels", func(t *testing.T) {
		history := baseHistory()
		history["binary_sensor.vanity_occupancy"] = append(
			history["binary_sensor.vanity_occupancy"],
			makeEntry("on", triggerTime.Add(time.Minute)),
		)
		history["light.vanity"] = []HistoryEntry{makeEntry("on", t0)}

		result := Correlate(newSpec(), history, 11*time.Minute)

		if got := result.TriggerEvents[0].Outcome.Status; got != OutcomeCanceled {
			t.Fatalf("expected canceled, got %q", got)
		}
	})
}

func TestResolveIdempotent_MissingBecomesIdempotent(t *testing.T) {
	triggerTime := t0.Add(5 * time.Second)
	exp := Expectation{
		Vibe:       VibeNormal,
		EntityID:   "light.room",
		State:      "on",
		Attributes: map[string]string{"brightness": ">= 199"},
	}
	result := &AuditResult{
		Summary: ResultSummary{Failed: 1},
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   triggerTime,
				Expectation: &exp,
				Outcome:     &OutcomeResult{Status: OutcomeMissing},
			},
		},
	}
	history := EntityTimeline{
		"light.room": {
			makeEntryWithAttrs("on", t0, map[string]interface{}{"brightness": float64(204)}),
		},
	}
	ResolveIdempotent([]*AuditResult{result}, history)

	ev := result.TriggerEvents[0]
	if ev.Outcome.Status != OutcomePassIdempotent {
		t.Errorf("expected pass_idempotent, got %q", ev.Outcome.Status)
	}
	if result.Summary.PassedIdempotent != 1 {
		t.Errorf("expected PassedIdempotent=1, got %d", result.Summary.PassedIdempotent)
	}
	if result.Summary.Failed != 0 {
		t.Errorf("expected Failed=0, got %d", result.Summary.Failed)
	}
}

func TestResolveIdempotent_MissingStaysMissing(t *testing.T) {
	// An off light cannot satisfy an expectation that it was already on.
	triggerTime := t0.Add(5 * time.Second)
	exp := Expectation{
		Vibe:     VibeNormal,
		EntityID: "light.room",
		State:    "on",
	}
	result := &AuditResult{
		Summary: ResultSummary{Failed: 1},
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   triggerTime,
				Expectation: &exp,
				Outcome:     &OutcomeResult{Status: OutcomeMissing},
			},
		},
	}
	history := EntityTimeline{
		"light.room": {
			makeEntry("off", t0),
		},
	}
	ResolveIdempotent([]*AuditResult{result}, history)

	ev := result.TriggerEvents[0]
	if ev.Outcome.Status != OutcomeMissing {
		t.Errorf("expected missing, got %q", ev.Outcome.Status)
	}
	if result.Summary.Failed != 1 {
		t.Errorf("expected Failed=1, got %d", result.Summary.Failed)
	}
}

func TestResolveIdempotent_WrongBrightnessUnchanged(t *testing.T) {
	// Wrong brightness outcomes are not upgraded.
	triggerTime := t0.Add(5 * time.Second)
	exp := Expectation{
		Vibe:       VibeNormal,
		EntityID:   "light.room",
		State:      "on",
		Attributes: map[string]string{"brightness": ">= 199"},
	}
	result := &AuditResult{
		Summary: ResultSummary{Failed: 1},
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   triggerTime,
				Expectation: &exp,
				Outcome:     &OutcomeResult{Status: OutcomeWrongBrightness},
			},
		},
	}
	history := EntityTimeline{
		"light.room": {
			makeEntryWithAttrs("on", t0, map[string]interface{}{"brightness": float64(204)}),
		},
	}
	ResolveIdempotent([]*AuditResult{result}, history)

	ev := result.TriggerEvents[0]
	if ev.Outcome.Status != OutcomeWrongBrightness {
		t.Errorf("expected wrong_brightness unchanged, got %q", ev.Outcome.Status)
	}
}

func TestResolveIdempotent_NilExpectation(t *testing.T) {
	// No-change events have no expectation to reclassify as idempotent.
	triggerTime := t0.Add(5 * time.Second)
	result := &AuditResult{
		Summary: ResultSummary{Failed: 1},
		TriggerEvents: []TriggerEventResult{
			{
				Timestamp:   triggerTime,
				Expectation: nil,
				Outcome:     &OutcomeResult{Status: OutcomeMissing},
			},
		},
	}
	ResolveIdempotent([]*AuditResult{result}, EntityTimeline{})

	if result.TriggerEvents[0].Outcome.Status != OutcomeMissing {
		t.Errorf("expected missing unchanged, got %q", result.TriggerEvents[0].Outcome.Status)
	}
}

func TestCorrelateFromTraces_HealthCheck_AllPassed(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Fan Dimmer Health",
		Automation: "automation.fan_dimmer",
		Trigger:    TriggerDef{Source: "traces"},
	}

	start := t0
	end := t0.Add(time.Hour)

	traces := []AutomationTrace{
		{RunID: "run1", Timestamp: t0.Add(10 * time.Second), State: "stopped"},
		{RunID: "run2", Timestamp: t0.Add(30 * time.Second), State: "stopped"},
	}

	result := CorrelateFromTraces(spec, traces, nil, 10*time.Second, start, end)

	if len(result.TriggerEvents) != 2 {
		t.Fatalf("expected 2 trigger events, got %d", len(result.TriggerEvents))
	}
	if result.Summary.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", result.Summary.Passed)
	}
	if result.Summary.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Summary.Failed)
	}
	for i, ev := range result.TriggerEvents {
		if ev.Trace == nil {
			t.Errorf("event %d: expected trace annotation", i)
		}
		if ev.Outcome.Status != OutcomePass {
			t.Errorf("event %d: expected pass, got %q", i, ev.Outcome.Status)
		}
	}
}

func TestCorrelateFromTraces_HealthCheck_WithError(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Fan Dimmer Health",
		Automation: "automation.fan_dimmer",
		Trigger:    TriggerDef{Source: "traces"},
	}

	start := t0
	end := t0.Add(time.Hour)

	traces := []AutomationTrace{
		{RunID: "run1", Timestamp: t0.Add(10 * time.Second), State: "stopped"},
		{RunID: "run2", Timestamp: t0.Add(30 * time.Second), State: "stopped", Error: "service not found"},
	}

	result := CorrelateFromTraces(spec, traces, nil, 10*time.Second, start, end)

	if result.Summary.Passed != 1 {
		t.Errorf("expected 1 passed, got %d", result.Summary.Passed)
	}
	if result.Summary.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", result.Summary.Failed)
	}
	if result.TriggerEvents[1].Outcome.Status != OutcomeTraceError {
		t.Errorf("expected trace_error, got %q", result.TriggerEvents[1].Outcome.Status)
	}
}

func TestCorrelateFromTraces_HealthCheck_RunningFails(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Running Trace Health",
		Automation: "automation.running",
		Trigger:    TriggerDef{Source: "traces"},
	}
	traces := []AutomationTrace{
		{RunID: "running", Timestamp: t0.Add(10 * time.Second), State: "running"},
	}

	result := CorrelateFromTraces(spec, traces, nil, 10*time.Second, t0, t0.Add(time.Hour))

	if result.Summary.Passed != 0 || result.Summary.Failed != 1 {
		t.Fatalf("expected running trace to fail health check, got summary %#v", result.Summary)
	}
	if got := result.TriggerEvents[0].Outcome.Status; got != OutcomeTraceError {
		t.Fatalf("expected trace_error for running trace, got %q", got)
	}
}

func TestCorrelateFromTraces_HealthCheck_AbortedFails(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Aborted Trace Health",
		Automation: "automation.aborted",
		Trigger:    TriggerDef{Source: "traces"},
	}
	traces := []AutomationTrace{
		{RunID: "aborted", Timestamp: t0.Add(10 * time.Second), State: "aborted"},
	}

	result := CorrelateFromTraces(spec, traces, nil, 10*time.Second, t0, t0.Add(time.Hour))

	if result.Summary.Passed != 0 || result.Summary.Failed != 1 {
		t.Fatalf("expected aborted trace to fail health check, got summary %#v", result.Summary)
	}
	if got := result.TriggerEvents[0].Outcome.Status; got != OutcomeTraceError {
		t.Fatalf("expected trace_error for aborted trace, got %q", got)
	}
}

func TestCorrelateFromTraces_FiltersToWindow(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Windowed",
		Automation: "automation.test",
		Trigger:    TriggerDef{Source: "traces"},
	}

	start := t0.Add(20 * time.Second)
	end := t0.Add(time.Hour)

	traces := []AutomationTrace{
		{RunID: "before", Timestamp: t0.Add(5 * time.Second), State: "stopped"},  // outside window
		{RunID: "inside", Timestamp: t0.Add(30 * time.Second), State: "stopped"}, // inside window
	}

	result := CorrelateFromTraces(spec, traces, nil, 10*time.Second, start, end)

	if len(result.TriggerEvents) != 1 {
		t.Fatalf("expected 1 event (filtered), got %d", len(result.TriggerEvents))
	}
	if result.TriggerEvents[0].Trace.RunID != "inside" {
		t.Errorf("expected 'inside' trace, got %q", result.TriggerEvents[0].Trace.RunID)
	}
}

func TestCorrelateFromTraces_WithExpectations(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Fan Dimmer With Expectations",
		Automation: "automation.fan_dimmer",
		Trigger:    TriggerDef{Source: "traces"},
		Expectations: []Expectation{
			{EntityID: "timer.fan_mode", State: "active"},
		},
	}

	start := t0
	end := t0.Add(time.Hour)
	traceTime := t0.Add(10 * time.Second)

	traces := []AutomationTrace{
		{RunID: "run1", Timestamp: traceTime, State: "stopped"},
	}

	history := EntityTimeline{
		"timer.fan_mode": {
			makeEntry("active", traceTime.Add(time.Second)),
		},
	}

	result := CorrelateFromTraces(spec, traces, history, 10*time.Second, start, end)

	if len(result.TriggerEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.TriggerEvents))
	}
	if result.Summary.Passed != 1 {
		t.Errorf("expected 1 passed, got %d", result.Summary.Passed)
	}
	if result.TriggerEvents[0].Outcome.Status != OutcomePass {
		t.Errorf("expected pass, got %q", result.TriggerEvents[0].Outcome.Status)
	}
}

func TestCorrelateFromTraces_NoTraces(t *testing.T) {
	spec := &AuditSpec{
		Name:       "Empty",
		Automation: "automation.test",
		Trigger:    TriggerDef{Source: "traces"},
	}

	result := CorrelateFromTraces(spec, nil, nil, 10*time.Second, t0, t0.Add(time.Hour))

	if len(result.TriggerEvents) != 0 {
		t.Errorf("expected 0 events, got %d", len(result.TriggerEvents))
	}
}
