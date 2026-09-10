package haverify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hasync"
	"gopkg.in/yaml.v3"
)

// StaleRestoredAutomation identifies a restored, unavailable automation state
// whose automation ID no longer exists in local YAML.
type StaleRestoredAutomation struct {
	EntityID string `json:"entity_id"`
	UniqueID string `json:"unique_id"`
}

// FindStaleRestoredAutomations returns only REST states that satisfy all stale
// signals. The result is diagnostic; it does not authorize deleting entries.
func FindStaleRestoredAutomations(states []hasync.EntityState, localIDs map[string]struct{}) []StaleRestoredAutomation {
	var stale []StaleRestoredAutomation
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "automation.") || state.State != "unavailable" {
			continue
		}
		restored, _ := state.Attributes["restored"].(bool)
		if !restored {
			continue
		}
		uniqueID, _ := state.Attributes["id"].(string)
		uniqueID = strings.TrimSpace(uniqueID)
		if uniqueID == "" {
			continue
		}
		if _, exists := localIDs[uniqueID]; exists {
			continue
		}
		stale = append(stale, StaleRestoredAutomation{
			EntityID: state.EntityID,
			UniqueID: uniqueID,
		})
	}

	sort.Slice(stale, func(i, j int) bool {
		if stale[i].EntityID == stale[j].EntityID {
			return stale[i].UniqueID < stale[j].UniqueID
		}
		return stale[i].EntityID < stale[j].EntityID
	})
	return stale
}

// LoadAutomationIDs reads top-level automation IDs from YAML files under root.
func LoadAutomationIDs(root string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if len(document.Content) == 0 || document.Content[0].Kind != yaml.SequenceNode {
			return nil
		}
		for _, automation := range document.Content[0].Content {
			if automation.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(automation.Content); i += 2 {
				if automation.Content[i].Value != "id" {
					continue
				}
				id := strings.TrimSpace(automation.Content[i+1].Value)
				if id != "" {
					ids[id] = struct{}{}
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load automation IDs from %s: %w", root, err)
	}
	return ids, nil
}
