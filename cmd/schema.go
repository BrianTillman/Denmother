package cmd

import (
	"fmt"

	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Generate VSCode entity schema for autocomplete",
	Long: `Generate a JSON Schema from entity-list.txt for VSCode autocomplete.

This command reads docs/reference/entity-list.txt and generates a JSON Schema
that VSCode's YAML extension uses to provide entity ID autocomplete.

The schema is saved to .vscode/ha-entities.schema.json

Prerequisites:
  - Run 'dm sync' first to populate entity-list.txt
  - Install the Red Hat YAML extension in VSCode

Examples:
  dm schema                    # Generate schema from entity-list.txt`,
	RunE: runSchema,
}

func init() {
	rootCmd.AddCommand(schemaCmd)
}

func runSchema(cmd *cobra.Command, args []string) error {
	fmt.Println()
	color.New(color.FgCyan, color.Bold).Println("Entity Schema Generator")
	fmt.Println()

	schemaPath, err := hasync.GenerateEntitySchema(configPath)
	if err != nil {
		color.Red("Error: %v", err)
		return err
	}

	color.Green("Schema generated successfully!")
	fmt.Printf("Output: %s\n", schemaPath)
	fmt.Println()
	fmt.Println("To enable autocomplete in VSCode:")
	fmt.Println("  1. Install the Red Hat YAML extension")
	fmt.Println("  2. Ensure .vscode/settings.json contains the yaml.schemas config")
	fmt.Println("  3. Reload VSCode window")

	return nil
}
