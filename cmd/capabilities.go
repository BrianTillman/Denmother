package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func resultSchemaObject() map[string]any {
	var schema map[string]any
	_ = json.Unmarshal([]byte(operator.ResultJSONSchema()), &schema)
	return schema
}

type commandPolicy struct {
	MutationScope []string `json:"mutation_scope"`
	Prerequisites []string `json:"prerequisites"`
	DefaultTarget string   `json:"default_target"`
	Notes         string   `json:"notes,omitempty"`
}

type flagCapability struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type commandCapability struct {
	Command     string           `json:"command"`
	Usage       string           `json:"usage"`
	Description string           `json:"description"`
	JSON        bool             `json:"json"`
	Flags       []flagCapability `json:"flags,omitempty"`
	commandPolicy
}

func init() {
	var jsonOutput, schemas bool
	var selectedCommand string
	command := &cobra.Command{Use: "capabilities", Short: "Describe CLI commands, flags, prerequisites, and result schemas", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := newCommandRuntime("capabilities", jsonOutput, cmd.OutOrStdout())
			filter := strings.TrimSpace(selectedCommand)
			if filter != "" && filter != "dm" && !strings.HasPrefix(filter, "dm ") {
				filter = "dm " + filter
			}
			var commands []commandCapability
			var visit func(*cobra.Command)
			visit = func(c *cobra.Command) {
				if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
					return
				}
				if c.Runnable() && (filter == "" || c.CommandPath() == filter || strings.HasPrefix(c.CommandPath(), filter+" ")) {
					entry := commandCapability{Command: c.CommandPath(), Usage: c.UseLine(), Description: c.Short, commandPolicy: policyForCommand(c.CommandPath())}
					flags := pflag.NewFlagSet("capabilities", pflag.ContinueOnError)
					flags.AddFlagSet(c.InheritedFlags())
					flags.AddFlagSet(c.Flags())
					entry.JSON = flags.Lookup("json") != nil
					flags.VisitAll(func(f *pflag.Flag) {
						if !f.Hidden && filter != "" {
							entry.Flags = append(entry.Flags, flagCapability{f.Name, f.Value.Type(), f.DefValue, f.Usage})
						}
					})
					commands = append(commands, entry)
				}
				for _, child := range c.Commands() {
					visit(child)
				}
			}
			visit(rootCmd)
			if len(commands) == 0 {
				return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("no matching command; use capabilities without --command to list commands")}
			}
			details := map[string]any{
				"capabilities_version": "dm.capabilities.v1", "version": version,
				"result_schema_version": operator.SchemaVersion,
				"commands":              commands, "non_interactive": true,
				"discovery":      "Use --command 'test' or --command 'agent context' for flags and defaults; add --schemas for the complete JSON result schema.",
				"output_options": "--compact writes full evidence and limits JSON details; --evidence-dir writes full evidence without compacting. These flags write local files and require --json.",
				"error_codes":    []string{"invalid_arguments", "invalid_project", "command_failed", "step_failed", "guardrail_blocked", "validation_failed", "verification_incomplete", "result_processing_failed"},
			}
			if schemas {
				details["result_schema"] = resultSchemaObject()
			}
			rt.AddStep(operator.Step{ID: "capabilities", Title: "Discover Denmother", Status: operator.StatusSuccess, Summary: "CLI capability discovery", Details: details})
			return rt.Complete(operator.StatusSuccess, "Denmother capabilities collected")
		}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable command metadata and result schemas")
	command.Flags().StringVar(&selectedCommand, "command", "", "Show detailed flags for a command or command group (for example 'test' or 'agent context')")
	command.Flags().BoolVar(&schemas, "schemas", false, "Include the complete operator JSON schema")
	rootCmd.AddCommand(command)
}

