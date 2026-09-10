package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func TestRunTestProductionGuardrailReturnsStructuredJSON(t *testing.T) {
	oldJSON := testJSON
	oldProdURL := testProdURL
	oldProdToken := testProdToken
	oldAllowProd := testAllowProd

	testJSON = true
	testProdURL = "https://example.com"
	testProdToken = "demo-token"
	testAllowProd = false

	defer func() {
		testJSON = oldJSON
		testProdURL = oldProdURL
		testProdToken = oldProdToken
		testAllowProd = oldAllowProd
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runTest(cmd, nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	var exitErr *operator.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusFailure {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusFailure)
	}
	if result.Target == nil || result.Target.Risk != operator.RiskHigh {
		t.Fatalf("unexpected target: %+v", result.Target)
	}
}

func TestRunTestSuccessUsesInjectedExecutor(t *testing.T) {
	oldJSON := testJSON
	oldResolve := resolveTestTarget
	oldExecute := runTestExecuteTests

	testJSON = true
	resolveTestTarget = func(_ context.Context, defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		return &operator.ResolvedTarget{
			Instance: haconfig.InstanceDev,
			Config: &haconfig.HAConfig{
				URL:     "http://localhost:8123",
				Token:   "token",
				IsLocal: true,
				Source:  "test",
			},
			Target: &operator.Target{
				Name:     "local",
				Instance: string(haconfig.InstanceDev),
				URL:      "http://localhost:8123",
				Source:   "test",
				Risk:     operator.RiskSafe,
				IsLocal:  true,
				Mode:     operator.ModeMutating,
			},
		}, nil
	}
	runTestExecuteTests = func(config *haconfig.HAConfig, opts testExecutionOptions) (*testSummary, error) {
		if config == nil || config.URL != "http://localhost:8123" {
			t.Fatalf("unexpected config: %+v", config)
		}
		return &testSummary{
			FilesDiscovered: 1,
			FilesSelected:   1,
			Passed:          3,
		}, nil
	}

	defer func() {
		testJSON = oldJSON
		resolveTestTarget = oldResolve
		runTestExecuteTests = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runTest(cmd, nil); err != nil {
		t.Fatalf("runTest returned error: %v", err)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(result.Steps))
	}
}

func TestRunTestChangedBaseSelectsCommittedDiffTests(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	runGitCommand(t, "init")
	runGitCommand(t, "config", "user.email", "test@example.com")
	runGitCommand(t, "config", "user.name", "Test User")
	writeTestFile(t, "ha-config/automations/presence/office_lights.yaml", `
- alias: Office Lights
`)
	writeTestFile(t, "ha-config/tests/presence/office_lights_test.yaml", `
version: 1
name: Office Lights
config:
  timeout: 1s
  cleanup: true
tests:
  - name: smoke
    trigger:
      - service: automation.trigger
        target:
          entity_id: automation.office_lights
    assertions:
      - entity_id: light.office
        state: "on"
`)
	runGitCommand(t, "add", ".")
	runGitCommand(t, "commit", "-m", "base")
	runGitCommand(t, "branch", "base")
	writeTestFile(t, "ha-config/automations/presence/office_lights.yaml", `
- alias: Office Lights Updated
`)
	runGitCommand(t, "add", ".")
	runGitCommand(t, "commit", "-m", "change automation")

	oldJSON := testJSON
	oldChanged := testChanged
	oldChangedBase := testChangedBase
	oldTrace := testTrace
	oldPattern := testPattern
	oldCase := testCase
	oldTags := testTags
	oldProdURL := testProdURL
	oldProdToken := testProdToken
	oldDevURL := testDevURL
	oldDevToken := testDevToken
	oldHAURL := testHAURL
	oldHAToken := testHAToken
	oldAllowProd := testAllowProd
	oldResolve := resolveTestTarget
	oldExecute := runTestExecuteTests

	testJSON = true
	testChanged = true
	testChangedBase = "base"
	testTrace = true
	testPattern = ""
	testCase = ""
	testTags = nil
	testProdURL = ""
	testProdToken = ""
	testDevURL = ""
	testDevToken = ""
	testHAURL = ""
	testHAToken = ""
	testAllowProd = false

	resolveTestTarget = func(_ context.Context, defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		return &operator.ResolvedTarget{
			Instance: haconfig.InstanceDev,
			Config: &haconfig.HAConfig{
				URL:     "http://localhost:8123",
				Token:   "token",
				IsLocal: true,
				Source:  "test",
			},
			Target: &operator.Target{
				Name:     "local",
				Instance: string(haconfig.InstanceDev),
				URL:      "http://localhost:8123",
				Source:   "test",
				Risk:     operator.RiskSafe,
				IsLocal:  true,
				Mode:     operator.ModeMutating,
			},
		}, nil
	}
	var gotOpts testExecutionOptions
	runTestExecuteTests = func(config *haconfig.HAConfig, opts testExecutionOptions) (*testSummary, error) {
		gotOpts = opts
		return &testSummary{
			FilesDiscovered: 1,
			FilesSelected:   len(opts.Args),
			Passed:          1,
		}, nil
	}

	defer func() {
		testJSON = oldJSON
		testChanged = oldChanged
		testChangedBase = oldChangedBase
		testTrace = oldTrace
		testPattern = oldPattern
		testCase = oldCase
		testTags = oldTags
		testProdURL = oldProdURL
		testProdToken = oldProdToken
		testDevURL = oldDevURL
		testDevToken = oldDevToken
		testHAURL = oldHAURL
		testHAToken = oldHAToken
		testAllowProd = oldAllowProd
		resolveTestTarget = oldResolve
		runTestExecuteTests = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runTest(cmd, nil); err != nil {
		t.Fatalf("runTest returned error: %v", err)
	}

	wantArgs := []string{"ha-config/tests/presence/office_lights_test.yaml"}
	if !reflect.DeepEqual(gotOpts.Args, wantArgs) {
		t.Fatalf("opts.Args = %#v, want %#v", gotOpts.Args, wantArgs)
	}
	if !gotOpts.Trace {
		t.Fatal("expected trace option to be forwarded")
	}
}

func runGitCommand(t *testing.T, args ...string) {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
