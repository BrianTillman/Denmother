package haverify

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
)

var (
	bold    = color.New(color.Bold)
	green   = color.New(color.FgGreen)
	red     = color.New(color.FgRed)
	yellow  = color.New(color.FgYellow)
	cyan    = color.New(color.FgCyan)
	dimText = color.New(color.Faint)
)

// PrintReport prints the verification results to stdout.
func PrintReport(results []DeviceResult, verbose bool) {
	fmt.Println()
	cyan.Add(color.Bold).Println("Device Parameter Verification")
	fmt.Println()

	totalDevices := 0
	devicesWithFailures := 0

	for _, r := range results {
		if r.Skipped {
			fmt.Printf("  ")
			yellow.Printf("SKIP")
			fmt.Printf("  %s", r.Name)
			dimText.Printf(" (%s)\n", filepath.Base(r.SourceFile))
			dimText.Printf("        %s\n\n", r.SkipReason)
			continue
		}

		totalDevices++

		fmt.Printf("  ")
		bold.Printf("%s", r.Name)
		dimText.Printf(" (%s)\n", filepath.Base(r.SourceFile))

		if r.Failed == 0 && r.NotFound == 0 {
			if verbose {
				for _, p := range r.Params {
					fmt.Printf("    ")
					green.Printf("PASS")
					fmt.Printf("  %-35s %s\n", p.MQTTParam, p.Expected)
				}
			} else {
				fmt.Printf("    ")
				green.Printf("All %d parameters match\n", r.Passed)
			}
		} else {
			devicesWithFailures++
			for _, p := range r.Params {
				if p.Pass && !verbose {
					continue
				}
				fmt.Printf("    ")
				if p.Pass {
					green.Printf("PASS")
					fmt.Printf("  %-35s %s\n", p.MQTTParam, p.Expected)
				} else if p.NotFound {
					yellow.Printf("MISS")
					fmt.Printf("  %-35s expected: %-15s (entity not found: %s)\n", p.MQTTParam, p.Expected, p.EntityID)
				} else if p.Unavailable {
					yellow.Printf("UNAVAIL")
					fmt.Printf(" %-35s expected: %-15s (entity unavailable: %s)\n", p.MQTTParam, p.Expected, p.EntityID)
				} else if p.InvalidOption {
					red.Printf("INVALID")
					fmt.Printf(" %-35s expected: %-15s actual: %-15s (select option rejected by %s; valid options: %s)\n", p.MQTTParam, p.Expected, p.Actual, p.EntityID, strings.Join(p.Options, ", "))
				} else {
					red.Printf("FAIL")
					fmt.Printf("  %-35s expected: %-15s actual: %s\n", p.MQTTParam, p.Expected, p.Actual)
				}
			}
			fmt.Printf("    ")
			dimText.Printf("%d/%d passed", r.Passed, r.Passed+r.Failed+r.NotFound)
			if r.Failed > 0 {
				dimText.Printf(", %d failed", r.Failed)
			}
			if r.NotFound > 0 {
				dimText.Printf(", %d not found", r.NotFound)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if totalDevices > 0 {
		bold.Printf("Summary: ")
		fmt.Printf("%d device(s) verified", totalDevices)
		if devicesWithFailures > 0 {
			fmt.Printf(", ")
			red.Printf("%d with failures", devicesWithFailures)
		} else {
			fmt.Printf(", ")
			green.Printf("all passing")
		}
		fmt.Println()
	}
}

// PrintDryRunReport prints expected values without querying HA.
func PrintDryRunReport(results []DryRunResult) {
	fmt.Println()
	cyan.Add(color.Bold).Println("Device Parameter Verification (dry run)")
	fmt.Println()

	for _, r := range results {
		if r.Skipped {
			fmt.Printf("  ")
			yellow.Printf("SKIP")
			fmt.Printf("  %s", r.Name)
			dimText.Printf(" (%s)\n", filepath.Base(r.SourceFile))
			dimText.Printf("        %s\n\n", r.SkipReason)
			continue
		}

		fmt.Printf("  ")
		bold.Printf("%s", r.Name)
		dimText.Printf(" (%s)\n", filepath.Base(r.SourceFile))
		if r.Z2MName != "" {
			dimText.Printf("    Z2M name: %s → prefix: %s\n", r.Z2MName, r.EntityPrefix)
		}

		for _, e := range r.Expectations {
			entityID := FindExpectedEntityID(r.EntityPrefix, e.MQTTParam)
			if e.EntityID != "" {
				entityID = e.EntityID
			}
			fmt.Printf("    %-35s → %-45s = %s\n", e.MQTTParam, entityID, e.Value)
		}
		fmt.Printf("    %d parameters expected\n\n", len(r.Expectations))
	}
}

// DryRunResult holds the parsed expectations without live comparison.
type DryRunResult struct {
	Name         string
	SourceFile   string
	Z2MName      string
	EntityPrefix string
	Expectations []ParamExpectation
	Skipped      bool
	SkipReason   string
}

// FindExpectedEntityID builds a number-domain candidate for dry-run display.
// Live lookup also searches select and sensor domains.
func FindExpectedEntityID(prefix, mqttParam string) string {
	return "number." + prefix + "_" + MQTTParamToEntitySuffix(mqttParam)
}
