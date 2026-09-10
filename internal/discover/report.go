package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LevitonReport represents the discovery report output
type LevitonReport struct {
	RegistryAvailable     bool                `json:"registry_available"`
	Warnings              []string            `json:"warnings,omitempty"`
	Matches               []DeviceMatch       `json:"matches"`
	Timestamp             string              `json:"timestamp"`
	HALevitonEntities     []string            `json:"ha_leviton_entities"`
	DiscoveredDevices     []*DiscoveredDevice `json:"discovered_devices"`
	NewDevices            []*DiscoveredDevice `json:"new_devices"`
	InconsistentNames     []NameInconsistency `json:"inconsistent_names"`
	MatterLightsToExclude []string            `json:"matter_lights_to_exclude"`
}

// NameInconsistency represents a naming mismatch between HA and discovered device
type NameInconsistency struct {
	Device     *DiscoveredDevice `json:"device"`
	HAEntity   string            `json:"ha_entity"`
	HAName     string            `json:"ha_name"`
	DeviceName string            `json:"dev_name"`
}

// DeviceMatch links a discovered device to HA entities and records the matching method.
type DeviceMatch struct {
	Device      *DiscoveredDevice `json:"device"`
	HAEntities  []string          `json:"ha_entities"`
	HADeviceIDs []string          `json:"ha_device_ids,omitempty"`
	Method      string            `json:"method"`
}

// CompareResult contains matched devices, unmatched devices, and naming differences.
type CompareResult struct {
	Matches               []DeviceMatch
	NewDevices            []*DiscoveredDevice
	InconsistentNames     []NameInconsistency
	MatterLightsToExclude []string
}

// CompareWithHA compares discovered devices against Home Assistant entities
func CompareWithHA(haEntities []string, discovered []*DiscoveredDevice) *CompareResult {
	return CompareWithRegistry(haEntities, nil, discovered)
}

// CompareWithRegistry prefers exact registry identities. Legacy MAC suffix
// matching is only accepted when it identifies one entity unambiguously.
func CompareWithRegistry(haEntities []string, identities []HAIdentity, discovered []*DiscoveredDevice) *CompareResult {
	result := &CompareResult{Matches: []DeviceMatch{}, NewDevices: []*DiscoveredDevice{}, InconsistentNames: []NameInconsistency{}, MatterLightsToExclude: []string{}}
	for _, dev := range discovered {
		var matched []HAIdentity
		keys := discoveredKeys(dev)
		for _, identity := range identities {
			for _, key := range keys {
				if containsString(identity.Keys, key) {
					matched = append(matched, identity)
					break
				}
			}
		}
		method := "registry"
		if len(matched) == 0 {
			method = "legacy_mac_suffix"
			mac := normalizeMAC(dev.MAC)
			if mac != "" {
				for _, entity := range haEntities {
					if !legacyEntity(entity) {
						continue
					}
					parts := strings.Split(entity, "_")
					suffix := normalizeMAC(parts[len(parts)-1])
					if suffix != "" && (strings.HasSuffix(mac, suffix) || strings.HasSuffix(suffix, mac)) {
						matched = append(matched, HAIdentity{EntityID: entity, Name: extractEntityName(entity)})
					}
				}
				if len(matched) > 1 {
					matched = nil
				}
			}
		}
		if len(matched) == 0 {
			result.NewDevices = append(result.NewDevices, dev)
			continue
		}
		match := DeviceMatch{Device: dev, Method: method, HAEntities: []string{}}
		for _, identity := range matched {
			if identity.DeviceID != "" && !containsString(match.HADeviceIDs, identity.DeviceID) {
				match.HADeviceIDs = append(match.HADeviceIDs, identity.DeviceID)
			}
			if identity.EntityID == "" || containsString(match.HAEntities, identity.EntityID) {
				continue
			}
			match.HAEntities = append(match.HAEntities, identity.EntityID)
			devName := extractDeviceName(dev.Name)
			if identity.Name != "" && !namesMatch(identity.Name, devName) {
				result.InconsistentNames = append(result.InconsistentNames, NameInconsistency{Device: dev, HAEntity: identity.EntityID, HAName: identity.Name, DeviceName: devName})
			}
			if dev.Protocol == "matter" && strings.HasPrefix(identity.EntityID, "light.") && !containsString(result.MatterLightsToExclude, identity.EntityID) {
				result.MatterLightsToExclude = append(result.MatterLightsToExclude, identity.EntityID)
			}
		}
		sort.Strings(match.HAEntities)
		sort.Strings(match.HADeviceIDs)
		result.Matches = append(result.Matches, match)
	}
	return result
}

