package validator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BrianTillman/Denmother/internal/project"
)

// verifySchemaSource requires evidence that Docker is checking the selected
// source. A name or HA_CONTAINER override alone cannot establish that identity.
func verifySchemaSource(config, container string) error {
	return verifySchemaSourceContext(context.Background(), config, container)
}

func verifySchemaSourceContext(ctx context.Context, config, container string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	settings, err := project.ForConfig(config)
	if err != nil {
		return err
	}
	output, err := commandOutput(ctx, "docker", "inspect", "-f", "{{json .Mounts}}", container)
	if err != nil {
		return fmt.Errorf("inspect schema source: %w", err)
	}
	var mounts []struct{ Source, Destination string }
	if err := json.Unmarshal(output, &mounts); err != nil {
		return fmt.Errorf("schema source mounts unavailable")
	}
	expected := settings.ConfigDir
	if hostRoot := os.Getenv("DM_DEV_HOST_REPO_ROOT"); hostRoot != "" {
		rel, err := filepath.Rel(settings.Root, expected)
		if err != nil {
			return err
		}
		expected = filepath.Join(hostRoot, rel)
	}
	matched := false
	direct := false
	for _, mount := range mounts {
		if (mount.Destination == "/ha-config-source" || mount.Destination == "/config") && filepath.Clean(mount.Source) == filepath.Clean(expected) {
			matched = true
			direct = mount.Destination == "/config"
			break
		}
	}
	if !matched {
		return fmt.Errorf("schema container does not mount the selected --config directory")
	}
	if direct || settings.DevComposeTemplate != "" {
		return nil
	}
	output, err = commandOutput(ctx, "docker", "exec", container, "cat", "/config/.denmother-source.json")
	if err != nil {
		return fmt.Errorf("runtime source manifest unavailable; run dm dev up with the same --config: %w", err)
	}
	var loaded map[string]string
	if err := json.Unmarshal(output, &loaded); err != nil {
		return fmt.Errorf("runtime source manifest is malformed")
	}
	current := map[string]string{}
	err = filepath.WalkDir(settings.ConfigDir, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(settings.ConfigDir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "tests" || entry.Name() == "custom_components") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		current[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(loaded, current) {
		return fmt.Errorf("runtime configuration is stale; run dm dev up with the same --config before schema validation")
	}
	return nil
}
