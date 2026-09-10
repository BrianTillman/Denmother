package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func selectAssistanceProject(t *testing.T, root string) {
	t.Helper()
	oldPath, oldExplicit := configPath, configExplicit
	configPath, configExplicit = filepath.Join(root, "custom-ha"), true
	t.Cleanup(func() { configPath, configExplicit = oldPath, oldExplicit })
	writeTestFile(t, filepath.Join(root, ".denmother.yaml"), "version: 1\nconfig_dir: custom-ha\nreferences_dir: inventory\n")
	writeTestFile(t, filepath.Join(root, "custom-ha/configuration.yaml"), "default_config:\n")
}

func TestProjectDocsAndPlansStayWithSelectedProject(t *testing.T) {
	root, caller := t.TempDir(), t.TempDir()
	selectAssistanceProject(t, root)
	writeTestFile(t, filepath.Join(root, "custom-ha/automations/lights/demo.yaml"), "id: demo\nalias: Demo\ntrigger: []\naction: []\n")
	writeTestFile(t, filepath.Join(root, "inventory/entity-list.txt"), "light.demo\n")
	t.Chdir(caller)
	summary, err := buildGeneratedDocs(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedDocs(summary.Docs); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(root, "docs/generated/automation-index.md")) || fileExists(filepath.Join(caller, "docs")) {
		t.Fatal("generated docs must only be written in the selected project")
	}
	content, err := os.ReadFile(filepath.Join(root, "docs/generated/automation-index.md"))
	if err != nil || !strings.Contains(string(content), "custom-ha/automations/lights/demo.yaml") || strings.Contains(string(content), root) {
		t.Fatalf("generated references must use stable project paths: %s (%v)", content, err)
	}
	t.Chdir(root)
	checked, err := buildGeneratedDocs(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range checked.Docs {
		if doc.Changed {
			t.Errorf("%s became stale after changing caller directory", doc.Path)
		}
	}
	t.Chdir(caller)
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runPlanInit(cmd, []string{"portable-plan"}); err != nil {
		t.Fatal(err)
	}
	status := planStatusSummary()
	if len(status.Active) != 1 || status.Active[0].Path != filepath.Join(root, "docs/exec-plans/active/portable-plan.md") {
		t.Fatalf("unexpected plan location: %+v", status)
	}
	if err := runPlanComplete(cmd, []string{"portable-plan"}); err != nil {
		t.Fatal(err)
	}
	status = planStatusSummary()
	if len(status.Active) != 0 || len(status.Completed) != 1 || fileExists(filepath.Join(caller, "docs")) {
		t.Fatalf("unexpected completed plans: %+v", status)
	}
}

func TestChangedFilesUseSelectedUnbornRepository(t *testing.T) {
	root, caller := t.TempDir(), t.TempDir()
	selectAssistanceProject(t, root)
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "custom-ha/configuration.yaml"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	writeTestFile(t, filepath.Join(root, "custom-ha/automations/new automation.yaml"), "id: new\n")
	t.Chdir(caller)
	changed, err := changedFilesForReview()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"custom-ha/configuration.yaml", "custom-ha/automations/new automation.yaml"} {
		found := false
		for _, got := range changed {
			found = found || got == filepath.Join(root, want)
		}
		if !found {
			t.Errorf("missing %s from selected repository changes: %v", want, changed)
		}
	}
	if !anyHAConfigChanged(changed) {
		t.Fatal("absolute selected configuration paths were not recognized")
	}
}

func TestAlternateRootBlueprintPlansAndReview(t *testing.T) {
	root := t.TempDir()
	selectAssistanceProject(t, root)
	t.Chdir(root)
	writeTestFile(t, "custom-ha/blueprints/automation/Example/demo.yaml", "blueprint:\n  name: Demo\n  domain: automation\n")
	writeTestFile(t, "custom-ha/automations/room/demo.yaml", "id: demo\nuse_blueprint:\n  path: Example/demo.yaml\n")
	writeTestFile(t, "custom-ha/tests/room/demo_test.yaml", "tests: []\n")
	plan, err := buildTestPlanSummary([]string{"custom-ha/blueprints/automation/Example/demo.yaml"})
	if err != nil || len(plan.TestFiles) != 1 || plan.TestFiles[0].Path != "custom-ha/tests/room/demo_test.yaml" {
		t.Fatalf("blueprint consumers not scheduled: %+v (%v)", plan, err)
	}
	if len(reviewBlueprintChanges([]string{"custom-ha/blueprints/automation/Example/demo.yaml"})) != 1 {
		t.Fatal("alternate-root blueprint change was missed")
	}
	for _, path := range []string{"custom-ha/dashboards/demo.yaml", "custom-ha/homekit/bridge.yaml", "custom-ha/zigbee2mqtt/configuration.yaml"} {
		if isHAConfigEntityImpactFile(path) {
			t.Errorf("%s should not schedule unrelated automation tests", path)
		}
	}
	if len(reviewProductionRisk([]string{"custom-ha/configuration.yaml"})) != 1 {
		t.Fatal("selected root configuration risk was missed")
	}
}

func TestGoPlanningSupportsStandaloneAndNestedModules(t *testing.T) {
	root := t.TempDir()
	selectAssistanceProject(t, root)
	t.Chdir(root)
	writeTestFile(t, "go.mod", "module example.test/project\ngo 1.24\n")
	writeTestFile(t, "cmd/example.go", "package cmd\n")
	writeTestFile(t, "tools/child/go.mod", "module example.test/child\ngo 1.24\n")
	plan, err := buildTestPlanSummary([]string{"cmd/example.go", "tools/child/child.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.GoCommands) != 2 || plan.GoCommands[0] != "go test ./..." || plan.GoCommands[1] != "cd tools/child && go test ./..." {
		t.Fatalf("unexpected module commands: %v", plan.GoCommands)
	}
	if !hasAgentIssue(reviewDocsDrift([]string{"cmd/example.go"}), "command-docs-drift") {
		t.Fatal("standalone command docs drift was missed")
	}
	if len(reviewDocsDrift([]string{"cmd/example.go", "README.md"})) != 0 {
		t.Fatal("a documented command change should not report docs drift")
	}
}
