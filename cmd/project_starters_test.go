package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BrianTillman/Denmother/internal/hatest"
	"github.com/BrianTillman/Denmother/internal/project"
)

func TestSelectableStartersOutsideCheckout(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dm")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	run := func(t *testing.T, args ...string) {
		t.Helper()
		command := exec.Command(bin, args...)
		command.Dir = t.TempDir()
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	for _, starter := range []string{"automations", "dashboard"} {
		t.Run(starter, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project with spaces")
			run(t, "init", "--directory", root, "--config", "home", "--example", starter, "--json")
			config := filepath.Join(root, "home")
			run(t, "--config", config, "--no-schema", "--json")
			if starter == "dashboard" {
				settings, err := project.ForConfig(config)
				if err != nil {
					t.Fatal(err)
				}
				if settings.DevFixtures != filepath.Join(root, "fixtures.json") || settings.DevScenarios != filepath.Join(root, "scenarios.json") {
					t.Fatalf("lost fixture settings: %+v", settings)
				}
				for _, path := range []string{"www/denmother-status-card.js", "dashboards/demo.yaml"} {
					if _, err := os.Stat(filepath.Join(config, path)); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for _, kind := range []string{"timer", "event", "blueprint"} {
					if _, err := hatest.LoadTestSpec(filepath.Join(config, "tests", kind+"_lamp_test.yaml")); err != nil {
						t.Fatal(err)
					}
				}
			}
			configFile := filepath.Join(config, "configuration.yaml")
			before, err := os.ReadFile(configFile)
			if err != nil {
				t.Fatal(err)
			}
			run(t, "init", "--directory", root, "--config", "home", "--example", starter, "--json")
			after, _ := os.ReadFile(configFile)
			if string(before) != string(after) {
				t.Fatal("reinitialization changed source")
			}
			// An existing home receives a distinct example with correctly scoped settings.
			other := t.TempDir()
			if err := os.Mkdir(filepath.Join(other, "ha-config"), 0755); err != nil {
				t.Fatal(err)
			}
			original := []byte("homeassistant:\n  name: Existing home\n")
			if err := os.WriteFile(filepath.Join(other, "ha-config/configuration.yaml"), original, 0600); err != nil {
				t.Fatal(err)
			}
			run(t, "init", "--directory", other, "--example", starter, "--json")
			home, _ := os.ReadFile(filepath.Join(other, "ha-config/configuration.yaml"))
			if string(home) != string(original) {
				t.Fatal("overwrote home")
			}
			sample := filepath.Join(other, "examples", "denmother-"+starter, "ha-config")
			run(t, "--config", sample, "--no-schema", "--json")
			if starter == "dashboard" {
				settings, err := project.ForConfig(sample)
				if err != nil {
					t.Fatal(err)
				}
				if settings.DevFixtures != filepath.Join(filepath.Dir(sample), "fixtures.json") {
					t.Fatal("nested fixture settings lost")
				}
			}
		})
	}
	invalid := filepath.Join(t.TempDir(), "untouched")
	cmd := exec.Command(bin, "init", "--directory", invalid, "--example", "missing", "--json")
	if err := cmd.Run(); err == nil {
		t.Fatal("unknown starter accepted")
	}
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatal("invalid selection wrote files")
	}
}
