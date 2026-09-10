package haverify

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigAutomation represents a parsed config automation YAML file.
type ConfigAutomation struct {
	ID           string `yaml:"id"`
	Alias        string `yaml:"alias"`
	Description  string `yaml:"description"`
	UseBlueprint struct {
		Path  string                 `yaml:"path"`
		Input map[string]interface{} `yaml:"input"`
	} `yaml:"use_blueprint"`
}

// BlueprintInput represents a single input definition from a blueprint.
type BlueprintInput struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Default     interface{} `yaml:"default"`
}

// Blueprint represents the parsed blueprint YAML structure.
type Blueprint struct {
	BlueprintMeta struct {
		Name  string                    `yaml:"name"`
		Input map[string]BlueprintInput `yaml:"input"`
	} `yaml:"blueprint"`
	Variables map[string]interface{} `yaml:"variables"`
	Actions   []interface{}          `yaml:"actions"`
}

// LoadConfigAutomation returns the first automation from a YAML list.
// Empty lists and non-list documents return an error.
func LoadConfigAutomation(path string) (*ConfigAutomation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config automation: %w", err)
	}

	var items []ConfigAutomation
	if err := yaml.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse config automation %s: %w", path, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no automation found in %s", path)
	}
	return &items[0], nil
}

// LoadBlueprint loads and parses a blueprint YAML file.
// configPath is the selected HA configuration directory; blueprintPath is the path
// from the automation's use_blueprint.path (e.g., "example/device_settings.yaml").
func LoadBlueprint(configPath, blueprintPath string) (*Blueprint, error) {
	fullPath := filepath.Join(configPath, "blueprints", "automation", blueprintPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read blueprint: %w", err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("parse blueprint %s: %w", fullPath, err)
	}
	return &bp, nil
}

// ExtractDefaults returns a map of input name → default value from the blueprint.
func ExtractDefaults(bp *Blueprint) map[string]interface{} {
	defaults := make(map[string]interface{})
	for name, input := range bp.BlueprintMeta.Input {
		if input.Default != nil {
			defaults[name] = input.Default
		}
	}
	return defaults
}
