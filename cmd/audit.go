package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"
)

var (
	auditProdURL    string
	auditProdToken  string
	auditHAURL      string
	auditHAToken    string
	auditVerbose    bool
	auditTracesOnly bool
	auditWindow     time.Duration
	auditTolerance  time.Duration
	auditPattern    string
	auditJSON       bool
)

var (
	resolveAuditTarget = operator.ResolveTarget
	runAuditExecution  = executeAudit
)

var auditCmd = &cobra.Command{
	Use:   "audit [spec-file...]",
	Short: "Audit production automations using history correlation",
	Long: `Retrospectively validates production automations by correlating trigger
events with expected outcomes using the HA History API. Read-only —
examines what already happened without firing automations or modifying state.

Connection Resolution (in order of precedence):
  1. Command-line flags (--prod-url, --prod-token)
  2. Environment variables (HASS_PROD_URL, HASS_PROD_TOKEN)
  3. Legacy env vars (HASS_URL, HASS_TOKEN)

Examples:
  # Run all audits (default: last 24h, 30s tolerance)
  dm audit

  # Custom window and tolerance
  dm audit --window 6h --tolerance 10s

  # Specific audit file
  dm audit ha-config/audits/presence/office_lights.yaml

  # Verbose output (per-trigger-event detail)
  dm audit -v`,
	RunE: runAudit,
}

func init() {
	auditCmd.Flags().StringVar(&auditProdURL, "prod-url", "", "Production Home Assistant URL")
	auditCmd.Flags().StringVar(&auditProdToken, "prod-token", "", "Production Home Assistant token")
	auditCmd.Flags().StringVar(&auditHAURL, "ha-url", "", "Home Assistant URL (legacy, use --prod-url)")
	auditCmd.Flags().StringVar(&auditHAToken, "ha-token", "", "Home Assistant token (legacy, use --prod-token)")
	auditCmd.Flags().BoolVarP(&auditVerbose, "verbose", "v", false, "Verbose output (per-trigger-event detail)")
	auditCmd.Flags().BoolVar(&auditTracesOnly, "traces-only", false, "Print raw automation traces without correlation")
	auditCmd.Flags().DurationVar(&auditWindow, "window", 24*time.Hour, "Lookback period from now")
	auditCmd.Flags().DurationVar(&auditTolerance, "tolerance", 30*time.Second, "Max delay between trigger and expected outcome")
	auditCmd.Flags().StringVar(&auditPattern, "pattern", "", "Glob pattern for audit spec files")
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "Emit a machine-readable JSON summary")

	rootCmd.AddCommand(auditCmd)
}

func runAudit(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("audit", auditJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     auditProdURL,
		ProdToken:   auditProdToken,
		LegacyURL:   auditHAURL,
		LegacyToken: auditHAToken,
	}
	resolved, err := resolveAuditTarget(haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()

	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve Home Assistant target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "audit could not start")
	}

	summary, auditErr := runAuditExecution(resolved.Config, auditExecutionOptions{
		Args:         args,
		Pattern:      auditPattern,
		Verbose:      auditVerbose,
		TracesOnly:   auditTracesOnly,
		Window:       auditWindow,
		Tolerance:    auditTolerance,
		PrintResults: !auditJSON,
	}, cmd.ErrOrStderr())
	if auditErr != nil {
		rt.AddStep(operator.Step{
			ID:      "run-audit",
			Title:   "Audit production automations",
			Status:  operator.StatusFailure,
			Summary: auditErr.Error(),
		})
		rt.AddHint("Check the production Home Assistant connection and audit spec selection, then rerun `dm audit`.")
		return rt.Complete(operator.StatusFailure, "audit failed before correlation")
	}

	step := summary.Step("run-audit", "Audit production automations")
	rt.AddStep(step)
	if step.Status == operator.StatusWarning {
		rt.AddHint("No audit activity was found in the current window. Use `dm audit --window 48h` or a narrower spec selection for a deeper production pass.")
	}
	if step.Status == operator.StatusPartial {
		rt.AddHint("Audit correlation completed, but some trace-backed details were unavailable. Re-run `dm audit -v` for more detail.")
	}
	return rt.Complete(step.Status, auditSummaryText(summary))
}

func discoverAuditSpecs(args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	fsys := os.DirFS(".")

	if auditPattern != "" {
		matches, err := doublestar.Glob(fsys, auditPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %w", err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no audit spec files matched pattern %q", auditPattern)
		}
		return matches, nil
	}

	matches, err := doublestar.Glob(fsys, "ha-config/audits/**/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to discover audit specs: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no audit spec files found\n\nTry:\n  dm audit <spec-file>\n  dm audit --pattern \"ha-config/audits/**/*.yaml\"")
	}
	return matches, nil
}
