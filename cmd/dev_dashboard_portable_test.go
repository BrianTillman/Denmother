package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeDashboardTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeDashboardDiscoveryAndIncludes(t *testing.T) {
	dir := t.TempDir()
	writeDashboardTestFile(t, dir, "configuration.yaml", "unrelated: !include absent.yaml\nhttp:\n  password: !secret nonexistent\nlovelace: !include ui/settings.yaml\n")
	writeDashboardTestFile(t, dir, "ui/settings.yaml", "resource_mode: yaml\nresources: !include resources.yaml\ndashboards: !include_dir_merge_named registrations\n")
	writeDashboardTestFile(t, dir, "ui/resources.yaml", "- url: /local/example.js?v=2\n  type: module\n")
	writeDashboardTestFile(t, dir, "ui/registrations/main.yaml", "test-dashboard:\n  mode: yaml\n  filename: ui/main.yaml\n")
	path := writeDashboardTestFile(t, dir, "ui/main.yaml", "views: !include_dir_list views\n")
	writeDashboardTestFile(t, dir, "ui/views/one.yaml", "title: First\npath: overview\ncards: !include ../cards.yaml\n")
	writeDashboardTestFile(t, dir, "ui/views/two.yaml", "title: Second\ncards:\n  - type: entity\n    entity: sensor.second\n")
	writeDashboardTestFile(t, dir, "ui/cards.yaml", "- &card\n  type: custom:example-card\n  entity: sensor.first\n- *card\n")
	targets, err := listDashboardTargets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []dashboardTarget{{Slug: "test-dashboard", File: path}}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v", targets)
	}
	slug, resolved, err := resolveDashboardFile(dir, "ui/main.yaml")
	if err != nil || slug != "test-dashboard" || resolved != path {
		t.Fatalf("resolve = %s %s %v", slug, resolved, err)
	}
	entities, cards, err := inspectDashboardYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entities, []string{"sensor.first", "sensor.second"}) || !reflect.DeepEqual(cards, []string{"example-card"}) {
		t.Fatalf("refs = %v %v", entities, cards)
	}
	resources, err := configuredDashboardResources(dir)
	if err != nil || !reflect.DeepEqual(resources, []string{"/local/example.js?v=2"}) {
		t.Fatalf("resources = %v %v", resources, err)
	}
	for _, tc := range []struct{ selection, want []string }{{nil, []string{"overview", "1"}}, {[]string{"1", "overview", "0"}, []string{"1", "overview"}}} {
		got, err := dashboardViewPaths(path, tc.selection)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("views = %v %v", got, err)
		}
	}
	if _, err := dashboardViewPaths(path, []string{"missing"}); err == nil {
		t.Fatal("accepted nonexistent view")
	}
}

func TestDefaultDashboardDiscoveryRequiresYAMLMode(t *testing.T) {
	dir := t.TempDir()
	path := writeDashboardTestFile(t, dir, "ui-lovelace.yaml", "views: []\n")
	writeDashboardTestFile(t, dir, "configuration.yaml", "lovelace:\n  mode: yaml\n")
	targets, err := listDashboardTargets(dir)
	if err != nil || !reflect.DeepEqual(targets, []dashboardTarget{{Slug: "lovelace", File: path}}) {
		t.Fatalf("targets = %#v, %v", targets, err)
	}
	writeDashboardTestFile(t, dir, "configuration.yaml", "lovelace:\n  mode: storage\n")
	targets, err = listDashboardTargets(dir)
	if err != nil || len(targets) != 0 {
		t.Fatalf("unregistered YAML was exposed: %#v %v", targets, err)
	}
}

func TestDashboardIncludeAndAliasCyclesFail(t *testing.T) {
	for _, contents := range []string{"views: !include main.yaml\n", "views: &loop [*loop]\n"} {
		dir := t.TempDir()
		path := writeDashboardTestFile(t, dir, "main.yaml", contents)
		if _, _, err := inspectDashboardYAML(path); err == nil || !strings.Contains(err.Error(), "cyclic") {
			t.Fatalf("expected cycle error, got %v", err)
		}
	}
}

