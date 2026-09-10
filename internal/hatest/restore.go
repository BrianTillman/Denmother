package hatest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func directlySeededEntities(tc TestCase) map[string]bool {
	direct := map[string]bool{}
	for _, setup := range tc.Setup {
		if setup.Method == "direct" || setup.Method == "mock" {
			direct[setup.EntityID] = true
		}
	}
	for _, call := range tc.Trigger {
		for _, entity := range serviceCallEntityIDs(call) {
			if call.Service == "mock_entities.set_state" {
				direct[entity] = true
			} else {
				delete(direct, entity)
			}
		}
	}
	return direct
}

// restoreSnapshot restores supported integration state through its services.
// Explicitly direct/mock seeds restore only their HA state-machine records.
func (r *TestRunner) restoreSnapshot(snapshot entitySnapshot) error {
	if snapshot.Direct {
		return r.client.SetEntityStateDirect(snapshot.EntityID, snapshot.State, snapshot.Attributes)
	}
	domain := getDomain(snapshot.EntityID)
	call := func(service string, fields map[string]interface{}) error {
		data := map[string]interface{}{"entity_id": snapshot.EntityID}
		for key, value := range fields {
			data[key] = value
		}
		return r.client.CallService(domain, service, data)
	}
	attrs := snapshot.Attributes
	fields := func(keys ...string) map[string]interface{} {
		values := map[string]interface{}{}
		for _, key := range keys {
			if value, ok := attrs[key]; ok && value != nil {
				values[key] = value
			}
		}
		return values
	}
	onOff := func(on map[string]interface{}) error {
		if snapshot.State == "on" {
			return call("turn_on", on)
		}
		if snapshot.State == "off" {
			return call("turn_off", nil)
		}
		return fmt.Errorf("cannot restore %s state %q through its service", domain, snapshot.State)
	}
	switch domain {
	case "automation", "input_boolean", "switch":
		return onOff(nil)
	case "input_number", "number":
		value, err := strconv.ParseFloat(snapshot.State, 64)
		if err != nil {
			return fmt.Errorf("cannot restore numeric state %q", snapshot.State)
		}
		return call("set_value", map[string]interface{}{"value": value})
	case "counter":
		value, err := strconv.ParseInt(snapshot.State, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot restore counter state %q", snapshot.State)
		}
		return call("set_value", map[string]interface{}{"value": value})
	case "input_text", "text":
		return call("set_value", map[string]interface{}{"value": snapshot.State})
	case "input_select", "select":
		return call("select_option", map[string]interface{}{"option": snapshot.State})
	case "input_datetime":
		key := "datetime"
		if hasDate, ok := attrs["has_date"].(bool); ok && !hasDate {
			key = "time"
		} else if hasTime, ok := attrs["has_time"].(bool); ok && !hasTime {
			key = "date"
		}
		if snapshot.State == "unknown" || snapshot.State == "unavailable" {
			return fmt.Errorf("cannot restore datetime state %q", snapshot.State)
		}
		return call("set_datetime", map[string]interface{}{key: snapshot.State})
	case "light":
		values := fields("brightness", "effect")
		mode, _ := attrs["color_mode"].(string)
		switch mode {
		case "color_temp":
			for key, value := range fields("color_temp_kelvin") {
				values[key] = value
			}
		case "hs", "xy", "rgb", "rgbw", "rgbww":
			for key, value := range fields(mode + "_color") {
				values[key] = value
			}
		}
		return onOff(values)
	case "fan":
		values := fields("percentage")
		if preset, ok := attrs["preset_mode"].(string); ok && preset != "" {
			values = map[string]interface{}{"preset_mode": preset}
		}
		if err := onOff(values); err != nil {
			return err
		}
		if snapshot.State == "off" {
			return nil
		}
		if value, ok := attrs["oscillating"].(bool); ok {
			if err := call("oscillate", map[string]interface{}{"oscillating": value}); err != nil {
				return err
			}
		}
		if value, ok := attrs["direction"].(string); ok && value != "" {
			return call("set_direction", map[string]interface{}{"direction": value})
		}
		return nil
	case "cover":
		if position, ok := attrs["current_position"]; ok && position != nil {
			if err := call("set_cover_position", map[string]interface{}{"position": position}); err != nil {
				return err
			}
		} else {
			service := ""
			switch snapshot.State {
			case "open":
				service = "open_cover"
			case "closed":
				service = "close_cover"
			default:
				return fmt.Errorf("cannot restore moving or unknown cover state %q", snapshot.State)
			}
			if err := call(service, nil); err != nil {
				return err
			}
		}
		if tilt, ok := attrs["current_tilt_position"]; ok && tilt != nil {
			return call("set_cover_tilt_position", map[string]interface{}{"tilt_position": tilt})
		}
		return nil
	case "lock":
		switch snapshot.State {
		case "locked":
			return call("lock", nil)
		case "unlocked":
			return call("unlock", nil)
		}
		return fmt.Errorf("cannot restore transient lock state %q", snapshot.State)
	case "climate":
		if snapshot.State == "unknown" || snapshot.State == "unavailable" {
			return fmt.Errorf("cannot restore climate state %q", snapshot.State)
		}
		if err := call("set_hvac_mode", map[string]interface{}{"hvac_mode": snapshot.State}); err != nil {
			return err
		}
		values := fields("temperature", "target_temp_high", "target_temp_low")
		if len(values) > 0 {
			if err := call("set_temperature", values); err != nil {
				return err
			}
		}
		for _, key := range []string{"preset_mode", "fan_mode", "swing_mode", "swing_horizontal_mode"} {
			if value, ok := attrs[key].(string); ok && value != "" {
				if err := call("set_"+key, map[string]interface{}{key: value}); err != nil {
					return err
				}
			}
		}
		return nil
	case "timer":
		return r.restoreTimer(snapshot)
	case "vacuum", "media_player", "alarm_control_panel", "water_heater", "humidifier", "siren", "script", "scene", "button", "input_button":
		return fmt.Errorf("automatic service restoration is not defined for %s; use explicit cleanup with auto_restore: false", domain)
	default:
		return r.client.SetEntityStateDirect(snapshot.EntityID, snapshot.State, snapshot.Attributes)
	}
}

