package validator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

// validateYAML checks configuration YAML recursively, including custom include directories.
func validateYAML(configPath string, opts Options) error {
	color.New(color.FgBlue).Println("Validating YAML syntax...")
	fmt.Println()
	var totalFiles, validFiles int
	err := filepath.WalkDir(configPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := opts.context().Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != configPath && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "tests" || entry.Name() == "custom_components") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "secrets.yaml" || entry.Name() == "secrets.yml" || !util.IsYAMLFile(path) {
			return nil
		}
		totalFiles++
		relPath := util.RelPath(configPath, path)
		if err := validateYAMLFileContext(opts.context(), path); err != nil {
			color.Red("  %s %s", crossMark, relPath)
			color.Red("    %s", err.Error())
		} else {
			validFiles++
			color.Green("  %s %s", checkMark, relPath)
		}
		return nil
	})
	if err == nil {
		err = opts.context().Err()
	}
	if err != nil {
		return fmt.Errorf("enumerate configuration YAML: %w", err)
	}
	fmt.Printf("\nValidated %d/%d YAML files\n", validFiles, totalFiles)
	if totalFiles == 0 {
		return incompleteResult("yaml", "no YAML configuration files found", nil, map[string]any{"config_path": configPath}).Err()
	}
	if validFiles != totalFiles {
		return fmt.Errorf("YAML validation failed")
	}
	return nil
}

// validateYAMLFile parses a single YAML file
func validateYAMLFile(path string) error { return validateYAMLFileContext(context.Background(), path) }

func validateYAMLFileContext(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	content, err := util.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	// Decode YAML nodes to preserve HA tags such as !include and !secret.
	decoder := yaml.NewDecoder(strings.NewReader(content))

	var doc interface{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := decoder.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}

	return nil
}

const (
	checkMark = "\u2713"
	crossMark = "\u2717"
	warning   = "\u26A0"
)
