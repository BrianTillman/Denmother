package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/BrianTillman/Denmother/internal/yamlsort"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	sortCheck    bool
	sortStaged   bool
	sortKey      string
	sortEntities bool
	sortVerbose  bool
	sortPattern  string
)

var sortCmd = &cobra.Command{
	Use:   "sort [file...]",
	Short: "Sort YAML list items by key",
	Long: `Sort YAML files by a specified key (default: unique_id).

This command sorts list items in YAML files alphabetically by a key field.
It's useful for maintaining consistent ordering in configuration files.

Default patterns (when no files specified):
  - helpers/**/*.yaml
  - automations/**/*.yaml
  - scenes/**/*.yaml

Examples:
  # Sort all YAML files in ha-config/
  dm sort

  # Check if files are sorted (for CI/pre-commit)
  dm sort --check

  # Sort only staged files
  dm sort --staged

  # Sort by a different key
  dm sort --key id

  # Also sort entity lists within groups
  dm sort --sort-entities

  # Sort specific files
  dm sort ha-config/helpers/light-groups/office.yaml

  # Sort files matching a pattern
  dm sort --pattern "helpers/light-groups/*.yaml"`,
	RunE: runSort,
}

func init() {
	sortCmd.Flags().BoolVar(&sortCheck, "check", false, "Check if files are sorted without modifying")
	sortCmd.Flags().BoolVarP(&sortStaged, "staged", "s", false, "Only process git staged files")
	sortCmd.Flags().StringVar(&sortKey, "key", "unique_id", "Key to sort list items by")
	sortCmd.Flags().BoolVar(&sortEntities, "sort-entities", true, "Sort entity lists within groups")
	sortCmd.Flags().BoolVarP(&sortVerbose, "verbose", "v", false, "Verbose output")
	sortCmd.Flags().StringVar(&sortPattern, "pattern", "", "Glob pattern to filter files (relative to config dir)")

	rootCmd.AddCommand(sortCmd)
}

func runSort(cmd *cobra.Command, args []string) error {
	fmt.Println()
	color.New(color.FgCyan, color.Bold).Println("YAML Sorter")
	fmt.Println()

	files, err := discoverSortFiles(configPath, args)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		color.Yellow("No YAML files found to sort")
		return nil
	}

	if sortVerbose {
		fmt.Printf("Found %d files to process\n\n", len(files))
	}

	opts := yamlsort.Options{
		SortKey:      sortKey,
		SortEntities: sortEntities,
		CheckOnly:    sortCheck,
		Verbose:      sortVerbose,
	}
	sorter := yamlsort.NewSorter(opts)

	results := yamlsort.NewResults()
	for _, file := range files {
		result, err := sorter.SortFile(file)
		if err != nil && sortVerbose {
			color.Red("  Error processing %s: %v", util.RelPath(configPath, file), err)
		}
		results.Add(result)

		if sortVerbose && result != nil {
			relPath := util.RelPath(configPath, file)
			if result.Error != nil {
				color.Red("  ✗ %s: %v", relPath, result.Error)
			} else if result.ItemCount == 0 {
				color.Yellow("  - %s (skipped, not a list)", relPath)
			} else if result.WasSorted {
				color.Green("  ✓ %s (already sorted)", relPath)
			} else if sortCheck {
				color.Yellow("  ✗ %s (needs sorting)", relPath)
			} else {
				color.Green("  ✓ %s (sorted)", relPath)
			}
		}
	}

	fmt.Println()
	if sortCheck {
		color.New(color.FgCyan, color.Bold).Println("Check Results")
	} else {
		color.New(color.FgCyan, color.Bold).Println("Sort Results")
	}
	fmt.Println()

	fmt.Printf("Files processed: %d\n", results.TotalProcessed())
	if len(results.Sorted) > 0 {
		if sortCheck {
			color.Yellow("Files needing sorting: %d", len(results.Sorted))
			for _, f := range results.Sorted {
				fmt.Printf("  - %s\n", util.RelPath(configPath, f))
			}
		} else {
			color.Green("Files sorted: %d", len(results.Sorted))
			for _, f := range results.Sorted {
				fmt.Printf("  - %s\n", util.RelPath(configPath, f))
			}
		}
	}
	if len(results.Unchanged) > 0 && sortVerbose {
		fmt.Printf("Already sorted: %d\n", len(results.Unchanged))
	}
	if len(results.Skipped) > 0 && sortVerbose {
		fmt.Printf("Skipped (not lists): %d\n", len(results.Skipped))
	}
	if len(results.Errors) > 0 {
		color.Red("Errors: %d", len(results.Errors))
		for f, e := range results.Errors {
			fmt.Printf("  - %s: %v\n", util.RelPath(configPath, f), e)
		}
	}

	fmt.Println()

	if results.HasErrors() {
		return fmt.Errorf("errors occurred during sorting")
	}
	if sortCheck && results.NeedsSorting() {
		color.Yellow("Some files need sorting. Run: dm sort")
		return fmt.Errorf("files need sorting")
	}

	color.Green("Done!")
	return nil
}

func discoverSortFiles(basePath string, args []string) ([]string, error) {
	if len(args) > 0 {
		var files []string
		for _, arg := range args {
			if containsGlobChars(arg) {
				matches, err := doublestar.FilepathGlob(arg)
				if err != nil {
					return nil, fmt.Errorf("invalid pattern %s: %w", arg, err)
				}
				files = append(files, matches...)
			} else {
				files = append(files, arg)
			}
		}
		return files, nil
	}

	if sortStaged {
		stagedFiles, err := util.GetStagedYAMLFilesContext(commandContext(), ".")
		if err != nil {
			return nil, fmt.Errorf("failed to get staged files: %w", err)
		}
		base, err := filepath.Abs(basePath)
		if err != nil {
			return nil, err
		}
		var files []string
		for _, f := range stagedFiles {
			absolute, err := filepath.Abs(f)
			if err != nil {
				return nil, err
			}
			relative, err := filepath.Rel(base, absolute)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				files = append(files, f)
			}
		}
		return files, nil
	}

	if sortPattern != "" {
		pattern := filepath.Join(basePath, sortPattern)
		return doublestar.FilepathGlob(pattern)
	}

	// Default patterns for sortable files
	defaultPatterns := []string{
		"helpers/**/*.yaml",
		"automations/**/*.yaml",
		"scenes/**/*.yaml",
	}

	var files []string
	for _, pattern := range defaultPatterns {
		fullPattern := filepath.Join(basePath, pattern)
		matches, err := doublestar.FilepathGlob(fullPattern)
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}

	return files, nil
}

func containsGlobChars(s string) bool {
	for _, c := range s {
		if c == '*' || c == '?' || c == '[' {
			return true
		}
	}
	return false
}
