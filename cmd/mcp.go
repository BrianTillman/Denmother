package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/BrianTillman/Denmother/internal/mcpserver"
	"github.com/spf13/cobra"
)

func init() {
	var cfg mcpserver.Config
	var tokenEnv string
	command := &cobra.Command{Use: "mcp", Short: "Serve intent-based agent tools over local MCP stdio", Args: cobra.NoArgs,
		Long: "Serve a configured project over MCP stdio. stdout is reserved for the protocol. HA targets, credentials and execution policy are configured at startup; clients cannot change them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("config") {
				cfg.ConfigDir = configPath
			}
			if cfg.Timeout < time.Millisecond || cfg.Timeout > time.Hour {
				return fmt.Errorf("MCP configuration: timeout must be between 1ms and 1h")
			}
			cfg.Version = version
			if tokenEnv != "" {
				cfg.Token = os.Getenv(tokenEnv)
			}
			adapter, err := mcpserver.New(cfg)
			if err != nil {
				return fmt.Errorf("MCP configuration: %w", err)
			}
			defer adapter.Close()
			return adapter.RunStdio(cmd.Context())
		},
	}
	command.Flags().StringVar(&cfg.ProjectRoot, "project-root", "", "Required project root; all configuration and evidence access stays within it")
	command.Flags().StringVar(&cfg.TargetURL, "target-url", "", "Explicit HA target URL (no automatic target discovery)")
	command.Flags().StringVar(&cfg.TargetInstance, "target-instance", "development", "Target identity: development or production (production is read-only)")
	command.Flags().StringVar(&tokenEnv, "token-env", "HASS_DEV_TOKEN", "Environment variable containing the configured target's token")
	command.Flags().BoolVar(&cfg.AllowTests, "allow-tests", false, "Enable selected tests against the explicitly configured development target")
	command.Flags().BoolVar(&cfg.AllowSchema, "allow-schema", false, "Permit native schema queries against the project's matching Docker runtime")
	command.Flags().DurationVar(&cfg.Timeout, "timeout", 5*time.Minute, "Tool deadline (1ms to 1h); tests get up to 35s additional cleanup time")
	command.Flags().StringSliceVar(&cfg.EvidenceFiles, "evidence-file", nil, "Register an existing project-relative evidence file as an opaque resource; repeat for multiple files")
	rootCmd.AddCommand(command)
}
