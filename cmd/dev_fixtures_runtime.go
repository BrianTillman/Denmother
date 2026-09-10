package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/project"
)

// Fixtures fill absent display states, preserving entities loaded by YAML.
// State records do not implement integration services.
func applyPortableDevFixtures(ctx context.Context, env *devEnvironment) error {
	settings, err := project.ForConfig(env.ConfigRoot)
	if err != nil {
		return err
	}
	if settings.DevComposeTemplate != "" {
		return nil
	}
	if settings.DevFixturesExplicit {
		if _, err := os.Stat(settings.DevFixtures); err != nil {
			return fmt.Errorf("configured development fixtures %s: %w", settings.DevFixtures, err)
		}
	}
	states, err := loadDevFixtureStates(settings.DevFixtures)
	if err != nil {
		return err
	}
	if len(states) == 0 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(env.ComposeFile), "token.env"))
	if err != nil {
		return fmt.Errorf("read local fixture credentials: %w", err)
	}
	token := strings.TrimSpace(strings.TrimPrefix(string(data), "HASS_BEARER_TOKEN="))
	if token == "" {
		return fmt.Errorf("local fixture credentials are empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return seedMissingDevStates(ctx, &haconfig.HAConfig{URL: env.HAURL, Token: token}, states)
}

func loadDevFixtureStates(path string) (map[string]devScenarioState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read development fixtures %s: %w", path, err)
	}
	document, err := decodeDevFixtureDocument(data)
	if err != nil {
		return nil, fmt.Errorf("development fixtures %s: %w", path, err)
	}
	return document.Entities, nil
}

func seedMissingDevStates(ctx context.Context, config *haconfig.HAConfig, fixtures map[string]devScenarioState) error {
	states, err := hasync.NewClient(config.URL, config.Token).WithContext(ctx).FetchEntities()
	if err != nil {
		return fmt.Errorf("inspect existing development states: %w", err)
	}
	exists := make(map[string]bool, len(states))
	for _, state := range states {
		exists[state.EntityID] = true
	}
	missing := make([]string, 0, len(fixtures))
	for entityID := range fixtures {
		if !exists[entityID] {
			missing = append(missing, entityID)
		}
	}
	sort.Strings(missing)
	for _, entityID := range missing {
		if err := postHAJSONContext(ctx, config, "/api/states/"+entityID, markDevDisplayState(fixtures[entityID], "fixture")); err != nil {
			return fmt.Errorf("seed development fixture %s: %w", entityID, err)
		}
	}
	return nil
}

func markDevDisplayState(state devScenarioState, source string) devScenarioState {
	attributes := make(map[string]any, len(state.Attributes)+1)
	for key, value := range state.Attributes {
		attributes[key] = value
	}
	attributes["denmother_source"] = source
	state.Attributes = attributes
	return state
}
