package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

// HAIdentity links an entity to stable identities from HA's device registry.
// Connections are MACs; identifiers retain their integration namespace.
// https://developers.home-assistant.io/docs/device_registry_index/
type HAIdentity struct {
	EntityID string
	Leviton  bool
	DeviceID string
	Name     string
	Keys     []string
}

type registryDevice struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	NameByUser   string     `json:"name_by_user"`
	Manufacturer string     `json:"manufacturer"`
	Connections  [][]string `json:"connections"`
	Identifiers  [][]string `json:"identifiers"`
	SerialNumber string     `json:"serial_number"`
}

func (d *Discoverer) fetchRegistry(ctx context.Context, states []hasync.EntityState) ([]HAIdentity, error) {
	ws := hasync.NewWSClient(d.options.HAUrl, d.options.HAToken)
	if err := ws.ConnectContext(ctx); err != nil {
		return nil, err
	}
	defer ws.Close()
	raw, err := ws.SendCommandContext(ctx, "config/device_registry/list", nil)
	if err != nil {
		return nil, err
	}
	var devices []registryDevice
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, fmt.Errorf("decode device registry: %w", err)
	}
	if devices == nil {
		return nil, fmt.Errorf("device registry response must be an array")
	}
	ws.SetContext(ctx)
	entries, err := ws.FetchEntityRegistryWS()
	if err != nil {
		return nil, err
	}
	return registryIdentities(devices, entries, states), nil
}

func registryIdentities(devices []registryDevice, entries []hasync.EntityRegistryEntry, states []hasync.EntityState) []HAIdentity {
	names := map[string]string{}
	for _, state := range states {
		names[state.EntityID], _ = state.Attributes["friendly_name"].(string)
	}
	byDevice := map[string][]hasync.EntityRegistryEntry{}
	for _, entry := range entries {
		byDevice[entry.DeviceID] = append(byDevice[entry.DeviceID], entry)
	}
	var identities []HAIdentity
	for _, device := range devices {
		var keys []string
		for _, connection := range device.Connections {
			if len(connection) == 2 && connection[0] == "mac" {
				if mac := normalizeMAC(connection[1]); len(mac) == 12 {
					keys = append(keys, "mac:"+mac)
				}
			}
		}
		for _, id := range device.Identifiers {
			if len(id) != 2 {
				continue
			}
			// HomeKit accessory IDs may be randomized MAC-like addresses. Keep them
			// separate from network MACs so unrelated namespaces cannot collide.
			if id[0] == "homekit_controller" {
				keys = append(keys, "homekit:"+normalizeID(id[1]))
			}
			if id[0] == "leviton" || id[0] == "leviton_decora_smart_wifi" {
				keys = append(keys, "serial:"+normalizeID(id[1]))
			}
		}
		if device.SerialNumber != "" {
			keys = append(keys, "serial:"+normalizeID(device.SerialNumber))
		}
		name := device.NameByUser
		if name == "" {
			name = device.Name
		}
		linked := byDevice[device.ID]
		if len(linked) == 0 && strings.Contains(strings.ToLower(device.Manufacturer), "leviton") {
			identities = append(identities, HAIdentity{DeviceID: device.ID, Name: name, Keys: keys})
		}
		for _, entry := range linked {
			entityKeys := append([]string(nil), keys...)
			if entry.Platform == "homekit_controller" {
				id, _, _ := strings.Cut(entry.UniqueID, "_")
				if id != "" {
					entityKeys = append(entityKeys, "homekit:"+normalizeID(id))
				}
			}
			// Keep registry identities for all manufacturers for exact matches, but
			// the inventory only lists known Leviton devices or legacy entity names.
			if !strings.Contains(strings.ToLower(device.Manufacturer), "leviton") && !legacyEntity(entry.EntityID) && len(entityKeys) == 0 {
				continue
			}
			entityName := names[entry.EntityID]
			if entityName == "" {
				entityName = name
			}
			identities = append(identities, HAIdentity{Leviton: strings.Contains(strings.ToLower(device.Manufacturer), "leviton") || legacyEntity(entry.EntityID), EntityID: entry.EntityID, DeviceID: device.ID, Name: entityName, Keys: entityKeys})
		}
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].EntityID < identities[j].EntityID })
	return identities
}

func normalizeID(value string) string {
	return strings.NewReplacer(":", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}
func normalizeMAC(value string) string {
	value = normalizeID(value)
	if !macRegex.MatchString(value) {
		return ""
	}
	return value
}
func legacyEntity(entity string) bool { return strings.HasPrefix(extractEntityName(entity), "levds_") }

func discoveredKeys(device *DiscoveredDevice) []string {
	var keys []string
	mac := normalizeMAC(device.MAC)
	// ExtractMAC also exposes serial and accessory IDs for legacy matching;
	// those values do not establish a network-MAC identity.
	accessoryID := device.Protocol == "homekit" && normalizeMAC(device.Properties["id"]) == mac
	serialID := normalizeMAC(device.Properties["serialNumber"]) == mac || normalizeMAC(device.Properties["sn"]) == mac
	if len(mac) == 12 && !accessoryID && !serialID {
		keys = append(keys, "mac:"+mac)
	}
	if device.Protocol == "homekit" && device.Properties["id"] != "" {
		keys = append(keys, "homekit:"+normalizeID(device.Properties["id"]))
	}
	for _, field := range []string{"serialNumber", "sn"} {
		if device.Properties[field] != "" {
			keys = append(keys, "serial:"+normalizeID(device.Properties[field]))
		}
	}
	return keys
}
