package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/install"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

func installationCommand(name, action string, binary bool) *cobra.Command {
	var o install.Options
	var jsonOutput bool
	command := &cobra.Command{Use: name, Short: map[string]string{"install": "Install the embedded skill with ownership and modification checks", "status": "Inspect registrations without launching an agent", "uninstall": "Remove unchanged owned skill files", "setup": "Install this binary and matching skills"}[name], Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if compactOutput || evidenceDir != "" {
			return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("installer commands do not accept --compact or --evidence-dir; use --json and redirect stdout to retain the read-only contract")}
		}
		for _, name := range []string{"destination", "bin-dir", "project"} {
			flag := cmd.Flags().Lookup(name)
			if flag != nil && flag.Changed && strings.TrimSpace(flag.Value.String()) == "" {
				return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("--%s must not be empty", name)}
			}
		}
		o.Version = version
		o.Binary = binary
		if o.Origin == "" {
			o.Origin = "existing-binary"
		}
		if o.Revision == "" {
			o.Revision = revision
		}
		report := install.RunContext(commandContext(), o, action)
		if binary && !o.DryRun && report.Exit != 1 && report.Exit != 3 {
			ctx, cancel := context.WithTimeout(commandContext(), 30*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, report.Binary, "version", "--json").Output()
			var result operator.Result
			matched := false
			if err == nil && json.Unmarshal(output, &result) == nil && result.SchemaVersion == operator.SchemaVersion {
				for _, step := range result.Steps {
					if step.ID == "build" && step.Details["version"] == version && step.Details["os"] == runtime.GOOS && step.Details["arch"] == runtime.GOARCH {
						matched = true
					}
				}
			}
			if !matched {
				report.Exit = 3
				report.ErrorCode = "incomplete_verification"
				report.Findings = append(report.Findings, "Installed binary version execution failed; inspect applied operations and retry setup")
			}
		}
		status := operator.StatusSuccess
		switch report.Exit {
		case 1:
			status = operator.StatusFailure
		case 2:
			status = operator.StatusWarning
		case 3:
			status = operator.StatusPartial
		}
		rt := newCommandRuntime(strings.TrimPrefix(cmd.CommandPath(), "dm "), jsonOutput, cmd.OutOrStdout())
		summary := "Installation " + action + " complete"
		if o.DryRun {
			summary = "Installation plan"
		}
		if report.Exit == 1 {
			summary = "Installation blocked"
		}
		if report.Exit == 3 {
			summary = "Installation verification incomplete"
		}
		rt.AddStep(operator.Step{ID: "installation", Title: summary, Status: status, Summary: summary, ErrorCode: report.ErrorCode, Mutates: action != "status" && !o.DryRun, Details: map[string]any{"installation": report}, Hints: report.Findings})
		if !jsonOutput {
			for _, op := range report.Operations {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s (%s)\n", op.Kind, op.Destination, op.Action, op.State)
				for _, c := range op.Conflicts {
					fmt.Fprintln(cmd.OutOrStdout(), c)
				}
				for _, p := range op.Leftovers {
					fmt.Fprintln(cmd.OutOrStdout(), "Preserved:", p)
				}
			}
		}
		return rt.Complete(status, summary)
	}}
	f := command.Flags()
	f.StringSliceVar(&o.Harnesses, "harness", nil, "Harness selection: detected (default), codex, claude; repeat or comma-separate")
	f.StringVar(&o.Scope, "scope", "user", "Skill scope: user or project")
	f.StringVar(&o.Project, "project", "", "Explicit project root (required with project scope)")
	f.StringVar(&o.Destination, "destination", "", "Exact skill directory for one selected harness")
	f.BoolVar(&o.DryRun, "dry-run", false, "Plan without filesystem writes or network access")
	f.BoolVar(&o.Offline, "offline", false, "Prohibit network use (skill operations are always local)")
	f.BoolVar(&jsonOutput, "json", false, "Emit the shared JSON result envelope")
	if action != "status" {
		f.BoolVar(&o.Recover, "recover", false, "Roll back an interrupted owned transaction before retrying; preserve subsequent edits")
	}
	if binary {
		f.BoolVar(&o.BinaryOnly, "binary-only", false, "Install only the binary")
		f.StringVar(&o.BinDir, "bin-dir", "", "Binary directory (default $HOME/.local/bin)")
		f.StringVar(&o.Origin, "origin", "", "Bootstrap provenance (source, archive, download, or existing-binary)")
		f.StringVar(&o.Revision, "source-revision", "", "Source revision and dirty state supplied by bootstrap")
	}
	return command
}
func init() {
	skills := &cobra.Command{Use: "skills", Short: "Manage Denmother skill registrations"}
	for _, action := range []string{"install", "status", "uninstall"} {
		skills.AddCommand(installationCommand(action, action, false))
	}
	rootCmd.AddCommand(skills, installationCommand("setup", "install", true))
}

// Error rendering must honor the same no-evidence-write rule as successful
// installer results, including malformed arguments rejected before RunE.
func installationCommandName(command string) bool {
	command = strings.TrimPrefix(command, "dm ")
	return command == "setup" || command == "skills" || strings.HasPrefix(command, "skills ")
}
