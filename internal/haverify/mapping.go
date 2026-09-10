package haverify

import (
	"fmt"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

// paramDomains is the ordered list of HA entity domains to search for device parameters.
var paramDomains = []string{"number", "select", "sensor"}

// EntityLookupEntry retains state and attributes for parameter checks, including
// the available options reported by select entities.
type EntityLookupEntry struct {
	EntityID   string
	State      string
	Attributes map[string]interface{}
}

// Options returns the Home Assistant select options for an entity, if present.
func (e EntityLookupEntry) Options() []string {
	raw, ok := e.Attributes["options"]
	if !ok || raw == nil {
		return nil
	}

	switch values := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(values))
		for _, v := range values {
			out = append(out, strings.TrimSpace(fmt.Sprintf("%v", v)))
		}
		return out
	case []string:
		out := make([]string, 0, len(values))
		for _, v := range values {
			out = append(out, strings.TrimSpace(v))
		}
		return out
	default:
		return nil
	}
}

type EntityLookup map[string]EntityLookupEntry

// Z2MNameToEntityPrefix converts a Z2M friendly name to an HA entity prefix.
// For example, "Example Dimmer" becomes "example_dimmer".
func Z2MNameToEntityPrefix(z2mName string) string {
	s := strings.ToLower(z2mName)
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// MQTTParamToEntitySuffix converts an MQTT parameter name to an HA entity suffix.
// "ledColorWhenOff" → "ledcolorwhenoff"
func MQTTParamToEntitySuffix(param string) string {
	return strings.ToLower(param)
}

// BuildEntityLookup creates a map from entity_id → state/attribute details for quick lookup.
// Only includes number.*, select.*, and sensor.* entities.
func BuildEntityLookup(entities []hasync.EntityState) EntityLookup {
	lookup := make(EntityLookup)
	for _, e := range entities {
		for _, d := range paramDomains {
			if strings.HasPrefix(e.EntityID, d+".") {
				lookup[e.EntityID] = EntityLookupEntry{
					EntityID:   e.EntityID,
					State:      e.State,
					Attributes: e.Attributes,
				}
				break
			}
		}
	}
	return lookup
}

// FindEntityForParam finds the HA entity ID for a given MQTT parameter and prefix.
// It tries number.*, then select.*, then sensor.* domains.
func FindEntityForParam(lookup EntityLookup, prefix, mqttParam string) (entityID string, state string, found bool) {
	suffix := MQTTParamToEntitySuffix(mqttParam)
	for _, domain := range paramDomains {
		candidate := domain + "." + prefix + "_" + suffix
		if entry, ok := lookup[candidate]; ok {
			return candidate, entry.State, true
		}
	}
	return "", "", false
}

// FindEntityForExpectation finds the HA entity for an expectation. Matter-backed
// config checks carry an explicit EntityID; Z2M checks still derive the entity
// from the MQTT parameter name and device prefix.
func FindEntityForExpectation(lookup EntityLookup, prefix string, exp ParamExpectation) (entityID string, state string, found bool) {
	if strings.TrimSpace(exp.EntityID) != "" {
		entry, found := lookup[exp.EntityID]
		return exp.EntityID, entry.State, found
	}
	return FindEntityForParam(lookup, prefix, exp.MQTTParam)
}

// EvaluateExpectation compares one expected parameter value with its HA state.
// It checks for missing entities, unavailable states, and invalid select options
// before comparing values.
func EvaluateExpectation(lookup EntityLookup, prefix string, exp ParamExpectation) ParamCheck {
	entityID, actual, found := FindEntityForExpectation(lookup, prefix, exp)
	check := ParamCheck{
		MQTTParam: exp.MQTTParam,
		EntityID:  entityID,
		Expected:  exp.Value,
		Actual:    actual,
	}

	if !found {
		check.NotFound = true
		return check
	}

	if actual == "unavailable" || actual == "unknown" {
		check.Unavailable = true
		return check
	}

	if strings.HasPrefix(entityID, "select.") {
		if entry, ok := lookup[entityID]; ok {
			options := entry.Options()
			check.Options = options
			if len(options) > 0 && !containsString(options, exp.Value) {
				check.InvalidOption = true
				return check
			}
		}
	}

	check.Pass = CompareParam(exp.Value, actual)
	return check
}

func containsString(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, v := range values {
		if strings.TrimSpace(v) == needle {
			return true
		}
	}
	return false
}
