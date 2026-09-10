package hatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetEntityStateDirectTimerActiveIncludesRestoreAttributes(t *testing.T) {
	var payload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/states/timer.fan_mode" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTestClient(server.URL, "test-token")
	if err := client.SetEntityStateDirect("timer.fan_mode", "active", nil); err != nil {
		t.Fatalf("SetEntityStateDirect returned error: %v", err)
	}

	attrs, ok := payload["attributes"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected timer attributes in payload, got %#v", payload["attributes"])
	}
	if attrs["duration"] == "" {
		t.Fatalf("expected duration attribute, got %#v", attrs)
	}
	finishesAt, ok := attrs["finishes_at"].(string)
	if !ok || finishesAt == "" {
		t.Fatalf("expected finishes_at attribute, got %#v", attrs["finishes_at"])
	}
	if _, err := time.Parse(time.RFC3339, finishesAt); err != nil {
		t.Fatalf("finishes_at is not RFC3339: %v", err)
	}
}

func TestSetEntityStateViaServiceSupportsTimerStates(t *testing.T) {
	var paths []string
	var payloads []map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		paths = append(paths, r.URL.Path)
		payloads = append(payloads, payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTestClient(server.URL, "test-token")
	if err := client.setEntityStateViaService("timer.fan_mode", "active", nil); err != nil {
		t.Fatalf("active timer service returned error: %v", err)
	}
	if err := client.setEntityStateViaService("timer.fan_mode", "idle", nil); err != nil {
		t.Fatalf("idle timer service returned error: %v", err)
	}

	expectedPaths := []string{
		"/api/services/timer/start",
		"/api/services/timer/cancel",
	}
	for i, expected := range expectedPaths {
		if paths[i] != expected {
			t.Fatalf("path %d: expected %s, got %s", i, expected, paths[i])
		}
		if payloads[i]["entity_id"] != "timer.fan_mode" {
			t.Fatalf("payload %d missing entity_id: %#v", i, payloads[i])
		}
	}
	if payloads[0]["duration"] != "00:01:00" {
		t.Fatalf("expected active timer setup to include duration, got %#v", payloads[0])
	}
}
