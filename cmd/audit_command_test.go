package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func TestRunAuditSuccessReturnsStructuredJSON(t *testing.T) {
	oldJSON := auditJSON
	oldResolve := resolveAuditTarget
	oldExecute := runAuditExecution

	auditJSON = true
	resolveAuditTarget = func(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
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
	runAuditExecution = func(config *haconfig.HAConfig, opts auditExecutionOptions, _ io.Writer) (*auditSummary, error) {
		return &auditSummary{
			SpecsDiscovered: 2,
			SpecsLoaded:     2,
			TriggerEvents:   5,
			Passed:          4,
			Idempotent:      1,
		}, nil
	}

	defer func() {
		auditJSON = oldJSON
		resolveAuditTarget = oldResolve
		runAuditExecution = oldExecute
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runAudit(cmd, nil); err != nil {
		t.Fatalf("runAudit returned error: %v", err)
	}

	var result operator.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON output: %v", err)
	}
	if result.Status != operator.StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusSuccess)
	}
}
