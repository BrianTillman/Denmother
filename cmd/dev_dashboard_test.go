package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInspectDashboardYAMLCollectsCustomEntityKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dashboard.yaml")
	content := []byte(`
views:
  - title: Test
    cards:
      - type: custom:clock-weather-card
        entity: weather.ksdl
        sun_entity: sun.sun
        temperature_sensor: sensor.ksdl_temperature
        humidity_sensor: sensor.ksdl_relative_humidity
        tap_action:
          action: call-service
          service: scene.turn_on
      - type: custom:mushroom-entity-card
        entity: light.pool_light
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	entities, cards, err := inspectDashboardYAML(path)
	if err != nil {
		t.Fatalf("inspectDashboardYAML returned error: %v", err)
	}

	wantEntities := []string{
		"light.pool_light",
		"sensor.ksdl_relative_humidity",
		"sensor.ksdl_temperature",
		"sun.sun",
		"weather.ksdl",
	}
	if !reflect.DeepEqual(entities, wantEntities) {
		t.Fatalf("entities = %#v, want %#v", entities, wantEntities)
	}
	wantCards := []string{"clock-weather-card", "mushroom-entity-card"}
	if !reflect.DeepEqual(cards, wantCards) {
		t.Fatalf("cards = %#v, want %#v", cards, wantCards)
	}
}

func TestSanitizeFixtureAttributesKeepsForecastRows(t *testing.T) {
	sanitized := sanitizeFixtureAttributes(map[string]interface{}{
		"friendly_name": "KSDL",
		"forecast": []any{
			map[string]any{
				"condition":          "sunny",
				"native_temperature": 104,
				"datetime":           "2026-06-29T21:00:00+00:00",
				"url":                "https://secret.example",
			},
		},
		"access_token": "secret",
	})

	if _, ok := sanitized["access_token"]; ok {
		t.Fatal("access_token should not be copied")
	}
	forecast, ok := sanitized["forecast"].([]any)
	if !ok || len(forecast) != 1 {
		t.Fatalf("forecast = %#v, want one row", sanitized["forecast"])
	}
	row, ok := forecast[0].(map[string]any)
	if !ok {
		t.Fatalf("forecast row = %T, want map", forecast[0])
	}
	for _, key := range []string{"condition", "native_temperature", "datetime"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("forecast row missing %s: %#v", key, row)
		}
	}
	if _, ok := row["url"]; ok {
		t.Fatalf("forecast row retained url: %#v", row)
	}
}

func TestBuildFixturePayloadMergesExistingByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixtures.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": "dev_state_fixtures.v1",
  "entities": {
    "sensor.keep": {"state": "old", "attributes": {"access_token": "secret", "friendly_name": "Keep"}},
    "sensor.replace": {"state": "old", "attributes": {"friendly_name": "Old"}}
  }
}`), 0644); err != nil {
		t.Fatal(err)
	}

	payload, err := buildFixturePayload(path, map[string]map[string]any{
		"sensor.replace": {"state": "new"},
		"sensor.new":     {"state": "fresh"},
	}, true)
	if err != nil {
		t.Fatalf("buildFixturePayload returned error: %v", err)
	}
	if len(payload.Entities) != 3 {
		t.Fatalf("entity count = %d, want 3", len(payload.Entities))
	}
	if payload.Entities["sensor.replace"]["state"] != "new" {
		t.Fatalf("sensor.replace was not refreshed: %#v", payload.Entities["sensor.replace"])
	}
	keptAttrs, ok := payload.Entities["sensor.keep"]["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("sensor.keep attributes = %T, want map", payload.Entities["sensor.keep"]["attributes"])
	}
	if _, ok := keptAttrs["access_token"]; ok {
		t.Fatalf("merged existing fixture retained access_token: %#v", keptAttrs)
	}

	replaced, err := buildFixturePayload(path, map[string]map[string]any{
		"sensor.only": {"state": "fresh"},
	}, false)
	if err != nil {
		t.Fatalf("replace buildFixturePayload returned error: %v", err)
	}
	if len(replaced.Entities) != 1 || replaced.Entities["sensor.only"]["state"] != "fresh" {
		t.Fatalf("replace payload = %#v, want only refreshed fixture", replaced.Entities)
	}
}

func TestDashboardStateHealthClassification(t *testing.T) {
	if reason := unhealthyDashboardStateReason("automation.sunrise", "unknown"); reason == "" {
		t.Fatal("automation unknown should be unhealthy")
	}
	if reason := unhealthyDashboardStateReason("scene.exterior_day", "unknown"); reason != "" {
		t.Fatalf("scene unknown should be allowed, got %q", reason)
	}
	if reason := unhealthyDashboardStateReason("event.front_yard_front_door_vehicle", "unknown"); reason != "" {
		t.Fatalf("event unknown should be allowed, got %q", reason)
	}
	if reason := unhealthyDashboardStateReason("sensor.air_sensor", "unavailable"); reason == "" {
		t.Fatal("unavailable should be unhealthy")
	}
}

