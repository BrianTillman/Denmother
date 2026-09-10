package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/haverify"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func TestVerifySummaryReportsStaleRestoredAutomations(t *testing.T) {
	summary := &verifySummary{
		DevicesChecked:           1,
		Passed:                   1,
		StaleRestoredAutomations: []haverify.StaleRestoredAutomation{{EntityID: "automation.retired", UniqueID: "retired_id"}},
	}

	if got := summary.Status(); got != operator.StatusFailure {
		t.Fatalf("Status() = %q, want failure", got)
	}
	step := summary.Step("run-verification", "Verify live state")
	if len(step.Hints) == 0 || !strings.Contains(step.Hints[0], "never deletes") {
		t.Fatalf("expected non-destructive remediation hint, got %#v", step.Hints)
	}
	if !strings.Contains(step.Summary, "1 stale restored automation") {
		t.Fatalf("expected stale automation count in summary, got %q", step.Summary)
	}
	if _, ok := step.Details["stale_restored_automations"]; !ok {
		t.Fatalf("expected REST-derived stale automation details, got %#v", step.Details)
	}
}

func TestDetectStaleRestoredAutomationsUsesFetchedRESTStates(t *testing.T) {
	oldConfigPath := configPath
	configPath = t.TempDir()
	defer func() { configPath = oldConfigPath }()

	automationsPath := filepath.Join(configPath, "automations")
	if err := os.MkdirAll(automationsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(automationsPath, "active.yaml"), []byte("- id: active_id\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	states := []hasync.EntityState{
		{EntityID: "automation.retired", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "retired_id"}},
		{EntityID: "automation.active", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "active_id"}},
	}
	got, err := detectStaleRestoredAutomations(states)
	if err != nil {
		t.Fatalf("detectStaleRestoredAutomations() error = %v", err)
	}
	want := []haverify.StaleRestoredAutomation{{EntityID: "automation.retired", UniqueID: "retired_id"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectStaleRestoredAutomations() = %#v, want %#v", got, want)
	}
}

func TestRunVerifyDryRunReturnsStructuredJSON(t *testing.T) {
	oldJSON := verifyJSON
	oldDryRun := verifyDryRun
	oldExecute := runVerifyExecution

	verifyJSON = true
	verifyDryRun = true
	runVerifyExecution = func(config *haconfig.HAConfig, opts verifyExecutionOptions) (*verifySummary, error) {
		if !opts.DryRun {
			t.Fatal("expected dry-run execution")
		}
		if config != nil {
			t.Fatalf("expected nil config for dry run, got %+v", config)
		}
		return &verifySummary{
			FilesDiscovered: 2,
			DevicesTotal:    2,
			DevicesChecked:  1,
			DevicesSkipped:  1,
			DryRun:          true,
		}, nil
	}

	defer func() {
		verifyJSON = oldJSON
		verifyDryRun = oldDryRun
		runVerifyExecution = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runVerify(cmd, nil); err != nil {
		t.Fatalf("runVerify returned error: %v", err)
	}

	var result operator.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
}

func TestDiscoverConfigAutomationsIncludesRoomSubdirectories(t *testing.T) {
	oldPattern := verifyPattern
	verifyPattern = ""
	defer func() { verifyPattern = oldPattern }()

	root := t.TempDir()
	t.Chdir(root)

	automationPath := filepath.Join("ha-config", "automations", "config", "kitchen", "fan.yaml")
	if err := os.MkdirAll(filepath.Dir(automationPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(automationPath, []byte("- id: config_kitchen_fan\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	files, err := discoverConfigAutomations(nil)
	if err != nil {
		t.Fatalf("discoverConfigAutomations returned error: %v", err)
	}
	if len(files) != 1 || files[0] != automationPath {
		t.Fatalf("files = %#v, want %#v", files, []string{automationPath})
	}
}
