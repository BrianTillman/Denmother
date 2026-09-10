// Package project contains explicit repository policy and portable filesystem paths.
package project

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const Filename = ".denmother.yaml"

type Settings struct {
	Version               int      `yaml:"version"`
	ConfigDir             string   `yaml:"config_dir"`
	ReferencesDir         string   `yaml:"references_dir"`
	ManagedRepository     bool     `yaml:"managed_repository"`
	FanLightPairBlueprint string   `yaml:"fan_light_pair_blueprint"`
	ForbiddenIntegrations []string `yaml:"forbidden_integrations"`
	DevComposeTemplate    string   `yaml:"dev_compose_template"`
	DevFixtures           string   `yaml:"dev_fixtures"`
	DevScenarios          string   `yaml:"dev_scenarios"`
	DevFixturesExplicit   bool     `yaml:"-"`
	Root                  string   `yaml:"-"`
}

// ForConfig loads policy only when it explicitly belongs to the selected
// configuration. An alternate configuration never inherits a parent's home policy.
func ForConfig(config string) (Settings, error) {
	return ForConfigWithin(config, os.Getenv("DM_MCP_WORKER_ROOT"))
}

// ForConfigWithin restricts policy discovery to an optional configured project.
// Ordinary CLI calls retain their existing parent-discovery behavior.
func ForConfigWithin(config, boundary string) (Settings, error) {
	abs, err := filepath.Abs(config)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{Version: 1, ConfigDir: abs, Root: filepath.Dir(abs)}
	if boundary != "" && filepath.Clean(boundary) == abs {
		settings.Root = abs
	}
	settings.ReferencesDir = filepath.Join(settings.Root, "docs", "reference")
	settings.DevFixtures = filepath.Join(settings.Root, ".devcontainer", "dev-state-fixtures.json")
	settings.DevScenarios = filepath.Join(settings.Root, ".devcontainer", "scenarios.json")
	for _, root := range []string{abs, filepath.Dir(abs)} {
		if boundary != "" {
			rel, err := filepath.Rel(boundary, root)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
		}
		data, err := os.ReadFile(filepath.Join(root, Filename))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Settings{}, err
		}
		var candidate Settings
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&candidate); err != nil {
			return Settings{}, fmt.Errorf("%s: %w", filepath.Join(root, Filename), err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Settings{}, fmt.Errorf("%s: expected exactly one YAML document", Filename)
		}
		if candidate.Version != 1 {
			return Settings{}, fmt.Errorf("%s: unsupported project version %d", Filename, candidate.Version)
		}
		if candidate.ConfigDir == "" {
			candidate.ConfigDir = "ha-config"
		}
		candidate.ConfigDir = absoluteUnder(root, candidate.ConfigDir)
		if filepath.Clean(candidate.ConfigDir) != abs {
			continue
		}
		candidate.Root = root
		if candidate.ReferencesDir == "" {
			candidate.ReferencesDir = "docs/reference"
		}
		candidate.ReferencesDir = absoluteUnder(root, candidate.ReferencesDir)
		candidate.DevFixturesExplicit = candidate.DevFixtures != ""
		if candidate.DevFixtures == "" {
			candidate.DevFixtures = ".devcontainer/dev-state-fixtures.json"
		}
		candidate.DevFixtures = absoluteUnder(root, candidate.DevFixtures)
		if candidate.DevScenarios == "" {
			candidate.DevScenarios = ".devcontainer/scenarios.json"
		}
		candidate.DevScenarios = absoluteUnder(root, candidate.DevScenarios)
		if candidate.DevComposeTemplate != "" {
			candidate.DevComposeTemplate = absoluteUnder(root, candidate.DevComposeTemplate)
		}
		return candidate, nil
	}
	return settings, nil
}

func absoluteUnder(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

// DiscoverConfig resolves a project's configured default from the current directory or ancestors.
func DiscoverConfig(start string) (string, error) {
	for root := start; ; root = filepath.Dir(root) {
		data, err := os.ReadFile(filepath.Join(root, Filename))
		if err == nil {
			var settings Settings
			if err := yaml.Unmarshal(data, &settings); err != nil {
				return "", err
			}
			dir := settings.ConfigDir
			if dir == "" {
				dir = "ha-config"
			}
			return absoluteUnder(root, dir), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	return "", nil
}

// RuntimeKey keeps legacy ha-config identities while isolating sibling alternate roots.
func (s Settings) RuntimeKey() string {
	if filepath.Base(s.ConfigDir) == "ha-config" {
		return s.Root
	}
	return s.ConfigDir
}
