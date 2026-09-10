package validator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/project"
	"gopkg.in/yaml.v3"

	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/fatih/color"
)

var excludeDirs = map[string]bool{
	"dev-config":   true,
	"node_modules": true,
	".git":         true,
	"docs":         true,
}

type violation struct {
	file string
	line int
	text string
	rule string
}

// validateGuard checks for forbidden integrations
func validateGuard(configPath string) error {
	return validateGuardContext(context.Background(), configPath)
}

func validateGuardContext(ctx context.Context, configPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	color.New(color.FgBlue).Println("Checking for forbidden integrations...")
	fmt.Println()

	settings, err := project.ForConfig(configPath)
	if err != nil {
		return err
	}
	forbidden := map[string]bool{}
	for _, name := range settings.ForbiddenIntegrations {
		forbidden[name] = true
	}
	if len(forbidden) == 0 {
		fmt.Println("  No integration restrictions configured")
		return nil
	}
	var violations []violation
	err = filepath.WalkDir(configPath, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "tests" || excludeDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !util.IsYAMLFile(path) || strings.Contains(entry.Name(), "secrets") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var visit func(*yaml.Node)
		visit = func(node *yaml.Node) {
			if node.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(node.Content); i += 2 {
					key, value := node.Content[i], node.Content[i+1]
					name := key.Value
					if name == "platform" {
						name = value.Value
					}
					if forbidden[name] {
						violations = append(violations, violation{file: util.RelPath(configPath, path), line: key.Line, rule: name, text: "integration prohibited by project policy"})
					}
				}
			}
			for _, child := range node.Content {
				visit(child)
			}
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var node yaml.Node
			err := decoder.Decode(&node)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			visit(&node)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(violations) > 0 {
		color.Red("  %s Found integrations prohibited by project policy:", crossMark)
		fmt.Println()
		for _, v := range violations {
			fmt.Printf("    [%s] %s:%d: %s\n", v.rule, v.file, v.line, v.text)
		}
		fmt.Println()
		return fmt.Errorf("guard check failed: %d violation(s)", len(violations))
	}

	color.Green("  %s No integrations prohibited by project policy", checkMark)
	fmt.Println()
	return ctx.Err()
}
