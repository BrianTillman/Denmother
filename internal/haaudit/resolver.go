package haaudit

import (
	"fmt"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

// AutomationIDResolver maps automation entity_ids to their config ids
// (the item_id expected by HA's trace/list and trace/get WebSocket APIs).
// Entity suffixes can differ from config IDs, including timestamp-based IDs.
type AutomationIDResolver struct {
	entityToConfigID map[string]string
}

// NewAutomationIDResolver fetches the entity registry via WebSocket and builds
// an entity_id → unique_id mapping for automation entities.
func NewAutomationIDResolver(ws *hasync.WSClient) (*AutomationIDResolver, error) {
	entries, err := ws.FetchEntityRegistryWS()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch entity registry: %w", err)
	}

	m := make(map[string]string)
	for _, e := range entries {
		if strings.HasPrefix(e.EntityID, "automation.") && e.UniqueID != "" {
			m[e.EntityID] = e.UniqueID
		}
	}

	return &AutomationIDResolver{entityToConfigID: m}, nil
}

// Resolve returns the config id (item_id) for an automation entity_id.
// Falls back to the entity suffix after the first dot if no registry entry is found.
func (r *AutomationIDResolver) Resolve(entityID string) string {
	if r != nil {
		if configID, ok := r.entityToConfigID[entityID]; ok {
			return configID
		}
	}
	return AutomationItemID(entityID)
}
