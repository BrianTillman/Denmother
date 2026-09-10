package haverify

import (
	"regexp"
	"strings"
)

// jinja template pattern: {{ var_name }} (with optional whitespace)
var jinjaVarRe = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)

// keyValueRe matches JSON key-value pairs in MQTT payload templates.
var keyValueRe = regexp.MustCompile(`"(\w+)"\s*:\s*("(?:[^"]*)"|\{\{[^}]+\}\}|-?\d+(?:\.\d+)?)`)

// topLevelKeyRe matches the key and value tail of a single-key JSON payload.
var topLevelKeyRe = regexp.MustCompile(`^\s*\{\s*"(\w+)"\s*:\s*(.+)$`)

// PayloadParam represents a single key-value pair extracted from an MQTT payload.
type PayloadParam struct {
	Name      string
	ValueExpr string // raw expression (may contain {{ var }})
}

// ExtractMQTTPayloads extracts parameter expressions from nested mqtt.publish
// actions, excluding /get requests.
func ExtractMQTTPayloads(actions []interface{}) []PayloadParam {
	var params []PayloadParam

	walkActionMaps(actions, func(actionMap map[string]interface{}) {
		actionType, _ := actionMap["action"].(string)
		if actionType != "mqtt.publish" {
			return
		}

		data, ok := actionMap["data"].(map[string]interface{})
		if !ok {
			return
		}

		// GET payloads request current values; they do not declare expected settings.
		if topic, _ := data["topic"].(string); strings.HasSuffix(topic, "/get") {
			return
		}

		payloadStr, ok := data["payload"].(string)
		if !ok {
			return
		}

		extracted := parsePayloadTemplate(payloadStr)
		params = append(params, extracted...)
	})

	return params
}

// ExtractGETParams collects MQTT parameter names requested by /get actions.
// The verifier uses these requests to select parameters for read-back checks.
func ExtractGETParams(actions []interface{}) map[string]bool {
	got := make(map[string]bool)

	walkActionMaps(actions, func(actionMap map[string]interface{}) {
		actionType, _ := actionMap["action"].(string)
		if actionType != "mqtt.publish" {
			return
		}

		data, ok := actionMap["data"].(map[string]interface{})
		if !ok {
			return
		}

		topic, _ := data["topic"].(string)
		if !strings.HasSuffix(topic, "/get") {
			return
		}

		payloadStr, ok := data["payload"].(string)
		if !ok {
			return
		}

		matches := keyValueRe.FindAllStringSubmatch(payloadStr, -1)
		for _, m := range matches {
			if len(m) >= 2 {
				got[m[1]] = true
			}
		}
	})

	return got
}

func walkActionMaps(actions []interface{}, visit func(map[string]interface{})) {
	for _, action := range actions {
		walkActionValue(action, visit)
	}
}

func walkActionValue(value interface{}, visit func(map[string]interface{})) {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			walkActionValue(item, visit)
		}
	case map[string]interface{}:
		visit(typed)
		for _, nested := range typed {
			walkActionValue(nested, visit)
		}
	}
}

// FilterVerifiable keeps only parameters with a corresponding GET request.
func FilterVerifiable(params []PayloadParam, getParams map[string]bool) []PayloadParam {
	var filtered []PayloadParam
	for _, p := range params {
		if getParams[p.Name] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// parsePayloadTemplate extracts parameters from a Jinja-templated JSON payload
// using regex because expressions such as {{ v_minimum_level }} are not valid
// JSON until Home Assistant renders them.
func parsePayloadTemplate(payload string) []PayloadParam {
	if m := topLevelKeyRe.FindStringSubmatch(payload); len(m) == 3 {
		valueTail := strings.TrimSpace(m[2])
		if strings.HasPrefix(valueTail, "[") || (strings.HasPrefix(valueTail, "{") && !strings.HasPrefix(valueTail, "{{")) {
			return []PayloadParam{{
				Name:      m[1],
				ValueExpr: strings.TrimSpace(payload),
			}}
		}
	}

	matches := keyValueRe.FindAllStringSubmatch(payload, -1)

	var params []PayloadParam
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		key := m[1]
		rawValue := strings.TrimSpace(m[2])

		valueExpr := rawValue

		if strings.HasPrefix(rawValue, "\"") && strings.HasSuffix(rawValue, "\"") {
			valueExpr = rawValue[1 : len(rawValue)-1]
		}

		params = append(params, PayloadParam{
			Name:      key,
			ValueExpr: valueExpr,
		})
	}

	return params
}
