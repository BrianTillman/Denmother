package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
)

// Opt-in only: create, inspect, render, and delete one uniquely named dashboard
// in the selected portable development runtime. The test never starts/stops HA.
func TestStorageDashboardLiveAcceptance(t *testing.T) {
	config := os.Getenv("DENMOTHER_TEST_STORAGE_CONFIG")
	if config == "" {
		t.Skip("set DENMOTHER_TEST_STORAGE_CONFIG to an already running portable development config")
	}
	absolute, err := filepath.Abs(config)
	if err != nil {
		t.Fatal(err)
	}
	prior, priorExplicit, priorViews := configPath, configExplicit, devDashboardViews
	configPath, configExplicit, devDashboardViews = absolute, true, nil
	defer func() { configPath, configExplicit, devDashboardViews = prior, priorExplicit, priorViews }()
	env, err := currentDevEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(env.ComposeFile), "token.env"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(strings.TrimPrefix(string(data), "HASS_BEARER_TOKEN="))
	if token == "" {
		t.Fatal("selected portable runtime has no token")
	}
	t.Setenv("HASS_DEV_URL", env.HAURL)
	t.Setenv("HASS_DEV_TOKEN", token)
	t.Setenv("HASS_DEV_USERNAME", "")
	t.Setenv("HASS_DEV_PASSWORD", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ws := hasync.NewWSClient(env.HAURL, token)
	if err := ws.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	slug := fmt.Sprintf("denmother-storage-acceptance-%d", time.Now().UnixNano())
	data, err = ws.SendCommandContext(ctx, "lovelace/dashboards/create", map[string]interface{}{"url_path": slug, "title": "Denmother Storage Acceptance", "mode": "storage", "show_in_sidebar": false, "require_admin": true})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created dashboard has no ID")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := ws.SendCommandContext(cleanup, "lovelace/dashboards/delete", map[string]interface{}{"dashboard_id": created.ID}); err != nil {
			t.Errorf("delete test dashboard %s: %v", slug, err)
		}
	}()
	configData := map[string]interface{}{"title": "Storage acceptance", "views": []any{
		map[string]any{"path": "overview", "cards": []any{map[string]any{"type": "markdown", "content": "Storage acceptance overview"}}},
		map[string]any{"path": "details", "cards": []any{map[string]any{"type": "markdown", "content": "Storage acceptance details"}}},
	}}
	if _, err := ws.SendCommandContext(ctx, "lovelace/config/save", map[string]interface{}{"url_path": slug, "config": configData}); err != nil {
		t.Fatal(err)
	}
	source, err := resolveDashboardSource(ctx, absolute, slug, true)
	if err != nil {
		t.Fatal(err)
	}
	status, summary, result := runDashboardRenderCheck(ctx, source)
	if status != operator.StatusSuccess || result == nil || len(result.Views) != 2 {
		t.Fatalf("live storage render: %s: %+v", summary, result)
	}
	t.Logf("rendered and cleaned up %s with %d views; screenshot %s", slug, len(result.Views), result.Screenshot)
}
