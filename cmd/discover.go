package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/BrianTillman/Denmother/internal/discover"
	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	discoverProdURL   string
	discoverProdToken string
	discoverDevURL    string
	discoverDevToken  string
	discoverHAURL     string
	discoverHAToken   string
	discoverTimeout   int
	discoverOutput    string
	discoverQuiet     bool
	discoverJSON      bool
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover Leviton devices via mDNS and compare with Home Assistant",
	Long: `Discover Leviton devices on the local network using mDNS (HomeKit/Matter)
and compare them against entities in Home Assistant.

This command:
  - Browses mDNS for HomeKit (_hap._tcp) and Matter (_matter._tcp / _matterc._udp) services
  - Filters for Leviton devices by manufacturer, hostname, and vendor ID
  - Fetches entity and device registries from Home Assistant
  - Matches registry MAC, HomeKit, and serial identities, with legacy-name fallback
  - Generates a JSON report with new devices, naming issues, and exclusions

Output: leviton-discovery.json in the selected project references directory
(or use --output to override). Unavailable registries or mDNS services produce
a partial result (exit 3); detected differences produce a warning (exit 2).

This command defaults to the PRODUCTION Home Assistant instance since it
compares against real device entities.

Connection Resolution (in order of precedence):
  1. Command-line flags (--prod-url, --prod-token)
  2. Environment variables (HASS_PROD_URL, HASS_PROD_TOKEN)
  3. Legacy env vars (HASS_URL, HASS_TOKEN)`,
	RunE: runDiscover,
}

func init() {
	discoverCmd.Flags().StringVar(&discoverProdURL, "prod-url", "", "Production Home Assistant URL")
	discoverCmd.Flags().StringVar(&discoverProdToken, "prod-token", "", "Production Home Assistant token")
	discoverCmd.Flags().StringVar(&discoverDevURL, "dev-url", "", "Development Home Assistant URL")
	discoverCmd.Flags().StringVar(&discoverDevToken, "dev-token", "", "Development Home Assistant token")
	discoverCmd.Flags().StringVar(&discoverHAURL, "ha-url", "", "Home Assistant URL (legacy, use --prod-url)")
	discoverCmd.Flags().StringVar(&discoverHAToken, "ha-token", "", "Home Assistant token (legacy, use --prod-token)")
	discoverCmd.Flags().IntVar(&discoverTimeout, "timeout", 30, "mDNS browse timeout in seconds")
	discoverCmd.Flags().StringVar(&discoverOutput, "output", "", "Output JSON file path (default: docs/reference/leviton-discovery.json)")
	discoverCmd.Flags().BoolVarP(&discoverQuiet, "quiet", "q", false, "Suppress output except errors")

	discoverCmd.Flags().BoolVar(&discoverJSON, "json", false, "Emit a machine-readable operator result")
	rootCmd.AddCommand(discoverCmd)
}

type discoverCommandRunner interface {
	Run(context.Context) (*discover.LevitonReport, error)
}

var newDiscoverCommandRunner = func(opts discover.Options) discoverCommandRunner { return discover.NewDiscoverer(opts) }

func runDiscover(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("discover", discoverJSON, cmd.OutOrStdout())
	fail := func(id string, err error) error {
		rt.AddStep(operator.Step{ID: id, Title: "Discover Leviton devices", Status: operator.StatusFailure, Summary: err.Error()})
		if !discoverJSON {
			fmt.Fprintln(cmd.OutOrStdout(), err)
		}
		return rt.Complete(operator.StatusFailure, "discovery failed")
	}
	if discoverTimeout <= 0 {
		return fail("validate-options", fmt.Errorf("--timeout must be positive"))
	}
	settings, err := selectedProject()
	if err != nil {
		return fail("resolve-project", err)
	}
	outputPath := discoverOutput
	if outputPath == "" {
		outputPath = filepath.Join(settings.ReferencesDir, "leviton-discovery.json")
	}
	flags := haconfig.InstanceFlags{ConfigPath: settings.ConfigDir,
		ProdURL: discoverProdURL, ProdToken: discoverProdToken,
		DevURL: discoverDevURL, DevToken: discoverDevToken,
		LegacyURL: discoverHAURL, LegacyToken: discoverHAToken,
	}
	resolved, err := operator.ResolveTargetContext(cmd.Context(), haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	if err != nil {
		return fail("resolve-target", err)
	}
	if !discoverQuiet {
		rt.PrintPreflight()
	}
	runner := newDiscoverCommandRunner(discover.Options{HAUrl: resolved.Config.URL, HAToken: resolved.Config.Token,
		ConfigPath: settings.ConfigDir, Timeout: time.Duration(discoverTimeout) * time.Second,
		OutputPath: outputPath, Quiet: true})
	report, err := runner.Run(cmd.Context())
	if err != nil {
		return fail("discover", err)
	}
	status := operator.StatusSuccess
	if len(report.NewDevices)+len(report.InconsistentNames)+len(report.MatterLightsToExclude) > 0 {
		status = operator.StatusWarning
	}
	if len(report.Warnings) > 0 {
		status = operator.StatusPartial
	}
	for _, warning := range report.Warnings {
		rt.AddHint(warning)
	}
	if !discoverQuiet && !discoverJSON {
		fmt.Fprint(cmd.OutOrStdout(), discover.FormatConsoleSummary(report))
		fmt.Fprintf(cmd.OutOrStdout(), "Results saved to: %s\n", outputPath)
	}
	rt.AddStep(operator.Step{ID: "discover", Title: "Compare discovered devices with Home Assistant", Status: status,
		Summary:   fmt.Sprintf("discovered %d devices; %d unmatched", len(report.DiscoveredDevices), len(report.NewDevices)),
		Artifacts: []string{outputPath}, Details: map[string]any{"report": report}})
	return rt.Complete(status, "discovery report saved")
}