func (r *TestRunner) restoreTimer(snapshot entitySnapshot) error {
	data := map[string]interface{}{"entity_id": snapshot.EntityID}
	if snapshot.State == "idle" {
		return r.client.CallService("timer", "cancel", data)
	}
	if snapshot.State != "active" && snapshot.State != "paused" {
		return fmt.Errorf("cannot restore timer state %q", snapshot.State)
	}
	remaining, ok := snapshot.Attributes["remaining"].(string)
	if snapshot.State == "active" {
		if finish, exists := snapshot.Attributes["finishes_at"].(string); exists {
			end, err := time.Parse(time.RFC3339Nano, finish)
			if err != nil {
				return fmt.Errorf("invalid timer finish time")
			}
			duration := time.Until(end)
			if duration <= 0 {
				return r.client.CallService("timer", "cancel", data)
			}
			data["duration"] = duration.Seconds()
		} else if ok && strings.TrimSpace(remaining) != "" {
			data["duration"] = remaining
		} else {
			return fmt.Errorf("cannot restore active timer without remaining duration")
		}
	} else {
		if !ok || remaining == "" {
			return fmt.Errorf("cannot restore paused timer without remaining duration")
		}
		data["duration"] = remaining
	}
	if err := r.client.CallService("timer", "start", data); err != nil {
		return err
	}
	if snapshot.State == "paused" {
		return r.client.CallService("timer", "pause", map[string]interface{}{"entity_id": snapshot.EntityID})
	}
	return nil
}

func unsupportedRestoreDomain(domain string) bool {
	switch domain {
	case "vacuum", "media_player", "alarm_control_panel", "water_heater", "humidifier", "siren", "script", "scene", "button", "input_button":
		return true
	default:
		return false
	}
}
