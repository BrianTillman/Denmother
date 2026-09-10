package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsBelongOnlyToSelectedConfiguration(t *testing.T) {
	root := t.TempDir()
	data := []byte("version: 1\nconfig_dir: custom\nreferences_dir: inventory\nmanaged_repository: true\nforbidden_integrations: [demo]\n")
	if err := os.WriteFile(filepath.Join(root, Filename), data, 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := ForConfig(filepath.Join(root, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ManagedRepository || settings.ReferencesDir != filepath.Join(root, "inventory") {
		t.Fatalf("%+v", settings)
	}
	alternate, err := ForConfig(filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if alternate.ManagedRepository || len(alternate.ForbiddenIntegrations) != 0 {
		t.Fatal("alternate configuration inherited home policy")
	}
	discovered, err := DiscoverConfig(root)
	if err != nil || discovered != settings.ConfigDir {
		t.Fatalf("%s %v", discovered, err)
	}
}
func TestMalformedProjectSettingsFailClosed(t *testing.T) {
	for _, contents := range []string{"version: 99\n", "version: 1\nunrecognized: true\n", "version: 1\n---\nversion: 1\n"} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, Filename), []byte(contents), 0600)
		if _, err := ForConfig(filepath.Join(root, "ha-config")); err == nil {
			t.Fatalf("accepted %q", contents)
		}
	}
}

func TestDevelopmentDataPathsBelongToSelectedProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Filename), []byte("version: 1\nconfig_dir: custom\ndev_fixtures: examples/fixtures.json\ndev_scenarios: examples/scenarios.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := ForConfig(filepath.Join(root, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.DevFixtures != filepath.Join(root, "examples", "fixtures.json") || settings.DevScenarios != filepath.Join(root, "examples", "scenarios.json") {
		t.Fatalf("%+v", settings)
	}
	alternate, err := ForConfig(filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if alternate.DevFixtures != filepath.Join(root, ".devcontainer", "dev-state-fixtures.json") || alternate.DevScenarios != filepath.Join(root, ".devcontainer", "scenarios.json") {
		t.Fatalf("alternate inherited another configuration's data: %+v", alternate)
	}
}
