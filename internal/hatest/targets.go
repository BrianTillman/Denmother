package hatest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// UnmarshalYAML accepts HA's scalar-or-list target fields while preserving the
// historical entities alias used by existing Denmother recipes.
func (target *ServiceTarget) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("service target must be a mapping")
	}
	*target = ServiceTarget{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		var values []string
		switch value.Kind {
		case yaml.ScalarNode:
			values = []string{value.Value}
		case yaml.SequenceNode:
			if err := value.Decode(&values); err != nil {
				return err
			}
		default:
			return fmt.Errorf("target.%s must be a string or list", key)
		}
		switch key {
		case "entity_id", "entities":
			target.Entities = append(target.Entities, values...)
		case "device_id", "devices":
			target.Devices = append(target.Devices, values...)
		case "area_id", "areas":
			target.Areas = append(target.Areas, values...)
		default:
			return fmt.Errorf("unsupported service target field %q", key)
		}
	}
	if len(target.Entities) == 1 {
		target.EntityID = target.Entities[0]
		target.Entities = nil
	}
	if len(target.Devices) == 1 {
		target.DeviceID = target.Devices[0]
		target.Devices = nil
	}
	if len(target.Areas) == 1 {
		target.AreaID = target.Areas[0]
		target.Areas = nil
	}
	return nil
}

func serviceCallPayload(call ServiceCall) (map[string]interface{}, error) {
	data := copyAttributes(call.Data)
	if data == nil {
		data = map[string]interface{}{}
	}
	for key, ids := range map[string][]string{
		"entity_id": append(append([]string{}, call.Target.Entities...), call.Target.EntityID),
		"device_id": append(append([]string{}, call.Target.Devices...), call.Target.DeviceID),
		"area_id":   append(append([]string{}, call.Target.Areas...), call.Target.AreaID),
	} {
		ids = uniqueStringsLocal(ids)
		if len(ids) == 0 {
			continue
		}
		if _, exists := data[key]; exists {
			return nil, fmt.Errorf("%s is specified in both target and data", key)
		}
		if len(ids) == 1 {
			data[key] = ids[0]
		} else {
			data[key] = ids
		}
	}
	return data, nil
}

func dynamicTarget(data map[string]interface{}) bool {
	return data["device_id"] != nil || data["area_id"] != nil || data["floor_id"] != nil || data["label_id"] != nil || data["entity_id"] == "all"
}

// resolveTarget uses HA's own registry/group semantics, rather than guessing
// entity membership from entity names. Missing selectors fail before mutation.
func (r *TestRunner) resolveTarget(target map[string]interface{}) ([]string, error) {
	ws, err := r.getWSClient()
	if err != nil {
		return nil, err
	}
	raw, err := ws.SendCommandContext(r.client.context(), "extract_from_target", map[string]interface{}{"target": target, "expand_group": true})
	if err != nil {
		return nil, fmt.Errorf("resolve HA target: %w", err)
	}
	var response struct {
		Entities       []string `json:"referenced_entities"`
		MissingDevices []string `json:"missing_devices"`
		MissingAreas   []string `json:"missing_areas"`
		MissingFloors  []string `json:"missing_floors"`
		MissingLabels  []string `json:"missing_labels"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if len(response.MissingDevices)+len(response.MissingAreas)+len(response.MissingFloors)+len(response.MissingLabels) > 0 {
		return nil, fmt.Errorf("target contains missing registry IDs (devices=%v areas=%v floors=%v labels=%v)", response.MissingDevices, response.MissingAreas, response.MissingFloors, response.MissingLabels)
	}
	if len(response.Entities) == 0 {
		return nil, fmt.Errorf("HA target resolves to no entities")
	}
	sort.Strings(response.Entities)
	return uniqueStringsLocal(response.Entities), nil
}

func targetFromPayload(data map[string]interface{}) map[string]interface{} {
	target := map[string]interface{}{}
	for _, key := range []string{"entity_id", "device_id", "area_id", "floor_id", "label_id"} {
		if value, ok := data[key]; ok {
			target[key] = value
		}
	}
	return target
}

func (r *TestRunner) resolveServiceCall(call ServiceCall) (ServiceCall, error) {
	data, err := serviceCallPayload(call)
	if err != nil {
		return call, err
	}
	if !dynamicTarget(data) {
		return call, nil
	}
	entities, err := r.resolveTarget(targetFromPayload(data))
	if err != nil {
		return call, err
	}
	domain, _ := parseService(call.Service)
	var selected []string
	for _, entity := range entities {
		if domain == "homeassistant" || domain == "mock_entities" || getDomain(entity) == domain {
			selected = append(selected, entity)
		}
	}
	if len(selected) == 0 {
		return call, fmt.Errorf("target has no entities for service %s", call.Service)
	}
	for key := range targetFromPayload(data) {
		delete(data, key)
	}
	call.Data = data
	call.Target = ServiceTarget{Entities: selected}
	return call, nil
}

func (r *TestRunner) resolveCaseTargets(tc TestCase) (TestCase, error) {
	tc.Trigger = append([]ServiceCall(nil), tc.Trigger...)
	tc.Cleanup = append([]ServiceCall(nil), tc.Cleanup...)
	for _, calls := range [][]ServiceCall{tc.Trigger, tc.Cleanup} {
		for i, call := range calls {
			resolved, err := r.resolveServiceCall(call)
			if err != nil {
				return tc, fmt.Errorf("%s target: %w", call.Service, err)
			}
			calls[i] = resolved
		}
	}
	return tc, nil
}

func (r *TestRunner) resolveTraceTargets(trace *FullTrace) error {
	for _, nodes := range trace.Trace {
		for _, node := range nodes {
			if node.Result == nil {
				continue
			}
			params := node.Result.Params
			target := map[string]interface{}{}
			if data, ok := params["service_data"].(map[string]interface{}); ok {
				for key, value := range targetFromPayload(data) {
					target[key] = value
				}
			}
			if data, ok := params["target"].(map[string]interface{}); ok {
				for key, value := range targetFromPayload(data) {
					target[key] = value
				}
			}
			if !dynamicTarget(target) {
				continue
			}
			entities, err := r.resolveTarget(target)
			if err != nil {
				return fmt.Errorf("resolve trace action target: %w", err)
			}
			domain, _ := params["domain"].(string)
			var selected []string
			for _, entity := range entities {
				if domain == "homeassistant" || domain == "mock_entities" || strings.HasPrefix(entity, domain+".") {
					selected = append(selected, entity)
				}
			}
			target["entity_id"] = selected
			params["target"] = target
		}
	}
	return nil
}
