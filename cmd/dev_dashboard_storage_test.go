package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fixtureDashboardWS struct {
	commands []string
	params   []map[string]interface{}
	data     map[string]string
	err      error
}

func (ws *fixtureDashboardWS) SendCommandContext(ctx context.Context, command string, params map[string]interface{}) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ws.commands = append(ws.commands, command)
	ws.params = append(ws.params, params)
	if ws.err != nil {
		return nil, ws.err
	}
	return json.RawMessage(ws.data[command]), nil
}
func TestStorageDashboardInspectionAndViewSelection(t *testing.T) {
	ws := &fixtureDashboardWS{data: map[string]string{
		"lovelace/dashboards/list": `[{"url_path":"yaml-dashboard","mode":"yaml"},{"url_path":"saved-dashboard","mode":"storage"}]`,
		"lovelace/config":          `{"views":[{"path":"overview","cards":[{"type":"custom:test-card","entity":"sensor.one"}]},{"cards":[{"type":"entity","entity":"sensor.two"}]}]}`,
	}}
	source, err := readStorageDashboard(context.Background(), ws, "saved-dashboard")
	if err != nil {
		t.Fatal(err)
	}
	entities, custom, err := inspectDashboardSource(source)
	if err != nil || !reflect.DeepEqual(entities, []string{"sensor.one", "sensor.two"}) || !reflect.DeepEqual(custom, []string{"test-card"}) {
		t.Fatalf("inspection: %v %v %v", entities, custom, err)
	}
	paths, err := dashboardNodeViewPaths(source.Root, []string{"1"})
	if err != nil || !reflect.DeepEqual(paths, []string{"1"}) {
		t.Fatalf("views: %v %v", paths, err)
	}
	if !reflect.DeepEqual(ws.commands, []string{"lovelace/dashboards/list", "lovelace/config"}) || ws.params[1]["url_path"] != "saved-dashboard" {
		t.Fatalf("unexpected API calls: %v %v", ws.commands, ws.params)
	}
	if source.Mode != "storage" || source.File != "" {
		t.Fatalf("source: %+v", source)
	}
}
func TestStorageDashboardDefaultsAndErrors(t *testing.T) {
	ws := &fixtureDashboardWS{data: map[string]string{"lovelace/dashboards/list": `[]`, "lovelace/config": `{"views":[]}`}}
	if _, err := readStorageDashboard(context.Background(), ws, "lovelace"); err != nil {
		t.Fatal(err)
	}
	if ws.params[1]["url_path"] != nil {
		t.Fatal("default dashboard must use null url_path")
	}
	if _, err := readStorageDashboard(context.Background(), ws, "missing"); err == nil {
		t.Fatal("unknown slug accepted")
	}
	ws.data["lovelace/dashboards/list"] = `[{"url_path":"lovelace","mode":"yaml"}]`
	if _, err := readStorageDashboard(context.Background(), ws, "lovelace"); err == nil {
		t.Fatal("YAML default treated as storage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readStorageDashboard(ctx, ws, "lovelace"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	ws.err = errors.New("not authorized")
	if _, err := readStorageDashboard(context.Background(), ws, "lovelace"); err == nil {
		t.Fatal("API failure ignored")
	}
}
func TestDashboardBrowserDependencyInstructions(t *testing.T) {
	err := dashboardBrowserDependencyError("linux", "/project/runner", "error while loading shared libraries: libnspr4.so")
	for _, want := range []string{"/project/runner", "npx playwright install-deps chromium", "libnspr4.so"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
	}
}
