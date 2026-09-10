package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func TestRunCheckDevSuccessUsesTraceEnabledSuite(t *testing.T) {
	oldJSON := checkJSON
	oldResolve := resolveCheckProfileTarget
	oldValidate := runCheckValidationSuite
	oldExecuteTests := runCheckExecuteTestsFunc
	oldProdURL := checkProdURL
	oldProdToken := checkProdToken

	checkJSON = true
	checkProdURL = ""
	checkProdToken = ""
	resolveCheckProfileTarget = func(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		if defaultInstance != haconfig.InstanceDev {
			t.Fatalf("unexpected instance: %q", defaultInstance)
		}
		if mode != operator.ModeMutating {
			t.Fatalf("unexpected mode: %q", mode)
		}
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
	runCheckValidationSuite = func(string, bool) validationSummary {
		return validationSummary{ChecksRun: 5}
	}
	runCheckExecuteTestsFunc = func(config *haconfig.HAConfig, opts testExecutionOptions) (*testSummary, error) {
		if config.URL != "http://localhost:8123" {
			t.Fatalf("unexpected config URL: %q", config.URL)
		}
		if !opts.Trace {
			t.Fatal("dev profile should enable trace validation")
		}
		return &testSummary{
			FilesDiscovered: 2,
			FilesSelected:   2,
			Passed:          4,
		}, nil
	}

	defer func() {
		checkJSON = oldJSON
		resolveCheckProfileTarget = oldResolve
		runCheckValidationSuite = oldValidate
		runCheckExecuteTestsFunc = oldExecuteTests
		checkProdURL = oldProdURL
		checkProdToken = oldProdToken
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runCheck(cmd, []string{"dev"}); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}

	var result operator.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
	if result.Profile != "dev" {
		t.Fatalf("profile = %q, want %q", result.Profile, "dev")
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
}

func TestRunCheckProdSuccessComposesVerifyAndAudit(t *testing.T) {
	oldJSON := checkJSON
	oldResolve := resolveCheckProfileTarget
	oldValidate := runCheckValidationSuite
	oldExecuteVerify := runCheckExecuteVerifyFunc
	oldExecuteAudit := runCheckExecuteAuditFunc
	oldDevURL := checkDevURL
	oldDevToken := checkDevToken

	checkJSON = true
	checkDevURL = ""
	checkDevToken = ""
	resolveCheckProfileTarget = func(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		if defaultInstance != haconfig.InstanceProd {
			t.Fatalf("unexpected instance: %q", defaultInstance)
		}
		if mode != operator.ModeReadOnly {
			t.Fatalf("unexpected mode: %q", mode)
		}
		return &operator.ResolvedTarget{
			Instance: haconfig.InstanceProd,
			Config: &haconfig.HAConfig{
				URL:    "https://prod.example",
				Token:  "token",
				Source: "test",
			},
			Target: &operator.Target{
				Name:     "production",
				Instance: string(haconfig.InstanceProd),
				URL:      "https://prod.example",
				Source:   "test",
				Risk:     operator.RiskCaution,
				Mode:     operator.ModeReadOnly,
			},
		}, nil
	}
	runCheckValidationSuite = func(string, bool) validationSummary {
		return validationSummary{ChecksRun: 5}
	}
	runCheckExecuteVerifyFunc = func(config *haconfig.HAConfig, opts verifyExecutionOptions) (*verifySummary, error) {
		if config.URL != "https://prod.example" {
			t.Fatalf("unexpected config URL: %q", config.URL)
		}
		if opts.DryRun {
			t.Fatal("prod profile should run live verification")
		}
		return &verifySummary{
			FilesDiscovered: 1,
			DevicesTotal:    1,
			DevicesChecked:  1,
			Passed:          4,
		}, nil
	}
	runCheckExecuteAuditFunc = func(config *haconfig.HAConfig, opts auditExecutionOptions, _ io.Writer) (*auditSummary, error) {
		if config.URL != "https://prod.example" {
			t.Fatalf("unexpected config URL: %q", config.URL)
		}
		if opts.Window != 24*time.Hour {
			t.Fatalf("window = %s, want %s", opts.Window, 24*time.Hour)
		}
		if opts.Tolerance != 30*time.Second {
			t.Fatalf("tolerance = %s, want %s", opts.Tolerance, 30*time.Second)
		}
		return &auditSummary{
			SpecsDiscovered: 2,
			SpecsLoaded:     2,
			TriggerEvents:   3,
			Passed:          3,
		}, nil
	}

	defer func() {
		checkJSON = oldJSON
		resolveCheckProfileTarget = oldResolve
		runCheckValidationSuite = oldValidate
		runCheckExecuteVerifyFunc = oldExecuteVerify
		runCheckExecuteAuditFunc = oldExecuteAudit
		checkDevURL = oldDevURL
		checkDevToken = oldDevToken
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runCheck(cmd, []string{"prod"}); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}

	var result operator.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
	if result.Profile != "prod" {
		t.Fatalf("profile = %q, want %q", result.Profile, "prod")
	}
	if len(result.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(result.Steps))
	}
}
