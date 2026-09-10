// Package haverify provides device parameter verification by comparing
// expected values (derived from blueprint + automation YAML) against
// live entity states in Home Assistant.
package haverify

import "fmt"

// ParamExpectation represents an expected parameter value derived from
// the blueprint + automation input merge.
type ParamExpectation struct {
	MQTTParam string // e.g., "minimumLevel"
	Value     string // resolved value as string
	EntityID  string // explicit HA entity ID for non-MQTT-backed checks
}

// ParamCheck holds the result of comparing one parameter's expected vs actual value.
type ParamCheck struct {
	MQTTParam     string
	EntityID      string // HA entity ID (e.g., "number.example_dimmer_minimumlevel")
	Expected      string
	Actual        string
	Options       []string // legal select options reported by HA, when available
	Pass          bool
	NotFound      bool // entity not found in HA
	Unavailable   bool // entity exists but HA reports unavailable or unknown
	InvalidOption bool // select entity does not offer the expected option
}

// DeviceResult holds all parameter checks for one config automation / device.
type DeviceResult struct {
	Name       string       // automation alias
	SourceFile string       // config automation YAML path
	Z2MName    string       // Z2M friendly name
	Params     []ParamCheck // per-parameter results
	Passed     int
	Failed     int
	NotFound   int
	Skipped    bool // true if this automation is not supported by verifier
	SkipReason string
}

// CompareParam checks whether an expected value matches an actual entity state.
// For numeric params, it compares as float64 to handle "255" vs "255.0".
func CompareParam(expected, actual string) bool {
	if expected == actual {
		return true
	}
	// Numeric comparison: HA may return "255.0" for param value 255
	ef, eOK := parseFloat(expected)
	af, aOK := parseFloat(actual)
	if eOK && aOK {
		return ef == af
	}
	return false
}

func parseFloat(s string) (float64, bool) {
	var f float64
	n, err := fmt.Sscanf(s, "%f", &f)
	if err != nil || n != 1 {
		return 0, false
	}
	return f, true
}
