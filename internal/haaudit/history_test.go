package haaudit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchHistory(t *testing.T) {
	const responseJSON = `[
		[
			{"entity_id": "binary_sensor.office_occupancy", "state": "on",
			 "attributes": {}, "last_changed": "2026-03-02T08:15:03.123456+00:00"}
		],
		[
			{"entity_id": "light.office_lights_all", "state": "on",
			 "attributes": {"brightness": 204}, "last_changed": "2026-03-02T08:15:04.345678+00:00"},
			{"entity_id": "light.office_lights_all", "state": "off",
			 "attributes": {}, "last_changed": "2026-03-02T08:25:00.000000+00:00"}
		]
	]`

	var gotURL string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseJSON))
	}))
	defer srv.Close()

	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	entityIDs := []string{"binary_sensor.office_occupancy", "light.office_lights_all"}

	timeline, err := FetchHistory(srv.URL, "test-token", entityIDs, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	occ := timeline["binary_sensor.office_occupancy"]
	if len(occ) != 1 {
		t.Errorf("occupancy entries: got %d, want 1", len(occ))
	} else if occ[0].State != "on" {
		t.Errorf("occupancy state: got %q, want %q", occ[0].State, "on")
	}

	lights := timeline["light.office_lights_all"]
	if len(lights) != 2 {
		t.Errorf("light entries: got %d, want 2", len(lights))
	} else {
		if lights[0].State != "on" {
			t.Errorf("light[0] state: got %q, want %q", lights[0].State, "on")
		}
		if lights[1].State != "off" {
			t.Errorf("light[1] state: got %q, want %q", lights[1].State, "off")
		}
		brightness, ok := lights[0].Attributes["brightness"].(float64)
		if !ok {
			t.Errorf("brightness attribute: expected float64, got %T", lights[0].Attributes["brightness"])
		} else if brightness != 204 {
			t.Errorf("brightness: got %v, want 204", brightness)
		}
	}

	if !strings.Contains(gotURL, "filter_entity_id=binary_sensor.office_occupancy,light.office_lights_all") {
		t.Errorf("URL missing filter_entity_id: %s", gotURL)
	}
	if !strings.Contains(gotURL, "significant_changes_only=0") {
		t.Errorf("URL missing significant_changes_only=0: %s", gotURL)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization: got %q, want %q", gotAuth, "Bearer test-token")
	}
}

func TestFetchHistory_PreservesUnavailableBoundaries(t *testing.T) {
	entries := []map[string]interface{}{
		{"entity_id": "light.test", "state": "on", "attributes": map[string]interface{}{}, "last_changed": "2026-03-02T08:00:00+00:00"},
		{"entity_id": "light.test", "state": "unavailable", "attributes": map[string]interface{}{}, "last_changed": "2026-03-02T08:01:00+00:00"},
		{"entity_id": "light.test", "state": "unknown", "attributes": map[string]interface{}{}, "last_changed": "2026-03-02T08:02:00+00:00"},
		{"entity_id": "light.test", "state": "off", "attributes": map[string]interface{}{}, "last_changed": "2026-03-02T08:03:00+00:00"},
	}
	body, _ := json.Marshal([]interface{}{entries})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	timeline, err := FetchHistory(srv.URL, "token", []string{"light.test"}, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(timeline["light.test"]); got != len(entries) {
		t.Fatalf("expected all %d observations, got %d", len(entries), got)
	}
	if got := DetectTriggers(timeline["light.test"], "off"); len(got) != 0 {
		t.Fatalf("recovery must not be a trigger: %v", got)
	}

}

func TestFetchHistory_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	timeline, err := FetchHistory(srv.URL, "token", []string{"light.test"}, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if timeline == nil {
		t.Error("timeline should be non-nil")
	}
	if len(timeline) != 0 {
		t.Errorf("expected empty timeline, got %d entries", len(timeline))
	}
}

func TestFetchHistory_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	_, err := FetchHistory(srv.URL, "bad-token", []string{"light.test"}, start, end)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain %q: %v", "401", err)
	}
}

func TestFetchHistory_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	_, err := FetchHistory(srv.URL, "token", []string{"light.test"}, start, end)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestEnrichHistoryContexts(t *testing.T) {
	changed := time.Date(2026, 7, 26, 7, 52, 7, 295993000, time.UTC)
	history := EntityTimeline{
		"light.office_lights_all": {{
			EntityID:        "light.office_lights_all",
			State:           "on",
			LastChangedTime: changed,
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("entity"); got != "light.office_lights_all" {
			t.Errorf("entity query: got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"entity_id":"light.office_lights_all","state":"on","when":"2026-07-26T07:52:07.295993+00:00","context_entity_id":"automation.office_overhead_fan_dimmer"}]`))
	}))
	defer srv.Close()

	err := EnrichHistoryContexts(srv.URL, "token", history, []string{"light.office_lights_all"}, changed.Add(-time.Minute), changed.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry := history["light.office_lights_all"][0]
	if !entry.ContextKnown || entry.ContextEntityID != "automation.office_overhead_fan_dimmer" {
		t.Errorf("unexpected context attribution: %+v", entry)
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		wantUTC time.Time
	}{
		{
			input:   "2026-03-02T08:15:03.123456+00:00",
			wantErr: false,
			wantUTC: time.Date(2026, 3, 2, 8, 15, 3, 123456000, time.UTC),
		},
		{
			input:   "2026-03-02T08:15:03+00:00",
			wantErr: false,
			wantUTC: time.Date(2026, 3, 2, 8, 15, 3, 0, time.UTC),
		},
		{
			input:   "",
			wantErr: true,
		},
		{
			input:   "not-a-time",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseTime(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if !got.UTC().Equal(tc.wantUTC) {
				t.Errorf("time: got %v, want %v", got.UTC(), tc.wantUTC)
			}
		})
	}
}

func TestLastBefore(t *testing.T) {
	base := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)

	entries := []HistoryEntry{
		{State: "a", LastChangedTime: base.Add(1 * time.Second)},
		{State: "b", LastChangedTime: base.Add(5 * time.Second)},
		{State: "c", LastChangedTime: base.Add(10 * time.Second)},
	}

	tests := []struct {
		name      string
		ts        time.Time
		wantState string
		wantNil   bool
	}{
		{
			name:      "T+6s returns entry at T+5s",
			ts:        base.Add(6 * time.Second),
			wantState: "b",
		},
		{
			name:    "T+0s (before all) returns nil",
			ts:      base,
			wantNil: true,
		},
		{
			name:    "T+1s exact match is exclusive — returns nil",
			ts:      base.Add(1 * time.Second),
			wantNil: true,
		},
		{
			name:      "T+1s+1ns returns entry at T+1s",
			ts:        base.Add(1*time.Second + 1),
			wantState: "a",
		},
		{
			name:      "T+100s (after all) returns last entry",
			ts:        base.Add(100 * time.Second),
			wantState: "c",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LastBefore(entries, tc.ts)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got entry with state %q", got.State)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil entry, got nil")
			}
			if got.State != tc.wantState {
				t.Errorf("state: got %q, want %q", got.State, tc.wantState)
			}
		})
	}
}
