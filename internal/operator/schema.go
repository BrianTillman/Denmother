package operator

import "encoding/json"

// ResultJSONSchema returns the generated JSON Schema for the shared operator
// result envelope emitted by Denmother JSON commands.
func ResultJSONSchema() string {
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "urn:denmother:operator-result:dm.operator.v1",
		"title":                "DenMother Operator Result",
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_version",
			"run_id",
			"command",
			"status",
			"summary",
			"exit_code",
			"started_at",
			"ended_at",
			"duration",
		},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": SchemaVersion},
			"run_id":         map[string]any{"type": "string", "minLength": 1},
			"command":        map[string]any{"type": "string", "minLength": 1},
			"profile":        map[string]any{"type": "string"},
			"cwd":            map[string]any{"type": "string"},
			"git_head":       map[string]any{"type": "string"},
			"git_dirty":      map[string]any{"type": "boolean"},
			"status":         enumSchema("success", "warning", "partial", "failure"),
			"summary":        map[string]any{"type": "string"},
			"exit_code":      map[string]any{"type": "integer"},
			"started_at":     map[string]any{"type": "string", "format": "date-time"},
			"ended_at":       map[string]any{"type": "string", "format": "date-time"},
			"duration":       map[string]any{"type": "string"},
			"target":         targetSchema(),
			"steps": map[string]any{
				"type":  "array",
				"items": stepSchema(),
			},
			"hints":      stringArraySchema(),
			"error_code": map[string]any{"type": "string"},
			"evidence":   map[string]any{"type": "string"},
			"truncated":  map[string]any{"type": "boolean"},
		},
	}
	encoded, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(encoded) + "\n"
}

func stepSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "title", "status", "summary"},
		"properties": map[string]any{
			"id":                 map[string]any{"type": "string"},
			"rule_id":            map[string]any{"type": "string"},
			"title":              map[string]any{"type": "string"},
			"status":             enumSchema("success", "warning", "partial", "failure"),
			"summary":            map[string]any{"type": "string"},
			"duration":           map[string]any{"type": "string"},
			"artifacts":          stringArraySchema(),
			"next_commands":      stringArraySchema(),
			"safe_repro_command": map[string]any{"type": "string"},
			"fix_command":        map[string]any{"type": "string"},
			"requires_human":     map[string]any{"type": "boolean"},
			"mutates":            map[string]any{"type": "boolean"},
			"details":            map[string]any{"type": "object"},
			"hints":              stringArraySchema(),
			"error_code":         map[string]any{"type": "string"},
			"next_actions":       map[string]any{"type": "array", "items": actionSchema()},
		},
	}
}

func actionSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"executable", "args", "cwd", "mutation_scope", "requires_human"},
		"properties": map[string]any{
			"executable": map[string]any{"type": "string"},
			"args":       stringArraySchema(), "cwd": map[string]any{"type": "string"},
			"mutation_scope": stringArraySchema(), "requires_human": map[string]any{"type": "boolean"},
			"required_env": stringArraySchema(),
		},
	}
}

func targetSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "instance", "risk", "is_local", "mode"},
		"properties": map[string]any{
			"name":       map[string]any{"type": "string"},
			"instance":   map[string]any{"type": "string"},
			"url":        map[string]any{"type": "string"},
			"source":     map[string]any{"type": "string"},
			"risk":       enumSchema("safe", "caution", "high"),
			"is_local":   map[string]any{"type": "boolean"},
			"guardrails": stringArraySchema(),
			"mode":       enumSchema("read_only", "mutating"),
		},
	}
}

func enumSchema(values ...string) map[string]any {
	enum := make([]any, 0, len(values))
	for _, value := range values {
		enum = append(enum, value)
	}
	return map[string]any{"type": "string", "enum": enum}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
}
