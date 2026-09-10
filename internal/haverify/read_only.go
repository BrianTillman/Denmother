package haverify

import "fmt"

var readOnlyInputParams = []struct {
	input string
	param string
}{
	{input: "dimming_mode", param: "dimmingMode"},
	{input: "power_type", param: "powerType"},
}

// AppendReadOnlyInputExpectations adds mapped read-only inputs that have a GET
// request and no existing expectation.
func AppendReadOnlyInputExpectations(expectations []ParamExpectation, inputs map[string]interface{}, getParams map[string]bool) []ParamExpectation {
	seen := make(map[string]bool, len(expectations))
	for _, expectation := range expectations {
		seen[expectation.MQTTParam] = true
	}

	for _, mapping := range readOnlyInputParams {
		if seen[mapping.param] || !getParams[mapping.param] {
			continue
		}
		value, ok := inputs[mapping.input]
		if !ok {
			continue
		}
		expectations = append(expectations, ParamExpectation{
			MQTTParam: mapping.param,
			Value:     fmt.Sprintf("%v", value),
		})
	}

	return expectations
}
