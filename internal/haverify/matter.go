package haverify

import (
	"fmt"
	"strings"
)

type matterEntityExpectation struct {
	EntityInput string
	ValueInput  string
	ParamName   string
}

var matterEntityExpectations = []matterEntityExpectation{
	{EntityInput: "button_delay_entity", ValueInput: "button_delay", ParamName: "buttonDelay"},
	{EntityInput: "switch_type_entity", ValueInput: "switch_type", ParamName: "switchType"},
	{EntityInput: "smart_bulb_mode_entity", ValueInput: "smart_bulb_mode", ParamName: "smartBulbMode"},
	{EntityInput: "control_of_switch_load_entity", ValueInput: "control_of_switch_load", ParamName: "controlOfSwitchLoad"},
	{EntityInput: "audible_click_entity", ValueInput: "audible_click", ParamName: "audibleClick"},
	{EntityInput: "dimming_edge_entity", ValueInput: "dimming_edge", ParamName: "dimmingEdge"},
	{EntityInput: "relay_entity", ValueInput: "relay", ParamName: "relay"},
	{EntityInput: "led_color_entity", ValueInput: "led_color", ParamName: "ledColor"},
	{EntityInput: "led_effect_entity", ValueInput: "led_effect", ParamName: "ledEffect"},
	{EntityInput: "led_intensity_on_entity", ValueInput: "led_intensity_on", ParamName: "ledIntensityOn"},
	{EntityInput: "led_intensity_off_entity", ValueInput: "led_intensity_off", ParamName: "ledIntensityOff"},
	{EntityInput: "led_on_intensity_number_entity", ValueInput: "led_on_intensity_number", ParamName: "ledOnIntensityNumber"},
	{EntityInput: "led_off_intensity_number_entity", ValueInput: "led_off_intensity_number", ParamName: "ledOffIntensityNumber"},
	{EntityInput: "on_level_entity", ValueInput: "on_level", ParamName: "onLevel"},
	{EntityInput: "on_transition_time_entity", ValueInput: "transition_time", ParamName: "onTransitionTime"},
	{EntityInput: "off_transition_time_entity", ValueInput: "transition_time", ParamName: "offTransitionTime"},
	{EntityInput: "on_off_transition_time_entity", ValueInput: "transition_time", ParamName: "onOffTransitionTime"},
}

var matterFanCanopyEntityExpectations = []matterEntityExpectation{
	{EntityInput: "light_mode_entity", ValueInput: "light_mode", ParamName: "lightMode"},
	{EntityInput: "fan_mode_entity", ValueInput: "fan_mode", ParamName: "fanMode"},
	{EntityInput: "light_on_level_entity", ValueInput: "light_on_level", ParamName: "lightOnLevel"},
	{EntityInput: "light_power_on_level_entity", ValueInput: "light_power_on_level", ParamName: "lightPowerOnLevel"},
	{EntityInput: "light_on_transition_time_entity", ValueInput: "light_transition_time", ParamName: "lightOnTransitionTime"},
	{EntityInput: "light_off_transition_time_entity", ValueInput: "light_transition_time", ParamName: "lightOffTransitionTime"},
	{EntityInput: "light_on_off_transition_time_entity", ValueInput: "light_transition_time", ParamName: "lightOnOffTransitionTime"},
	{EntityInput: "light_power_on_behavior_entity", ValueInput: "light_power_on_behavior", ParamName: "lightPowerOnBehavior"},
	{EntityInput: "fan_on_level_entity", ValueInput: "fan_on_level", ParamName: "fanOnLevel"},
	{EntityInput: "fan_power_on_level_entity", ValueInput: "fan_power_on_level", ParamName: "fanPowerOnLevel"},
	{EntityInput: "fan_on_off_transition_time_entity", ValueInput: "fan_transition_time", ParamName: "fanOnOffTransitionTime"},
	{EntityInput: "fan_power_on_behavior_entity", ValueInput: "fan_power_on_behavior", ParamName: "fanPowerOnBehavior"},
	{EntityInput: "led_on_intensity_entity", ValueInput: "led_on_intensity", ParamName: "ledOnIntensity"},
}

// ResolveMatterEntityExpectations resolves explicit HA entity checks from the
// Matter Inovelli settings blueprint inputs. Blank optional entity inputs are
// skipped.
func ResolveMatterEntityExpectations(inputs map[string]interface{}) []ParamExpectation {
	return resolveMatterEntityExpectations(inputs, matterEntityExpectations)
}

// ResolveMatterFanCanopyEntityExpectations resolves explicit HA entity checks
// from the Matter Inovelli LightFan module settings blueprint inputs.
func ResolveMatterFanCanopyEntityExpectations(inputs map[string]interface{}) []ParamExpectation {
	return resolveMatterEntityExpectations(inputs, matterFanCanopyEntityExpectations)
}

func resolveMatterEntityExpectations(inputs map[string]interface{}, definitions []matterEntityExpectation) []ParamExpectation {
	expectations := make([]ParamExpectation, 0, len(definitions))

	for _, def := range definitions {
		entityID := strings.TrimSpace(fmt.Sprintf("%v", inputs[def.EntityInput]))
		if entityID == "" {
			continue
		}

		value, ok := inputs[def.ValueInput]
		if !ok {
			continue
		}

		expectations = append(expectations, ParamExpectation{
			MQTTParam: def.ParamName,
			EntityID:  entityID,
			Value:     fmt.Sprintf("%v", value),
		})
	}

	return expectations
}
