package haverify

import (
	"fmt"
	"strings"
)

const (
	declarativeCommonSettings = "common_settings"
	declarativeCanopySettings = "canopy_settings"
)

// ResolveDeclarativeMQTTSettings resolves the settings lists used by the
// Inovelli Blue Series dimmer and fan-canopy blueprints. These lists also
// supply Home Assistant's repeat actions.
func ResolveDeclarativeMQTTSettings(actions []interface{}, inputs map[string]interface{}) ([]ParamExpectation, bool, error) {
	settings, ok := findDeclarativeSettings(actions)
	if !ok {
		return nil, false, nil
	}

	model := ""
	var lists []string
	if _, canopy := settings[declarativeCanopySettings]; canopy {
		if err := validateCanopyInputs(inputs); err != nil {
			return nil, true, err
		}
		lists = []string{declarativeCanopySettings}
	} else {
		model = inputString(inputs, "device_model")
		if model != "VZM31-SN" && model != "VZM32-SN" {
			return nil, true, fmt.Errorf("unsupported Inovelli Blue Series device_model %q", model)
		}
		if err := validateDeclarativeInputs(model, inputs); err != nil {
			return nil, true, err
		}

		lists = []string{declarativeCommonSettings}
		if model == "VZM31-SN" {
			lists = append(lists, "vzm31_settings")
		} else {
			lists = append(lists, "vzm32_settings")
			if inputString(inputs, "mmwave_room_size_preset") == "Custom" {
				lists = append(lists, "vzm32_custom_geometry_settings")
			}
		}
	}

	var expectations []ParamExpectation
	for _, listName := range lists {
		rawList, exists := settings[listName]
		if !exists {
			return nil, true, fmt.Errorf("declarative settings list %q is missing", listName)
		}
		list, ok := rawList.([]interface{})
		if !ok {
			return nil, true, fmt.Errorf("declarative settings %q must be a list", listName)
		}
		for index, rawItem := range list {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				return nil, true, fmt.Errorf("declarative setting %s[%d] must be a map", listName, index)
			}
			parameter := strings.TrimSpace(fmt.Sprintf("%v", item["parameter"]))
			if parameter == "" || parameter == "<nil>" {
				return nil, true, fmt.Errorf("declarative setting %s[%d] has no parameter", listName, index)
			}
			value, err := resolveDeclarativeValue(parameter, item["value"], model, inputs)
			if err != nil {
				return nil, true, fmt.Errorf("resolve %s: %w", parameter, err)
			}
			expectations = append(expectations, ParamExpectation{MQTTParam: parameter, Value: value})
		}
	}

	return expectations, true, nil
}

func findDeclarativeSettings(actions []interface{}) (map[string]interface{}, bool) {
	var found map[string]interface{}
	walkActionMaps(actions, func(action map[string]interface{}) {
		if found != nil {
			return
		}
		variables, ok := action["variables"].(map[string]interface{})
		if !ok {
			return
		}
		if _, exists := variables[declarativeCommonSettings]; exists {
			found = variables
		}
		if _, exists := variables[declarativeCanopySettings]; exists {
			found = variables
		}
	})
	return found, found != nil
}

func resolveDeclarativeValue(parameter string, raw interface{}, model string, inputs map[string]interface{}) (string, error) {
	switch parameter {
	case "switchType":
		wiring := inputString(inputs, "switch_type")
		if model == "VZM31-SN" {
			switch wiring {
			case "Aux Switch":
				return "3-Way Aux Switch", nil
			case "Dumb Switch":
				return "3-Way Dumb Switch", nil
			}
		}
		return wiring, nil
	case "loadLevelIndicatorTimeout":
		value := inputString(inputs, "load_level_indicator_timeout")
		if value != "Stay Off" && value != "Stay On" {
			suffix := " Seconds"
			if value == "1" {
				suffix = " Second"
			}
			return value + suffix, nil
		}
		return value, nil
	}

	if valueName, ok := raw.(string); ok {
		valueName = strings.TrimSpace(valueName)
		if inputValue, exists := inputs[valueName]; exists {
			return fmt.Sprintf("%v", inputValue), nil
		}
		if strings.Contains(valueName, "{{") {
			return "", fmt.Errorf("unsupported derived template %q", valueName)
		}
	}
	return fmt.Sprintf("%v", raw), nil
}

func validateDeclarativeInputs(model string, inputs map[string]interface{}) error {
	wiring := inputString(inputs, "switch_type")
	if model == "VZM32-SN" && wiring != "Single Pole" && wiring != "Aux Switch" {
		return fmt.Errorf("VZM32-SN does not support wiring type %q through Zigbee2MQTT", wiring)
	}
	minimum, minOK := numericInput(inputs["minimum_level"])
	maximum, maxOK := numericInput(inputs["maximum_level"])
	if minOK && maxOK && minimum >= maximum {
		return fmt.Errorf("minimum_level (%v) must be less than maximum_level (%v)", minimum, maximum)
	}
	if inputString(inputs, "dimming_mode") == "Trailing edge" {
		if inputString(inputs, "power_type") != "Neutral" {
			return fmt.Errorf("trailing-edge dimming requires neutral power")
		}
		if wiring != "Single Pole" && wiring != "Aux Switch" {
			return fmt.Errorf("trailing-edge dimming requires single-pole or auxiliary-switch wiring")
		}
	}
	if model == "VZM32-SN" && inputString(inputs, "mmwave_room_size_preset") == "Custom" {
		for _, pair := range [][2]string{{"mmwave_height_min", "mmwave_height_max"}, {"mmwave_width_min", "mmwave_width_max"}, {"mmwave_depth_min", "mmwave_depth_max"}} {
			min, minOK := numericInput(inputs[pair[0]])
			max, maxOK := numericInput(inputs[pair[1]])
			if minOK && maxOK && min > max {
				return fmt.Errorf("%s (%v) must not exceed %s (%v)", pair[0], min, pair[1], max)
			}
		}
	}
	return nil
}

func validateCanopyInputs(inputs map[string]interface{}) error {
	for _, pair := range [][2]string{
		{"light_minimum_level", "light_maximum_level"},
		{"fan_minimum_level", "fan_maximum_level"},
	} {
		minimum, minOK := numericInput(inputs[pair[0]])
		maximum, maxOK := numericInput(inputs[pair[1]])
		if minOK && maxOK && minimum >= maximum {
			return fmt.Errorf("%s (%v) must be less than %s (%v)", pair[0], minimum, pair[1], maximum)
		}
	}
	return nil
}

func inputString(inputs map[string]interface{}, name string) string {
	return strings.TrimSpace(fmt.Sprintf("%v", inputs[name]))
}

func numericInput(value interface{}) (float64, bool) {
	return parseFloat(fmt.Sprintf("%v", value))
}
