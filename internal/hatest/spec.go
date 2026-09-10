package hatest

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TestSpec represents a complete test file
type TestSpec struct {
	Version     int        `yaml:"version"`
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Tags        []string   `yaml:"tags"`
	Config      TestConfig `yaml:"config"`
	Tests       []TestCase `yaml:"tests"`
}

// TestConfig contains global test settings
type TestConfig struct {
	Timeout     Duration `yaml:"timeout"`
	Cleanup     bool     `yaml:"cleanup"`
	AutoRestore *bool    `yaml:"auto_restore"`
}

// TestCase represents a single test
type TestCase struct {
	Name                    string           `yaml:"name"`
	Description             string           `yaml:"description"`
	Skip                    bool             `yaml:"skip"`
	Setup                   []StateAction    `yaml:"setup"`
	Trigger                 []ServiceCall    `yaml:"trigger"`
	Events                  []EventAction    `yaml:"events"` // Fire HA events (e.g., zha_event)
	Assertions              []Assertion      `yaml:"assertions"`
	Cleanup                 []ServiceCall    `yaml:"cleanup"`
	Subtests                []TestCase       `yaml:"subtests"`
	TraceAssertions         *TraceAssertions `yaml:"trace_assertions"`
	TraceSkipReason         string           `yaml:"trace_skip_reason"`         // Only for helper/integration tests with no automation trace.
	AutomationTriggerReason string           `yaml:"automation_trigger_reason"` // Document deliberate manual/time-action coverage.
}

// TraceAssertions defines optional trace-level validation for a test case.
type TraceAssertions struct {
	Attribution          string           `yaml:"attribution"`
	AttributionReason    string           `yaml:"attribution_reason"`
	TriggerIndex         *int             `yaml:"trigger_index"`           // Optional zero-based action index: trigger calls, then events.
	Automation           string           `yaml:"automation"`              // Required: automation entity_id
	ExpectNoTrace        bool             `yaml:"expect_no_trace"`         // Assert that no run was created
	ExpectCompleted      *bool            `yaml:"expect_completed"`        // Expected value of state == "stopped"; nil skips the check.
	ExpectNoErrors       *bool            `yaml:"expect_no_errors"`        // Expected value of error == ""; nil skips the check.
	ExpectBranch         string           `yaml:"expect_branch"`           // Trigger ID to match
	ExpectActions        []ExpectedAction `yaml:"expect_actions"`          // Service calls to verify
	ExpectActionsInOrder []ExpectedAction `yaml:"expect_actions_in_order"` // Ordered service calls to verify
	RejectActions        []ExpectedAction `yaml:"reject_actions"`          // Service calls that must be absent
}

// ExpectedAction defines a service call expected in the automation trace.
type ExpectedAction struct {
	Service string                 `yaml:"service"` // e.g., "light.turn_on"
	Target  string                 `yaml:"target"`  // e.g., "light.example"
	Data    map[string]interface{} `yaml:"data"`    // Partial match on service call data
}

// StateAction describes a setup state and the method used to set it.
type StateAction struct {
	EntityID   string                 `yaml:"entity_id"`
	State      string                 `yaml:"state"`
	Attributes map[string]interface{} `yaml:"attributes"`
	// Method specifies how to set the state:
	// - "auto" (default): uses turn_on/turn_off for on/off in selected domains, direct API otherwise
	// - "direct": always uses POST /api/states (bypasses entity services)
	// - "service": uses turn_on/turn_off, or timer start/cancel for active/idle
	// - "mock": uses mock_entities.set_state service
	Method string `yaml:"method"`
}

// ServiceCall describes an HA service invocation.
type ServiceCall struct {
	Service string                 `yaml:"service"`
	Target  ServiceTarget          `yaml:"target"`
	Data    map[string]interface{} `yaml:"data"`
	Delay   Duration               `yaml:"delay"` // Optional pause after the service call
}

// ServiceTarget selects entities directly or through device and area registries.
type ServiceTarget struct {
	EntityID string   `yaml:"entity_id"`
	Entities []string `yaml:"entities"`
	DeviceID string   `yaml:"device_id"`
	Devices  []string `yaml:"devices"`
	AreaID   string   `yaml:"area_id"`
	Areas    []string `yaml:"areas"`
}

