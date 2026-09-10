package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/BrianTillman/Denmother/internal/validator"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	configPath     string
	configExplicit bool
	fix            bool
	lintOnly       bool
	entitiesOnly   bool
	guardOnly      bool
	schemaOnly     bool
	noSchema       bool
	validationJSON bool
)

var rootCmd = &cobra.Command{
	Use:   "dm",
	Short: "Home Assistant configuration validator and sync tool",
	Long: `Validate Home Assistant YAML and test automations against an isolated runtime.

Run dm without a subcommand to check syntax, entity references, and native HA
schema. Use --no-schema for offline checks, or --lint for YAML syntax only.

Examples:
  dm --config examples/quickstart/ha-config --no-schema
  dm dev up --config examples/quickstart/ha-config --json
  dm test --config examples/quickstart/ha-config --json
  dm dev down --config examples/quickstart/ha-config --json

Use "dm COMMAND --help" for command options. Provide HA tokens through environment
variables; see docs/project.md for project settings and target selection.`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		configExplicit = cmd.Flag("config").Changed
		if (compactOutput || evidenceDir != "") && !commandJSON(cmd) {
			return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("--compact and --evidence-dir require --json")}
		}
		if !projectCommand(cmd.CommandPath()) {
			return nil
		}
		_, err := selectedProject()
		if err != nil {
			return &cliInputError{Code: "invalid_project", Err: err}
		}
		return err
	}
	rootCmd.RunE = runValidation
	// Persistent flags available to all subcommands
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "ha-config", "Path to HA config directory")
	rootCmd.PersistentFlags().BoolVar(&compactOutput, "compact", false, "Limit JSON details and save the complete result as a local evidence artifact")
	rootCmd.PersistentFlags().StringVar(&evidenceDir, "evidence-dir", "", "Save complete JSON evidence in this directory (default with --compact: artifacts/dm)")
	rootCmd.Flags().BoolVar(&validationJSON, "json", false, "Emit structured validation results without running automation tests")

	// Local flags for validation only
	rootCmd.Flags().BoolVarP(&fix, "fix", "f", false, "Show suggestions for invalid entities")
	rootCmd.Flags().BoolVar(&lintOnly, "lint", false, "Run YAML linting only")
	rootCmd.Flags().BoolVar(&entitiesOnly, "entities", false, "Run entity validation only")
	rootCmd.Flags().BoolVar(&guardOnly, "guard", false, "Run integration guard only")
	rootCmd.Flags().BoolVar(&schemaOnly, "schema", false, "Run HA schema validation only (requires docker)")
	rootCmd.Flags().BoolVar(&noSchema, "no-schema", false, "Skip HA schema validation (static/offline checks)")
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DM_MCP_WORKER_ROOT") != "" {
		workerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = workerCtx
		go func() {
			var signal [1]byte
			_, _ = os.Stdin.Read(signal[:])
			cancel() // Cancellation byte or parent disconnect, including on Windows.
		}()
	}
	rootCmd.SetContext(ctx)
	executed, err := rootCmd.ExecuteC()
	return structuredCLIError(executed, err)
}

func runValidation(cmd *cobra.Command, args []string) error {
	if validationJSON {
		return runJSONValidation(cmd)
	}
	if schemaOnly && noSchema {
		return fmt.Errorf("--schema and --no-schema cannot be combined")
	}
	resolved, err := resolvedConfigRoot()
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("configuration directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("configuration path is not a directory: %s", resolved)
	}

	runAll := !lintOnly && !entitiesOnly && !guardOnly && !schemaOnly

	v := validator.New(resolved, validator.Options{
		Fix:     fix,
		Context: cmd.Context(),
	})

	var hasErrors bool
	var incomplete error
	record := func(err error) {
		if err == nil {
			return
		}
		if validator.IsIncomplete(err) {
			if incomplete == nil {
				incomplete = err
			}
			return
		}
		hasErrors = true
	}

	fmt.Println()
	color.New(color.FgCyan, color.Bold).Println("Home Assistant Configuration Validator")
	fmt.Println()

	if runAll || lintOnly {
		record(v.ValidateYAML())
	}
	if runAll {
		record(v.ValidateConfig())
	}
	if runAll || guardOnly {
		record(v.ValidateGuard())
	}
	if runAll || entitiesOnly {
		record(v.ValidateEntities())
	}
	if (runAll && !noSchema) || schemaOnly {
		record(v.ValidateSchema())
	}

	fmt.Println()
	if hasErrors {
		color.Red("Validation failed with errors")
		return fmt.Errorf("validation failed")
	}
	if incomplete != nil {
		color.Yellow("Validation incomplete")
		return incomplete
	}

	color.Green("All validations passed!")
	return nil
}

func commandContext() context.Context {
	if rootCmd.Context() != nil {
		return rootCmd.Context()
	}
	return context.Background()
}
