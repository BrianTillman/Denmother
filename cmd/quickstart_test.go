package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/hatest"
)

func TestOfflineQuickstartOutsideHomeRepository(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dm")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build quickstart CLI: %v\n%s", err, output)
	}
	project := filepath.Join(t.TempDir(), "example")
	if err := os.CopyFS(project, os.DirFS("../examples/quickstart")); err != nil {
		t.Fatal(err)
	}
	// Parse the shipped test with the runner schema. The HA integration tests
	// check its runtime behavior.
	if _, err := hatest.LoadTestSpec(filepath.Join(project, "ha-config/tests/presence/motion_lamp_test.yaml")); err != nil {
		t.Fatalf("example test specification: %v", err)
	}
	run := func() ([]byte, error) {
		cmd := exec.Command(bin, "--config", "ha-config", "--no-schema")
		cmd.Dir = project
		return cmd.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("standalone offline quickstart: %v\n%s", err, output)
	}
	automation := filepath.Join(project, "ha-config/automations/presence/motion_lamp.yaml")
	contents, err := os.ReadFile(automation)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.ReplaceAll(string(contents), "input_boolean.study_lamp", "input_boolean.study_missing_lamp"))
	if err := os.WriteFile(automation, contents, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := run()
	if err == nil || !strings.Contains(string(output), "input_boolean.study_missing_lamp") || !strings.Contains(string(output), "Line ") {
		t.Fatalf("invalid example must fail with entity and source location: err=%v\n%s", err, output)
	}
}
