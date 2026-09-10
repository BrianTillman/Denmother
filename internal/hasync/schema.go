package hasync

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/project"
	"github.com/BrianTillman/Denmother/internal/util"
)

// EntitySchema represents the JSON Schema structure for entity autocomplete
type EntitySchema struct {
	Schema      string                 `json:"$schema"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Definitions map[string]interface{} `json:"definitions"`
}

// GenerateEntitySchema reads entity-list.txt and generates a JSON Schema for VSCode autocomplete
func GenerateEntitySchema(configPath string) (string, error) {
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config path: %w", err)
	}

	settings, err := project.ForConfig(absConfigPath)
	if err != nil {
		return "", err
	}
	projectRoot := settings.Root
	entityListPath := filepath.Join(settings.ReferencesDir, "entity-list.txt")
	outputPath := filepath.Join(projectRoot, ".vscode", "ha-entities.schema.json")

	entities, err := readEntityList(entityListPath)
	if err != nil {
		return "", fmt.Errorf("failed to read entity list: %w", err)
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no entities found in %s", entityListPath)
	}

	schema := EntitySchema{
		Schema:      "http://json-schema.org/draft-07/schema#",
		Title:       "Home Assistant Entity Schema",
		Description: fmt.Sprintf("Auto-generated schema for %d Home Assistant entities. Provides autocomplete in VSCode.", len(entities)),
		Definitions: map[string]interface{}{
			"entity_id": map[string]interface{}{
				"type":        "string",
				"enum":        entities,
				"description": "Home Assistant entity ID",
			},
		},
	}

	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(schema); err != nil {
		return "", err
	}
	if err := util.WriteFileAtomic(outputPath, buffer.Bytes(), 0644); err != nil {
		return "", err
	}

	return outputPath, nil
}

// readEntityList reads entity IDs from the entity-list.txt file
func readEntityList(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entities []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entities = append(entities, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entities, nil
}
