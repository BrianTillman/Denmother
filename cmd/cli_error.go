package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

type cliInputError struct {
	Code string
	Err  error
}

func (e *cliInputError) Error() string { return e.Err.Error() }
func (e *cliInputError) Unwrap() error { return e.Err }

func commandJSON(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	flag := cmd.Flags().Lookup("json")
	if flag == nil {
		flag = cmd.InheritedFlags().Lookup("json")
	}
	return flag != nil && flag.Value.String() == "true"
}

func structuredCLIError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var emitted *operator.ExitError
	if errors.As(err, &emitted) {
		return err
	}
	jsonOutput := commandJSON(cmd)
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			jsonOutput = true
		}
		if arg == "--json=false" {
			jsonOutput = false
		}
	}
	if !jsonOutput {
		return err
	}
	if cmd == nil {
		cmd = rootCmd
	}
	code := "command_failed"
	summary := redactCLIError(err.Error())
	var input *cliInputError
	if errors.As(err, &input) {
		code = input.Code
	} else if strings.Contains(summary, "unknown command") || strings.Contains(summary, "unknown flag") || strings.Contains(summary, "unknown shorthand") || strings.Contains(summary, "invalid argument") || strings.Contains(summary, "arg(s)") || strings.Contains(summary, "required flag") || strings.Contains(summary, "flag needs an argument") {
		code = "invalid_arguments"
		// Cobra errors can echo arbitrary positional or flag values, including credentials.
		summary = "invalid command arguments; inspect command help for accepted flags and positional arguments"
	}
	rt := newCommandRuntime(strings.TrimPrefix(cmd.CommandPath(), "dm "), true, cmd.OutOrStdout())
	rt.AddStep(operator.Step{ID: "cli", Title: "Execute command", Status: operator.StatusFailure, ErrorCode: code, Summary: summary,
		NextCommands: []string{cmd.CommandPath() + " --help"}})
	return rt.Complete(operator.StatusFailure, summary)
}

func redactCLIError(message string) string {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if value != "" && (strings.Contains(key, "TOKEN") || strings.Contains(key, "PASSWORD")) {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	for i, arg := range os.Args[1:] {
		if !strings.Contains(arg, "token") && !strings.Contains(arg, "password") {
			continue
		}
		_, value, equal := strings.Cut(arg, "=")
		if !equal && i+2 < len(os.Args) {
			value = os.Args[i+2]
		}
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return message
}

func runJSONValidation(cmd *cobra.Command) error {
	if schemaOnly && noSchema {
		return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("--schema and --no-schema cannot be combined")}
	}
	root, err := resolvedConfigRoot()
	if err != nil {
		return &cliInputError{Code: "invalid_project", Err: err}
	}
	info, err := os.Stat(root)
	if err != nil {
		return &cliInputError{Code: "invalid_project", Err: err}
	}
	if !info.IsDir() {
		return &cliInputError{Code: "invalid_project", Err: fmt.Errorf("configuration is not a directory")}
	}
	all := !lintOnly && !entitiesOnly && !guardOnly && !schemaOnly
	checks := []string{}
	if all || lintOnly {
		checks = append(checks, "yaml")
	}
	if all {
		checks = append(checks, "config")
	}
	if all || guardOnly {
		checks = append(checks, "guard")
	}
	if all || entitiesOnly {
		checks = append(checks, "entities")
	}
	if (all && !noSchema) || schemaOnly {
		checks = append(checks, "schema")
	}
	summary := runSelectedValidation(root, true, checks)
	step := summary.Step()
	if step.Status == operator.StatusFailure {
		step.ErrorCode = "validation_failed"
	}
	step.Details["schema_requested"] = (all && !noSchema) || schemaOnly
	step.Details["runtime_tests_run"] = false
	step.Details["scope"] = "selected_validation_checks"
	rt := newCommandRuntime("validate", true, cmd.OutOrStdout())
	rt.AddStep(step)
	return rt.Complete(step.Status, step.Summary)
}
