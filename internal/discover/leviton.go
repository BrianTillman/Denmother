package discover

import (
	"regexp"
	"strings"
)

// DiscoveredDevice represents a device found via mDNS
type DiscoveredDevice struct {
	Name         string            `json:"name"`
	Hostname     string            `json:"hostname"`
	Addresses    []string          `json:"addresses"`
	Port         int               `json:"port"`
	Properties   map[string]string `json:"properties"`
	ServiceType  string            `json:"service_type"`
	Protocol     string            `json:"protocol"` // "homekit" or "matter"
	MAC          string            `json:"mac,omitempty"`
	Manufacturer string            `json:"manufacturer"`
}

// Leviton identification constants from CSA certification:
// https://csa-iot.org/csa_product/d23lp/
const (
	LevitonMatterVendorIDHex     = "0x109b"
	LevitonMatterVendorIDDecimal = "4251"
)

var macRegex = regexp.MustCompile(`^[0-9a-f]{4,12}$`)

// IsLeviton determines if a discovered device is a Leviton device by checking:
// - Manufacturer string containing "leviton"
// - Hostname starting with "leviton"
// - Device name containing "leviton"
// - Matter vendor ID (0x109b / 4251)
func IsLeviton(device *DiscoveredDevice) bool {
	lowerName := strings.ToLower(device.Name)
	lowerHost := strings.ToLower(device.Hostname)
	lowerManufacturer := strings.ToLower(device.Manufacturer)

	if strings.Contains(lowerManufacturer, "leviton") {
		return true
	}
	if strings.HasPrefix(lowerHost, "leviton") {
		return true
	}
	if strings.Contains(lowerName, "leviton") {
		return true
	}

	// Matter device types are shared across manufacturers; require a vendor ID.
	if device.Protocol == "matter" {
		vid := strings.ToLower(device.Properties["vid"])
		vendorID := device.Properties["vendorid"]
		vpVendor, _, _ := strings.Cut(device.Properties["VP"], "+")

		if vid == LevitonMatterVendorIDHex || vid == LevitonMatterVendorIDDecimal || vendorID == LevitonMatterVendorIDDecimal || vpVendor == LevitonMatterVendorIDDecimal {
			return true
		}
	}

	return false
}

// ExtractMAC returns a normalized hexadecimal identifier from device properties
// or the hostname. It may be a serial number, accessory ID, or partial MAC.
func ExtractMAC(properties map[string]string, hostname string) string {
	candidates := []string{
		strings.ToLower(properties["serialNumber"]),
		strings.ToLower(properties["sn"]),
		strings.ToLower(properties["id"]),
	}

	// Try the last 12 and 8 characters of the first hostname label.
	if hostname != "" {
		parts := strings.Split(hostname, ".")
		if len(parts) > 0 {
			hostPart := strings.ToLower(parts[0])
			if len(hostPart) >= 12 {
				candidates = append(candidates, hostPart[len(hostPart)-12:])
			}
			if len(hostPart) >= 8 {
				candidates = append(candidates, hostPart[len(hostPart)-8:])
			}
		}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		cleaned := strings.ReplaceAll(candidate, ":", "")
		cleaned = strings.ReplaceAll(cleaned, "-", "")
		cleaned = strings.ReplaceAll(cleaned, "_", "")

		if macRegex.MatchString(cleaned) {
			return cleaned
		}
	}

	return ""
}

// DetermineProtocol determines the protocol based on service type
func DetermineProtocol(serviceType string) string {
	if strings.Contains(serviceType, "_matter") {
		return "matter"
	}
	return "homekit"
}