func policyForCommand(name string) commandPolicy {
	p := commandPolicy{MutationScope: []string{}, Prerequisites: []string{}, DefaultTarget: "none"}
	switch name {
	case "dm", "dm validate":
		p.Prerequisites = []string{"HA YAML configuration", "Docker and matching HA container for native schema validation (unless --no-schema)"}
	case "dm agent context":
		p.Prerequisites = []string{"HA YAML configuration", "Docker and matching HA container for complete schema verification"}
	case "dm agent review", "dm agent next", "dm agent tasks", "dm test plan":
		p.Prerequisites = []string{"HA YAML configuration", "Git for diff discovery"}
	case "dm agent lint", "dm agent quality", "dm test doctor":
		p.Prerequisites = []string{"HA YAML configuration"}
	case "dm init", "dm plan init", "dm plan complete", "dm schema", "dm sort", "dm docs generate":
		p.MutationScope = []string{"local_files"}
		p.Notes = "Inspect flags: dry-run/check modes can avoid writes. init preserves existing files."
	case "dm skills install", "dm skills uninstall", "dm setup":
		p.MutationScope = []string{"local_files"}
		p.Notes = "Ownership-protected installation; --dry-run is read-only. No HA or agent session is launched."
	case "dm skills status", "dm plan status", "dm version", "dm capabilities":
	case "dm mcp":
		p.MutationScope = []string{"local_files", "subprocesses"}
		p.Prerequisites = []string{"configured project root", "MCP stdio client"}
		p.Notes = "Host-configured tools preserve CLI results; --allow-tests enables development HA mutation. Production testing and runtime reset are unavailable."
	case "dm test", "dm check":
		p.MutationScope = []string{"ha_state"}
		p.Prerequisites = []string{"running HA and credentials", "test specifications"}
		p.DefaultTarget = "development"
		if name == "dm check" {
			p.DefaultTarget = "local"
			p.MutationScope = append(p.MutationScope, "local_runtime")
			p.Notes = "local/dev profiles run mutating tests; prod is read-only. --ensure-dev can create a local runtime."
		} else {
			p.Notes = "Production requires explicit --allow-prod; never add it automatically."
		}
	case "dm trace", "dm logs", "dm observe", "dm audit", "dm verify", "dm sync", "dm discover":
		p.Prerequisites = []string{"HA URL and credentials"}
		p.DefaultTarget = "development"
		if name == "dm audit" || name == "dm verify" || name == "dm sync" || name == "dm discover" {
			p.DefaultTarget = "production"
		}
		if name == "dm sync" || name == "dm discover" {
			p.MutationScope = []string{"local_files"}
		}
		if name == "dm verify" {
			p.Notes = "--dry-run uses local expectations without querying HA."
		}
		if name == "dm discover" {
			p.Prerequisites = append(p.Prerequisites, "local network mDNS access")
		}
	case "dm dev up", "dm dev down", "dm dev reset":
		p.MutationScope = []string{"local_files", "local_runtime"}
		p.Prerequisites = []string{"Docker Engine and Compose v2", "dedicated secret-free development YAML"}
		p.DefaultTarget = "local"
		if name == "dm dev reset" {
			p.Notes = "Discards local runtime volumes."
		}
	case "dm dev doctor", "dm dev status":
		p.Prerequisites = []string{"Docker Engine and Compose v2"}
		p.DefaultTarget = "local"
	case "dm dev dashboard":
		p.MutationScope = []string{"local_files", "local_runtime"}
		p.DefaultTarget = "local"
		p.Prerequisites = []string{"development HA", "browser dependencies for --render"}
		p.Notes = "--render writes artifacts; --ensure-dev starts the runtime."
	case "dm dev fixtures":
		p.MutationScope = []string{"local_files"}
		p.DefaultTarget = "production"
		p.Prerequisites = []string{"HA URL and credentials"}
	case "dm dev scenario":
		p.MutationScope = []string{"ha_state"}
		p.DefaultTarget = "local"
		p.Prerequisites = []string{"local development HA", "scenario definitions"}
		p.Notes = "--list does not apply state changes."
	case "dm release check", "dm release build":
		p.MutationScope = []string{"local_files", "subprocesses"}
		p.Prerequisites = []string{"Denmother source", "Go toolchain", "Python 3 for release check"}
	default:
		p.MutationScope = []string{"unknown"}
		p.Notes = "Consult command help before execution."
	}
	return p
}

func stringSliceContains(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}
