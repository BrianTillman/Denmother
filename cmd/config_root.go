package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/project"

	"github.com/bmatcuk/doublestar/v4"
)

func resolvedConfigRoot() (string, error) {
	if strings.TrimSpace(configPath) == "" {
		return "", fmt.Errorf("config path is empty")
	}
	if filepath.IsAbs(configPath) {
		return filepath.Clean(configPath), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	if configPath == "ha-config" && !configExplicit {
		if configured, err := project.DiscoverConfig(cwd); err != nil {
			return "", err
		} else if configured != "" {
			return configured, nil
		}
	}

	dir := cwd
	for {
		candidate := filepath.Clean(filepath.Join(dir, configPath))
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if root, err := repoRoot(); err == nil {
		candidate := filepath.Clean(filepath.Join(root, configPath))
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
	}

	return filepath.Clean(filepath.Join(cwd, configPath)), nil
}

func globUnderConfig(pattern string) ([]string, error) {
	root, err := resolvedConfigRoot()
	if err != nil {
		return nil, err
	}

	matches, err := doublestar.Glob(os.DirFS(root), pattern)
	if err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(matches))
	for _, match := range matches {
		full := filepath.Join(root, filepath.FromSlash(match))
		if rel, err := filepath.Rel(cwd, full); err == nil && isLocalRelPath(rel) {
			out = append(out, filepath.ToSlash(rel))
			continue
		}
		out = append(out, full)
	}
	return out, nil
}

func isLocalRelPath(rel string) bool {
	if rel == "" || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// configFile joins path to the selected HA configuration directory.
func configFile(path string) string {
	root, err := resolvedConfigRoot()
	if err != nil {
		return filepath.Join(configPath, filepath.FromSlash(path))
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func selectedProject() (project.Settings, error) {
	root, err := resolvedConfigRoot()
	if err != nil {
		return project.Settings{}, err
	}
	return project.ForConfig(root)
}

func managedRepository() bool {
	settings, err := selectedProject()
	return err == nil && settings.ManagedRepository
}

func selectedProjectRoot() string {
	settings, err := selectedProject()
	if err != nil {
		return ""
	}
	return settings.Root
}
func resolveProjectLocalConfig() (*haconfig.HAConfig, error) {
	settings, err := selectedProject()
	if err != nil {
		return nil, err
	}
	return haconfig.ResolveConfigLocalConfigContext(commandContext(), settings.ConfigDir)
}

// relativeConfigPath normalizes selected-root files for shared review heuristics.
func relativeConfigPath(path string) string {
	normalized := filepath.ToSlash(filepath.Clean(path))
	literalPrefix := filepath.ToSlash(filepath.Clean(configPath)) + "/"
	if strings.HasPrefix(normalized, literalPrefix) {
		return strings.TrimPrefix(normalized, literalPrefix)
	}
	root, err := resolvedConfigRoot()
	if err != nil {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || !isLocalRelPath(relative) {
		return ""
	}
	return filepath.ToSlash(relative)
}
