package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func TestRunCheckUnknownProfileReturnsStructuredJSON(t *testing.T) {
	oldJSON := checkJSON
	checkJSON = true
	defer func() { checkJSON = oldJSON }()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runCheck(cmd, []string{"weird"})
	if err == nil {
		t.Fatal("expected non-nil error for unknown profile")
	}

	var exitErr *operator.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != operator.ExitFailure {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), operator.ExitFailure)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusFailure {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusFailure)
	}
	if result.Summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestRunCheckLocalUnavailableReturnsPartialJSON(t *testing.T) {
	oldJSON := checkJSON
	oldResolve := resolveCheckLocalConfig
	oldValidate := runCheckValidationSuite
	oldExecute := runCheckExecuteTestsFunc

	checkJSON = true
	resolveCheckLocalConfig = func() (*haconfig.HAConfig, error) {
		return nil, errors.New("local HA unavailable")
	}
	runCheckValidationSuite = func(string, bool) validationSummary {
		return validationSummary{ChecksRun: 5}
	}
	runCheckExecuteTestsFunc = func(*haconfig.HAConfig, testExecutionOptions) (*testSummary, error) {
		t.Fatal("executeTests should not run when local config resolution fails")
		return nil, nil
	}

	defer func() {
		checkJSON = oldJSON
		resolveCheckLocalConfig = oldResolve
		runCheckValidationSuite = oldValidate
		runCheckExecuteTestsFunc = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runCheck(cmd, nil)
	if err == nil {
		t.Fatal("expected non-nil error for partial result")
	}

	var exitErr *operator.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != operator.ExitPartial {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), operator.ExitPartial)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Profile != "local" {
		t.Fatalf("profile = %q, want %q", result.Profile, "local")
	}
	if result.Status != operator.StatusPartial {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusPartial)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
	if result.Steps[1].ID != "test-fast" {
		t.Fatalf("second step id = %q, want %q", result.Steps[1].ID, "test-fast")
	}
}

func TestRunCheckLocalSuccessRunsFastTests(t *testing.T) {
	oldJSON := checkJSON
	oldResolve := resolveCheckLocalConfig
	oldValidate := runCheckValidationSuite
	oldExecute := runCheckExecuteTestsFunc

	checkJSON = true
	resolveCheckLocalConfig = func() (*haconfig.HAConfig, error) {
		return &haconfig.HAConfig{
			URL:     "http://localhost:8123",
			Token:   "token",
			IsLocal: true,
			Source:  "test",
		}, nil
	}
	runCheckValidationSuite = func(string, bool) validationSummary {
		return validationSummary{ChecksRun: 5}
	}
	runCheckExecuteTestsFunc = func(config *haconfig.HAConfig, opts testExecutionOptions) (*testSummary, error) {
		if config == nil || config.URL != "http://localhost:8123" {
			t.Fatalf("unexpected config: %+v", config)
		}
		if len(opts.Tags) != 1 || opts.Tags[0] != "fast" {
			t.Fatalf("unexpected tags: %+v", opts.Tags)
		}
		if opts.PrintResults {
			t.Fatal("json mode should suppress direct test result printing")
		}
		return &testSummary{
			FilesDiscovered: 1,
			FilesSelected:   1,
			Passed:          2,
		}, nil
	}

	defer func() {
		checkJSON = oldJSON
		resolveCheckLocalConfig = oldResolve
		runCheckValidationSuite = oldValidate
		runCheckExecuteTestsFunc = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runCheck(cmd, nil); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
	if result.Steps[1].Status != operator.StatusSuccess {
		t.Fatalf("test step status = %q, want %q", result.Steps[1].Status, operator.StatusSuccess)
	}
}