func TestDashboardResourceAuthenticationAndFailures(t *testing.T) {
	var seenToken string
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>fallback</html>"))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte("export {};"))
		}
	}))
	defer resource.Close()
	if err := fetchDashboardResource(context.Background(), resource.URL, "synthetic-token", "/ok"); err != nil {
		t.Fatal(err)
	}
	if seenToken != "Bearer synthetic-token" {
		t.Fatal("same origin lacked authentication")
	}
	if err := fetchDashboardResource(context.Background(), "http://different.invalid", "synthetic-token", resource.URL+"/ok"); err != nil {
		t.Fatal(err)
	}
	if seenToken != "" {
		t.Fatal("token leaked to an external resource")
	}
	for _, path := range []string{"/missing", "/html", "/redirect"} {
		if err := fetchDashboardResource(context.Background(), resource.URL, "synthetic-token", path); err == nil {
			t.Fatalf("accepted bad resource %s", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fetchDashboardResource(ctx, resource.URL, "synthetic-token", "/ok"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestMissingDashboardRenderResultFails(t *testing.T) {
	if err := loadDashboardRenderResult(filepath.Join(t.TempDir(), "absent.json"), &dashboardRenderCheck{}); err == nil {
		t.Fatal("missing browser result accepted")
	}
}

func TestDashboardMergeKeysMatchHAPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeDashboardTestFile(t, dir, "configuration.yaml", `defaults: &defaults
  mode: yaml
  filename: merged.yaml
lovelace:
  dashboards:
    merged-dashboard:
      <<: *defaults
      title: Merged
`)
	path := writeDashboardTestFile(t, dir, "merged.yaml", `views:
  - <<: &view
      path: inherited
      cards:
        - <<: [{type: entity, entity: sensor.inherited}, {entity: sensor.later}]
          entity: sensor.actual
`)
	targets, err := listDashboardTargets(dir)
	if err != nil || len(targets) != 1 {
		t.Fatalf("merged registration = %v %v", targets, err)
	}
	paths, err := dashboardViewPaths(path, nil)
	if err != nil || !reflect.DeepEqual(paths, []string{"inherited"}) {
		t.Fatalf("merged path = %v %v", paths, err)
	}
	entities, _, err := inspectDashboardYAML(path)
	if err != nil || !reflect.DeepEqual(entities, []string{"sensor.actual"}) {
		t.Fatalf("overridden entity leaked: %v %v", entities, err)
	}
}

func TestDashboardIncludeDirectoryRecursesAndIgnoresYML(t *testing.T) {
	dir := t.TempDir()
	path := writeDashboardTestFile(t, dir, "main.yaml", "views: !include_dir_list views\n")
	writeDashboardTestFile(t, dir, "views/nested/view.yaml", "path: nested\n")
	writeDashboardTestFile(t, dir, "views/ignored.yml", "path: ignored\n")
	paths, err := dashboardViewPaths(path, nil)
	if err != nil || !reflect.DeepEqual(paths, []string{"nested"}) {
		t.Fatalf("directory paths = %v %v", paths, err)
	}
}

func TestDashboardRunnerChecksInstalledVersion(t *testing.T) {
	dir := t.TempDir()
	path := "node_modules/playwright/package.json"
	writeDashboardTestFile(t, dir, path, `{"version":"0.0.1"}`)
	if dashboardRenderPackageCurrent(dir) {
		t.Fatal("stale Playwright accepted")
	}
	writeDashboardTestFile(t, dir, path, `{"version":"`+dashboardRenderPlaywrightVersion+`"}`)
	if !dashboardRenderPackageCurrent(dir) {
		t.Fatal("current Playwright rejected")
	}
}
