package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haverify"
)

func writeConfigFixture(t *testing.T, root, id string) {
	t.Helper()

	paths := map[string]string{
		filepath.Join(root, "tests", id+"_test.yaml"):                          "name: " + id + "\n",
		filepath.Join(root, "automations", "config", id+".yaml"):               "- id: " + id + "\n  use_blueprint:\n    path: example/" + id + ".yaml\n",
		filepath.Join(root, "blueprints", "automation", "example", id+".yaml"): "blueprint:\n  name: " + id + "\n",
	}
	for path, body := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func TestDiscoverKeepsSelectedConfigRootSeparate(t *testing.T) {
	parent := t.TempDir()
	t.Chdir(parent)

	defaultRoot := filepath.Join(parent, "ha-config")
	altRoot := filepath.Join(parent, "alt-config")
	writeConfigFixture(t, defaultRoot, "default_only")
	writeConfigFixture(t, altRoot, "alt_only")

	oldConfig := configPath
	oldPattern := verifyPattern
	configPath = "alt-config"
	verifyPattern = ""
	t.Cleanup(func() {
		configPath = oldConfig
		verifyPattern = oldPattern
	})

	doctorFiles, err := allTestFiles()
	if err != nil || len(doctorFiles) != 1 || !strings.Contains(doctorFiles[0], "alt_only") {
		t.Fatalf("doctor scope: %v, %v", doctorFiles, err)
	}
	known := knownAutomationEntityIDs()
	if !known["automation.alt_only"] || known["automation.default_only"] {
		t.Fatalf("doctor automation scope: %v", known)
	}

	tests, err := discoverTestFiles(nil, "")
	if err != nil {
		t.Fatalf("discoverTestFiles: %v", err)
	}
	joinedTests := strings.Join(tests, "\n")
	if !strings.Contains(joinedTests, "alt_only_test.yaml") {
		t.Fatalf("selected root tests missing: %#v", tests)
	}
	if strings.Contains(joinedTests, "default_only_test.yaml") {
		t.Fatalf("default root tests leaked into selected root: %#v", tests)
	}

	autos, err := discoverConfigAutomations(nil)
	if err != nil {
		t.Fatalf("discoverConfigAutomations: %v", err)
	}
	joinedAutos := strings.Join(autos, "\n")
	if !strings.Contains(joinedAutos, "alt_only.yaml") {
		t.Fatalf("selected root automations missing: %#v", autos)
	}
	if strings.Contains(joinedAutos, "default_only.yaml") {
		t.Fatalf("default root automations leaked into selected root: %#v", autos)
	}

	ids, err := haverify.LoadAutomationIDs(filepath.Join(configPath, "automations"))
	if err != nil {
		t.Fatalf("LoadAutomationIDs: %v", err)
	}
	if _, ok := ids["alt_only"]; !ok {
		t.Fatalf("selected automation ID missing: %#v", ids)
	}
	if _, ok := ids["default_only"]; ok {
		t.Fatalf("default automation ID leaked: %#v", ids)
	}

	bp, err := haverify.LoadBlueprint(configPath, "example/alt_only.yaml")
	if err != nil {
		t.Fatalf("LoadBlueprint selected root: %v", err)
	}
	if bp.BlueprintMeta.Name != "alt_only" {
		t.Fatalf("blueprint name = %q, want alt_only", bp.BlueprintMeta.Name)
	}
	if _, err := haverify.LoadBlueprint(configPath, "example/default_only.yaml"); err == nil {
		t.Fatal("selected root loaded default blueprint")
	}

	root, err := resolvedConfigRoot()
	if err != nil {
		t.Fatalf("resolvedConfigRoot: %v", err)
	}
	if filepath.Base(root) != "alt-config" {
		t.Fatalf("resolvedConfigRoot = %q, want alt-config", root)
	}
}

func TestDiscoverResolvesConfigRootFromNestedWorkingDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "ha-config")
	writeConfigFixture(t, root, "nested")

	nested := filepath.Join(root, "automations", "config")
	t.Chdir(nested)

	oldConfig := configPath
	oldPattern := verifyPattern
	configPath = "ha-config"
	verifyPattern = ""
	t.Cleanup(func() {
		configPath = oldConfig
		verifyPattern = oldPattern
	})

	tests, err := discoverTestFiles(nil, "")
	if err != nil {
		t.Fatalf("discoverTestFiles nested: %v", err)
	}
	if !strings.Contains(strings.Join(tests, "\n"), "nested_test.yaml") {
		t.Fatalf("nested working directory missed selected tests: %#v", tests)
	}

	autos, err := discoverConfigAutomations(nil)
	if err != nil {
		t.Fatalf("discoverConfigAutomations nested: %v", err)
	}
	if !strings.Contains(strings.Join(autos, "\n"), "nested.yaml") {
		t.Fatalf("nested working directory missed selected automations: %#v", autos)
	}
}

func TestSiblingConfigurationRootsHaveSeparateRuntimeIdentities(t *testing.T) {
	root := t.TempDir()
	old := configPath
	defer func() { configPath = old }()
	configPath = filepath.Join(root, "first")
	first, err := currentDevEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(root, "second")
	second, err := currentDevEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectName == second.ProjectName || first.ComposeFile == second.ComposeFile {
		t.Fatal("alternate configurations share a runtime")
	}
}
