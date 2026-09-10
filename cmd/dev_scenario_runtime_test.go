package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
)

func TestScenarioFilesReportMissingMalformedAndInvalidStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenarios.json")
	if _, err := loadDevScenarios(path); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, value := range []string{`{`, `null`, `{}`, `{"warm":{"states":{"sensor.study":{"state":"20"}},"typo":true}}`, `{"warm":{"states":{"../../api":{"state":"20"}}}}`, `{"warm":{"states":{"sensor.study":{}}}}`, `{"warm":{"name":"cold","states":{"sensor.study":{"state":"20"}}}}`, `{"warm":{"states":{"sensor.study":{"state":"20"}}}} {}`} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadDevScenarios(path); err == nil {
			t.Fatalf("accepted invalid scenarios %s", value)
		}
	}
	if err := os.WriteFile(path, []byte(`{"warm":{"description":"Warm room","states":{"sensor.study":{"state":"20"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, err := loadDevScenarios(path)
	if err != nil || scenarios["warm"].Name != "warm" {
		t.Fatalf("%+v: %v", scenarios, err)
	}
}

func TestFixturesSeedMissingStatesWithoutOverwritingYAML(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing credentials")
		}
		if r.Method == http.MethodGet {
			if r.URL.Path != "/prefix/api/states" {
				t.Errorf("wrong endpoint %s", r.URL.Path)
			}
			w.Write([]byte(`[{"entity_id":"input_boolean.real","state":"off"}]`))
			return
		}
		writes = append(writes, r.URL.Path)
		var state devScenarioState
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			t.Error(err)
		}
		if state.State != "21" {
			t.Errorf("wrong state %q", state.State)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	states := map[string]devScenarioState{"input_boolean.real": {State: "on"}, "sensor.synthetic": {State: "21"}}
	if err := seedMissingDevStates(context.Background(), &haconfig.HAConfig{URL: server.URL + "/prefix", Token: "synthetic"}, states); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 || writes[0] != "/prefix/api/states/sensor.synthetic" {
		t.Fatalf("writes = %v", writes)
	}
}

func TestDevelopmentStateCancellationAndHTTPFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := postHAJSONContext(ctx, &haconfig.HAConfig{URL: "http://127.0.0.1:1"}, "/api/states/sensor.study", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); w.Write([]byte("private-body")) }))
	defer server.Close()
	err = seedMissingDevStates(context.Background(), &haconfig.HAConfig{URL: server.URL}, map[string]devScenarioState{"sensor.study": {State: "21"}})
	if err == nil || strings.Contains(err.Error(), "private-body") {
		t.Fatalf("failure privacy: %v", err)
	}
}

func TestFixturePayloadFormatsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixtures.json")
	if states, err := loadDevFixtureStates(path); err != nil || states != nil {
		t.Fatalf("optional fixtures: %v %v", states, err)
	}
	for _, value := range []string{`{"sensor.study":{"state":"21"}}`, `{"schema_version":"dev_state_fixtures.v1","entities":{"sensor.study":{"state":"21"}}}`} {
		os.WriteFile(path, []byte(value), 0600)
		states, err := loadDevFixtureStates(path)
		if err != nil || states["sensor.study"].State != "21" {
			t.Fatalf("%v: %v", states, err)
		}
	}
	for _, value := range []string{`null`, `{"entities":null}`, `{"entities":{"../../bad":{"state":"21"}}}`, `{"entities":{"sensor.study":{"state":21}}}`} {
		os.WriteFile(path, []byte(value), 0600)
		if _, err := loadDevFixtureStates(path); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestDashboardExampleScenarioAndFixtures(t *testing.T) {
	scenarios, err := loadDevScenarios("../examples/dashboard/scenarios.json")
	if err != nil || len(scenarios) != 2 {
		t.Fatalf("example scenarios: %v %v", scenarios, err)
	}
	fixtures, err := loadDevFixtureStates("../examples/dashboard/fixtures.json")
	if err != nil || len(fixtures) != 1 {
		t.Fatalf("example fixtures: %v %v", fixtures, err)
	}
}

func TestFixtureProvenancePreservesOriginalAttributes(t *testing.T) {
	original := devScenarioState{State: "21", Attributes: map[string]any{"unit_of_measurement": "°C"}}
	marked := markDevDisplayState(original, "fixture")
	if marked.Attributes["denmother_source"] != "fixture" || marked.Attributes["unit_of_measurement"] != "°C" {
		t.Fatalf("%+v", marked)
	}
	if _, exists := original.Attributes["denmother_source"]; exists {
		t.Fatal("modified caller attributes")
	}
}

func TestExplicitMissingFixtureFailsBeforeCredentialLookup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".denmother.yaml"), []byte("version: 1\nconfig_dir: ha-config\ndev_fixtures: absent.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := applyPortableDevFixtures(context.Background(), &devEnvironment{ProjectRoot: root, ConfigRoot: filepath.Join(root, "ha-config")})
	if err == nil || !strings.Contains(err.Error(), "configured development fixtures") {
		t.Fatalf("%v", err)
	}
}

func TestPortableReadinessWaitsForIntegrations(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("wrong readiness request")
		}
		attempts++
		if attempts == 1 {
			w.Write([]byte(`{"state":"STARTING"}`))
		} else {
			w.Write([]byte(`{"state":"RUNNING"}`))
		}
	}))
	defer server.Close()
	if err := waitPortableDevRunning(context.Background(), server.URL, "synthetic"); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("checked startup %d times", attempts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitPortableDevRunning(ctx, server.URL, "synthetic"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestPortableReadinessRejectsRecoveryMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"RUNNING","recovery_mode":true}`))
	}))
	defer server.Close()
	if err := waitPortableDevRunning(context.Background(), server.URL, "synthetic"); err == nil || !strings.Contains(err.Error(), "recovery mode") {
		t.Fatalf("%v", err)
	}
}
