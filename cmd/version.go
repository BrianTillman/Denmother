package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

// Release builds set these values; source builds retain the development defaults.
var version = "dev"
var revision = "unknown"
var buildDate = "unknown"
var versionJSON bool

func init() {
	command := &cobra.Command{Use: "version", Short: "Print version and build provenance", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		commit := revision
		if commit == "unknown" {
			if info, ok := debug.ReadBuildInfo(); ok {
				for _, setting := range info.Settings {
					if setting.Key == "vcs.revision" {
						commit = setting.Value
					}
					if setting.Key == "vcs.modified" && setting.Value == "true" {
						commit += "-dirty"
					}
				}
			}
		}
		rt := newCommandRuntime("version", versionJSON, cmd.OutOrStdout())
		rt.AddStep(operator.Step{ID: "build", Title: "Build provenance", Status: operator.StatusSuccess, Summary: version, Details: map[string]any{"version": version, "revision": commit, "built_at": buildDate, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}})
		return rt.Complete(operator.StatusSuccess, fmt.Sprintf("Denmother %s (%s/%s)", version, runtime.GOOS, runtime.GOARCH))
	}}
	command.Flags().BoolVar(&versionJSON, "json", false, "Emit build provenance as JSON")
	rootCmd.AddCommand(command)
}
