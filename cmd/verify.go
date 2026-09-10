package cmd

import (
	"fmt"
	"os"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"
)

var (
	verifyProdURL   string
	verifyProdToken string
	verifyHAURL     string
	verifyHAToken   string
	verifyVerbose   bool
	verifyDryRun    bool
	verifyPattern   string
	verifyJSON      bool
)

var (
	resolveVerifyTarget = operator.ResolveTarget
	runVerifyExecution  = executeVerify
)

var verifyCmd = &cobra.Command{
	Use:   "verify [config-file...]",
	Short: "Verify device parameters against live HA entity states",
	Long: `Compares expected device parameter values (derived from blueprint + automation
YAML) against live entity states in Home Assistant. It also reports restored,
unavailable automation states whose attributes.id values no longer exist in
YAML. Detection reuses the REST state response, is read-only, and never deletes
registry entries. Z2M Inovelli checks derive HA entities from MQTT parameters;
Matter Inovelli checks use explicit entity inputs from the automation.

Connection Resolution (in order of precedence):
  1. Command-line flags (--prod-url, --prod-token)
  2. Environment variables (HASS_PROD_URL, HASS_PROD_TOKEN)
  3. Legacy env vars (HASS_URL, HASS_TOKEN)

Examples:
  # Verify all config automations
  dm verify

  # Dry run: show expected values without querying HA
  dm verify --dry-run

  # Single device
  dm verify ha-config/automations/config/office/office_baywindow_dimmer.yaml

  # Verbose: show all parameters, not just mismatches
  dm verify -v

  # Pattern matching
  dm verify --pattern "ha-config/automations/config/office/**/*.yaml"`,
	RunE: runVerify,
}

func init() {
	verifyCmd.Flags().StringVar(&verifyProdURL, "prod-url", "", "Production Home Assistant URL")
	verifyCmd.Flags().StringVar(&verifyProdToken, "prod-token", "", "Production Home Assistant token")
	verifyCmd.Flags().StringVar(&verifyHAURL, "ha-url", "", "Home Assistant URL (legacy, use --prod-url)")
	verifyCmd.Flags().StringVar(&verifyHAToken, "ha-token", "", "Home Assistant token (legacy, use --prod-token)")
	verifyCmd.Flags().BoolVarP(&verifyVerbose, "verbose", "v", false, "Show all parameters, not just mismatches")
	verifyCmd.Flags().BoolVar(&verifyDryRun, "dry-run", false, "Show expected values without querying HA")
	verifyCmd.Flags().StringVar(&verifyPattern, "pattern", "", "Glob pattern for config automation files")
	verifyCmd.Flags().BoolVar(&verifyJSON, "json", false, "Emit a machine-readable JSON summary")

	rootCmd.AddCommand(verifyCmd)
}

func runVerify(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("verify", verifyJSON, cmd.OutOrStdout())

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     verifyProdURL,
		ProdToken:   verifyProdToken,
		LegacyURL:   verifyHAURL,
		LegacyToken: verifyHAToken,
	}
	if !verifyDryRun {
		resolved, err := resolveVerifyTarget(haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
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
			return rt.Complete(operator.StatusFailure, "verification could not start")
		}

		summary, verifyErr := runVerifyExecution(resolved.Config, verifyExecutionOptions{
			Args:         args,
			Pattern:      verifyPattern,
			Verbose:      verifyVerbose,
			DryRun:       false,
			PrintResults: !verifyJSON,
		})
		if verifyErr != nil {
			rt.AddStep(operator.Step{
				ID:      "run-verification",
				Title:   "Verify live device parameters",
				Status:  operator.StatusFailure,
				Summary: verifyErr.Error(),
			})
			rt.AddHint("Check the production Home Assistant connection or narrow the verification pattern, then rerun `dm verify`.")
			return rt.Complete(operator.StatusFailure, "verification failed before comparison")
		}

		step := summary.Step("run-verification", "Verify live device parameters")
		rt.AddStep(step)
		if step.Status == operator.StatusWarning {
			rt.AddHint("No verifiable Inovelli config automations were found in the selected config set.")
		}
		return rt.Complete(step.Status, verifySummaryText(summary))
	}

	summary, verifyErr := runVerifyExecution(nil, verifyExecutionOptions{
		Args:         args,
		Pattern:      verifyPattern,
		Verbose:      verifyVerbose,
		DryRun:       true,
		PrintResults: !verifyJSON,
	})
	if verifyErr != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-verification",
			Title:   "Resolve expected device parameters",
			Status:  operator.StatusFailure,
			Summary: verifyErr.Error(),
		})
		return rt.Complete(operator.StatusFailure, "verification dry run failed")
	}

	step := summary.Step("resolve-verification", "Resolve expected device parameters")
	rt.AddStep(step)
	if step.Status == operator.StatusWarning {
		rt.AddHint("No verifiable Inovelli config automations were found in the selected config set.")
	}
	return rt.Complete(step.Status, verifySummaryText(summary))
}

func discoverConfigAutomations(args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	if verifyPattern != "" {
		matches, err := doublestar.Glob(os.DirFS("."), verifyPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %w", err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no config automation files matched pattern %q", verifyPattern)
		}
		return matches, nil
	}

	matches, err := globUnderConfig("automations/config/**/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to discover config automations: %w", err)
	}
	if len(matches) == 0 {
		root, _ := resolvedConfigRoot()
		if root == "" {
			root = configPath
		}
		return nil, fmt.Errorf("no config automation files found in %s/automations/config/", root)
	}
	return matches, nil
}