func TestDashboardURLPath(t *testing.T) {
	if got := dashboardURLPath("kitchen-kiosk-yaml"); got != "/kitchen-kiosk-yaml/0" {
		t.Fatalf("dashboardURLPath = %q", got)
	}
	if got := dashboardURLPath("lovelace"); got != "/lovelace/0" {
		t.Fatalf("dashboardURLPath(lovelace) = %q", got)
	}
}

func TestResolveDashboardFileUsesRegisteredSlugForDirectFile(t *testing.T) {
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "dashboards", "definitions"), 0755); err != nil {
		t.Fatal(err)
	}
	dashboardPath := filepath.Join(configDir, "dashboards", "kitchen-kiosk.yaml")
	if err := os.WriteFile(dashboardPath, []byte("views: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	definitionPath := filepath.Join(configDir, "dashboards", "definitions", "core.yaml")
	if err := os.WriteFile(definitionPath, []byte(`
kitchen-kiosk-yaml:
  mode: yaml
  title: Kitchen Kiosk
  filename: dashboards/kitchen-kiosk.yaml
`), 0644); err != nil {
		t.Fatal(err)
	}

	slug, file, err := resolveDashboardFile(configDir, "dashboards/kitchen-kiosk.yaml")
	if err != nil {
		t.Fatalf("resolveDashboardFile returned error: %v", err)
	}
	if slug != "kitchen-kiosk-yaml" {
		t.Fatalf("slug = %q, want kitchen-kiosk-yaml", slug)
	}
	if file != dashboardPath {
		t.Fatalf("file = %q, want %q", file, dashboardPath)
	}
}

func TestDashboardCommandsRequireAnExplicitSelection(t *testing.T) {
	if err := devDashboardCmd.ValidateArgs(nil); err == nil {
		t.Fatal("dashboard command accepted an absent selection")
	}
	if _, err := resolveFixtureDashboardTargets(t.TempDir(), "", false); err == nil {
		t.Fatal("fixture refresh accepted an absent selection")
	}
}

func TestDashboardLoginUsesSelectedRuntimeCredentials(t *testing.T) {
	t.Setenv("HASS_DEV_USERNAME", "")
	t.Setenv("HASS_DEV_PASSWORD", "")
	root := t.TempDir()
	env := &devEnvironment{HAURL: "http://localhost:8123", ComposeFile: filepath.Join(root, "compose.yaml")}
	if err := os.WriteFile(filepath.Join(root, "credentials.json"), []byte(`{"username":"fixture-user","password":"synthetic-password"}`), 0600); err != nil {
		t.Fatal(err)
	}
	credentials, err := dashboardLoginCredentials(env, env.HAURL)
	if err != nil || credentials.Username != "fixture-user" || credentials.Password != "synthetic-password" {
		t.Fatal("did not load selected runtime login")
	}
	if _, err := dashboardLoginCredentials(env, "http://localhost:9123"); err == nil {
		t.Fatal("reused credentials for another runtime")
	}
	t.Setenv("HASS_DEV_USERNAME", "custom-user")
	if _, err := dashboardLoginCredentials(env, env.HAURL); err == nil {
		t.Fatal("accepted incomplete explicit login")
	}
	t.Setenv("HASS_DEV_PASSWORD", "synthetic-custom-password")
	credentials, err = dashboardLoginCredentials(env, "http://localhost:9123")
	if err != nil || credentials.Username != "custom-user" {
		t.Fatal("did not accept explicit custom runtime login")
	}
}

func TestDashboardDiagnosticsRemoveURLCredentials(t *testing.T) {
	file := filepath.Join(t.TempDir(), "render.json")
	data := `{"request_failures":["HTTP 502 https://fixture-user:synthetic-password@ha.example/api/map_tiles/tilejson.json?token=synthetic-private-token#private-fragment"],"views":[{"page_errors":["https://ha.example/card?auth=synthetic-private-token"]}]}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var report dashboardRenderCheck
	if err := loadDashboardRenderResult(file, &report); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"fixture-user", "synthetic-password", "synthetic-private-token", "private-fragment"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("diagnostics retained %s", private)
		}
	}
	if !strings.Contains(string(encoded), "/api/map_tiles/tilejson.json") {
		t.Fatal("lost request path evidence")
	}
}
