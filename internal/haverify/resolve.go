package haverify

import (
	"fmt"
	"strings"
)

// MergeInputs merges automation input overrides onto blueprint defaults.
// Returns a complete map of input name → resolved value.
func MergeInputs(defaults map[string]interface{}, overrides map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

// BuildVariableMap substitutes inputs in the blueprint's variables section.
// yaml.v3 decodes !input minimum_level as the string minimum_level here.
func BuildVariableMap(variables map[string]interface{}, inputs map[string]interface{}) map[string]interface{} {
	resolved := make(map[string]interface{})
	for varName, varValue := range variables {
		if inputName, ok := varValue.(string); ok {
			if val, exists := inputs[inputName]; exists {
				resolved[varName] = val
			} else {
				resolved[varName] = inputName
			}
		} else {
			resolved[varName] = varValue
		}
	}
	return resolved
}

// ResolvePayloadParams substitutes simple {{ name }} references using the
// variable and input maps. Unknown names and other Jinja expressions remain unchanged.
func ResolvePayloadParams(params []PayloadParam, variables map[string]interface{}, inputs map[string]interface{}) []ParamExpectation {
	var expectations []ParamExpectation

	for _, p := range params {
		value := resolveExpression(p.ValueExpr, variables, inputs)
		expectations = append(expectations, ParamExpectation{
			MQTTParam: p.Name,
			Value:     value,
		})
	}

	return expectations
}

// resolveExpression substitutes {{ name }} references, looking in variables
// before inputs. Literal text and unresolved references remain unchanged.
func resolveExpression(expr string, variables map[string]interface{}, inputs map[string]interface{}) string {
	if !strings.Contains(expr, "{{") {
		return expr
	}

	result := jinjaVarRe.ReplaceAllStringFunc(expr, func(match string) string {
		varName := strings.TrimSpace(match[2 : len(match)-2])

		if val, ok := variables[varName]; ok {
			return fmt.Sprintf("%v", val)
		}
		if val, ok := inputs[varName]; ok {
			return fmt.Sprintf("%v", val)
		}
		return match
	})

	return result
}
