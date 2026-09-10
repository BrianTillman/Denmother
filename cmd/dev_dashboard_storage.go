package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"gopkg.in/yaml.v3"
)

type dashboardSource struct {
	Slug string     `json:"slug"`
	File string     `json:"file,omitempty"`
	Mode string     `json:"mode"`
	Root *yaml.Node `json:"-"`
}

type liveDashboardRegistration struct {
	URLPath string `json:"url_path"`
	Mode    string `json:"mode"`
	Title   string `json:"title"`
}

type dashboardWS interface {
	SendCommandContext(context.Context, string, map[string]interface{}) (json.RawMessage, error)
}

func connectDevDashboard(ctx context.Context) (*hasync.WSClient, error) {
	resolved, err := operator.ResolveTargetContext(ctx, haconfig.InstanceDev, haconfig.InstanceFlags{ConfigPath: configFile("")}, operator.ModeReadOnly, true)
	if err != nil {
		return nil, err
	}
	ws := hasync.NewWSClient(resolved.Config.URL, resolved.Config.Token)
	if err := ws.ConnectContext(ctx); err != nil {
		return nil, err
	}
	return ws, nil
}

func listLiveStorageDashboards(ctx context.Context, ws dashboardWS) ([]liveDashboardRegistration, error) {
	data, err := ws.SendCommandContext(ctx, "lovelace/dashboards/list", nil)
	if err != nil {
		return nil, err
	}
	var all []liveDashboardRegistration
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	var result []liveDashboardRegistration
	hasDefault := false
	for _, item := range all {
		if item.URLPath == "lovelace" {
			hasDefault = true
		}
		if item.Mode == "storage" {
			result = append(result, item)
		}
	}
	// The default dashboard may be absent from the dashboard collection.
	if !hasDefault {
		result = append(result, liveDashboardRegistration{URLPath: "lovelace", Mode: "storage", Title: "Default dashboard"})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URLPath < result[j].URLPath })
	return result, nil
}

func readStorageDashboard(ctx context.Context, ws dashboardWS, slug string) (*dashboardSource, error) {
	registrations, err := listLiveStorageDashboards(ctx, ws)
	if err != nil {
		return nil, err
	}
	found := false
	for _, registration := range registrations {
		if registration.URLPath == slug {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("storage dashboard %q was not found; use --storage --list", slug)
	}
	if slug == "" || strings.ContainsAny(slug, "/\\") || slug == "." || slug == ".." {
		return nil, fmt.Errorf("invalid storage dashboard slug %q", slug)
	}
	var urlPath any = slug
	if slug == "lovelace" {
		urlPath = nil
	}
	data, err := ws.SendCommandContext(ctx, "lovelace/config", map[string]interface{}{"url_path": urlPath})
	if err != nil {
		return nil, fmt.Errorf("read storage dashboard %q: %w (save an explicit dashboard configuration in HA before inspection)", slug, err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, fmt.Errorf("storage dashboard %q returned an invalid configuration", slug)
	}
	var root yaml.Node
	if err := root.Encode(object); err != nil {
		return nil, err
	}
	return &dashboardSource{Slug: slug, Mode: "storage", Root: &root}, nil
}

func resolveDashboardSource(ctx context.Context, configDir, target string, storage bool) (*dashboardSource, error) {
	if !storage {
		slug, file, err := resolveDashboardFile(configDir, target)
		if err != nil {
			return nil, err
		}
		root, err := loadDashboardYAML(file)
		if err != nil {
			return nil, err
		}
		return &dashboardSource{Slug: slug, File: file, Mode: "yaml", Root: root}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ws, err := connectDevDashboard(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage dashboard inspection requires the selected development HA: %w", err)
	}
	defer ws.Close()
	return readStorageDashboard(ctx, ws, target)
}

func inspectDashboardSource(source *dashboardSource) ([]string, []string, error) {
	if source.Root == nil || source.Root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("dashboard configuration must be a mapping")
	}
	entities, custom := map[string]bool{}, map[string]bool{}
	collectDashboardRefs(source.Root, "", entities, custom)
	return sortedKeys(entities), sortedKeys(custom), nil
}