// EventAction fires a Home Assistant event
type EventAction struct {
	EventType string                 `yaml:"event_type"` // e.g., "zha_event"
	Data      map[string]interface{} `yaml:"data"`       // Event payload
}

// Assertion checks entity state
type Assertion struct {
	EntityID   string            `yaml:"entity_id"`
	State      string            `yaml:"state"`
	Attributes map[string]string `yaml:"attributes"` // e.g., "brightness": ">= 100"
	Timeout    Duration          `yaml:"timeout"`
	Skip       bool              `yaml:"skip"`
}

// Duration is a custom type for YAML duration parsing
type Duration time.Duration

// UnmarshalYAML implements custom YAML unmarshaling for Duration
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}

	duration, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}

	*d = Duration(duration)
	return nil
}

// LoadTestSpec loads and parses a test specification file
func LoadTestSpec(filePath string) (*TestSpec, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read test file %s: %w", filePath, err)
	}

	var spec TestSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse test file %s: %w", filePath, err)
	}

	if spec.Version == 0 {
		spec.Version = 1
	}

	if spec.Config.Timeout == 0 {
		spec.Config.Timeout = Duration(30 * time.Second)
	}

	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid test spec: %w", err)
	}

	return &spec, nil
}

// Validate checks if the test spec is valid
func (s *TestSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("test spec must have a name")
	}

	if len(s.Tests) == 0 {
		return fmt.Errorf("test spec must have at least one test case")
	}

	for i, tc := range s.Tests {
		if err := tc.Validate(); err != nil {
			return fmt.Errorf("test case %d (%s): %w", i, tc.Name, err)
		}
	}

	return nil
}

// Validate checks if a test case is valid
func (tc *TestCase) Validate() error {
	if tc.Name == "" {
		return fmt.Errorf("test case must have a name")
	}

	if len(tc.Trigger) == 0 && len(tc.Events) == 0 && len(tc.Subtests) == 0 {
		return fmt.Errorf("test case must have triggers, events, or subtests")
	}

	if len(tc.Assertions) == 0 && len(tc.Subtests) == 0 {
		return fmt.Errorf("test case must have assertions or subtests")
	}
	if tc.TraceAssertions != nil && strings.TrimSpace(tc.TraceAssertions.Automation) == "" {
		return fmt.Errorf("trace_assertions.automation must be a non-empty automation entity ID")
	}
	if tc.TraceAssertions != nil && tc.TraceAssertions.ExpectNoTrace &&
		(tc.TraceAssertions.ExpectCompleted != nil || tc.TraceAssertions.ExpectNoErrors != nil ||
			tc.TraceAssertions.ExpectBranch != "" || len(tc.TraceAssertions.ExpectActions) > 0 ||
			len(tc.TraceAssertions.ExpectActionsInOrder) > 0 ||
			len(tc.TraceAssertions.RejectActions) > 0) {
		return fmt.Errorf("trace_assertions.expect_no_trace cannot be combined with trace run assertions")
	}

	if tc.TraceAssertions != nil {
		switch tc.TraceAssertions.Attribution {
		case "", "context":
		case "fresh_unique":
			if strings.TrimSpace(tc.TraceAssertions.AttributionReason) == "" {
				return fmt.Errorf("fresh_unique trace attribution requires attribution_reason explaining why command context is unavailable")
			}
			if tc.TraceAssertions.TriggerIndex != nil {
				return fmt.Errorf("fresh_unique trace attribution cannot select a command trigger_index")
			}
		default:
			return fmt.Errorf("unsupported trace attribution %q", tc.TraceAssertions.Attribution)
		}
	}
	if tc.TraceAssertions != nil && tc.TraceAssertions.TriggerIndex != nil {
		index := *tc.TraceAssertions.TriggerIndex
		if index < 0 || index >= len(tc.Trigger)+len(tc.Events) {
			return fmt.Errorf("trace_assertions.trigger_index must select a trigger or event action")
		}
	}

	for i, subtest := range tc.Subtests {
		if err := subtest.Validate(); err != nil {
			return fmt.Errorf("subtest %d (%s): %w", i, subtest.Name, err)
		}
	}

	return nil
}

// AutoRestoreEnabled reports whether automatic restoration is enabled (the default).
// The runner also requires Cleanup before taking snapshots or restoring them.
func (c TestConfig) AutoRestoreEnabled() bool {
	return c.AutoRestore == nil || *c.AutoRestore
}