// extractEntityName extracts the name portion from an entity ID
func extractEntityName(entityID string) string {
	parts := strings.Split(entityID, ".")
	if len(parts) >= 2 {
		return parts[1]
	}
	return entityID
}

// extractDeviceName extracts a clean name from a discovered device name
func extractDeviceName(name string) string {
	parts := strings.Split(name, ".")
	return parts[0]
}

// namesMatch checks if two names are similar enough to be considered matching
func namesMatch(haName, devName string) bool {
	haLower := strings.ToLower(haName)
	devLower := strings.ToLower(devName)

	if strings.Contains(devLower, haLower) || strings.Contains(haLower, devLower) {
		return true
	}

	haNormalized := strings.ReplaceAll(haLower, "_", " ")
	devNormalized := strings.ReplaceAll(devLower, "_", " ")

	return strings.Contains(devNormalized, haNormalized) || strings.Contains(haNormalized, devNormalized)
}

// GenerateReport creates a LevitonReport from the discovery results
func GenerateReport(haEntities []string, discovered []*DiscoveredDevice, comparison *CompareResult) *LevitonReport {
	return &LevitonReport{
		Matches:               comparison.Matches,
		Timestamp:             time.Now().Format(time.RFC3339),
		HALevitonEntities:     haEntities,
		DiscoveredDevices:     discovered,
		NewDevices:            comparison.NewDevices,
		InconsistentNames:     comparison.InconsistentNames,
		MatterLightsToExclude: comparison.MatterLightsToExclude,
	}
}

// SaveReport saves the report to a JSON file
func SaveReport(report *LevitonReport, outputPath string) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}

// FormatConsoleSummary returns a formatted string for console output
func FormatConsoleSummary(report *LevitonReport) string {
	var sb strings.Builder

	sb.WriteString("\n=== LEVITON DISCOVERY REPORT ===\n")
	fmt.Fprintf(&sb, "Timestamp: %s\n", report.Timestamp)
	for _, warning := range report.Warnings {
		fmt.Fprintf(&sb, "Warning: %s\n", warning)
	}
	fmt.Fprintf(&sb, "HA Leviton Entities: %d\n", len(report.HALevitonEntities))
	fmt.Fprintf(&sb, "Discovered: %d\n", len(report.DiscoveredDevices))
	fmt.Fprintf(&sb, "New/Unconfigured: %d\n", len(report.NewDevices))
	fmt.Fprintf(&sb, "Naming Inconsistent: %d\n", len(report.InconsistentNames))

	if len(report.NewDevices) > 0 {
		sb.WriteString("\nNEW DEVICES:\n")
		for _, d := range report.NewDevices {
			addr := "N/A"
			if len(d.Addresses) > 0 {
				addr = d.Addresses[0]
			}
			fmt.Fprintf(&sb, "  - %s (%s) MAC:%s IP:%s\n", d.Name, d.Hostname, d.MAC, addr)
		}
	}

	if len(report.InconsistentNames) > 0 {
		sb.WriteString("\nINCONSISTENT NAMES:\n")
		for _, inc := range report.InconsistentNames {
			fmt.Fprintf(&sb, "  - HA: %s vs Device: %s\n", inc.HAEntity, inc.DeviceName)
		}
	}

	if len(report.MatterLightsToExclude) > 0 {
		sb.WriteString("\nMATTER LIGHTS TO EXCLUDE:\n")
		for _, entity := range report.MatterLightsToExclude {
			fmt.Fprintf(&sb, "  - %s\n", entity)
		}
	}

	return sb.String()
}
