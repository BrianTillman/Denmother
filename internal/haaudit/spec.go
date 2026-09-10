package haaudit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AuditSpec represents a complete audit specification file.
type AuditSpec struct {
	Version      int           `yaml:"version"`
	Name         string        `yaml:"name"`
	Automation   string        `yaml:"automation"`
	Trigger      TriggerDef    `yaml:"trigger"`
	Expectations []Expectation `yaml:"expectations"`
	Tolerance    string        `yaml:"tolerance"`
	CanceledBy   []CancelDef   `yaml:"canceled_by"`
}

// TriggerDef identifies what initiates an audit window.
// For history-sourced specs (default), EntityID and To define the state change.
// For trace-sourced specs (Source: "traces"), automation traces are the trigger source.
type TriggerDef struct {
	EntityID string `yaml:"entity_id"`
	To       string `yaml:"to"`
	Source   string `yaml:"source"` // "history" (default) or "traces"
}

// CancelDef identifies a state transition that cancels a delayed audit window.
type CancelDef struct {
	EntityID string `yaml:"entity_id"`
	To       string `yaml:"to"`
	Within   string `yaml:"within"`
}

// Expectation describes the expected state of a target entity for a given vibe.
type Expectation struct {
	Vibe       string            `yaml:"vibe"`
	EntityID   string            `yaml:"entity_id"`
	State      string            `yaml:"state"`
	NoChange   bool              `yaml:"no_change"`
	Attributes map[string]string `yaml:"attributes"`
}

func validateExpectation(i int, exp Expectation, requireVibe bool) error {
	if requireVibe && exp.Vibe == "" {
		return fmt.Errorf("expectation %d: missing vibe", i)
	}
	if exp.EntityID == "" {
		return fmt.Errorf("expectation %d: missing entity_id", i)
	}
	if exp.NoChange {
		if exp.State != "" || len(exp.Attributes) > 0 {
			return fmt.Errorf("expectation %d: no_change cannot be combined with state or attributes", i)
		}
		return nil
	}
	if exp.State == "" {
		return fmt.Errorf("expectation %d: missing state or no_change", i)
	}
	return nil
}

// LoadAuditSpec loads and parses an audit specification file.
func LoadAuditSpec(path string) (*AuditSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read audit spec %s: %w", path, err)
	}

	var spec AuditSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse audit spec %s: %w", path, err)
	}

	if spec.Version == 0 {
		spec.Version = 1
	}

	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid audit spec %s: %w", path, err)
	}

	return &spec, nil
}

// IsTraceBased returns true when the spec uses automation traces as the trigger source.
func (s *AuditSpec) IsTraceBased() bool {
	return s.Trigger.Source == "traces"
}

func (s *AuditSpec) UsesLegacyVibeAliases() bool {
	return !s.IsTraceBased() && s.Trigger.To == "off"
}

// Validate checks that all required fields are present.
func (s *AuditSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("audit spec must have a name")
	}
	if s.Automation == "" {
		return fmt.Errorf("audit spec must have an automation")
	}
	if s.Tolerance != "" {
		if _, err := time.ParseDuration(s.Tolerance); err != nil {
			return fmt.Errorf("tolerance must be a duration such as 30s or 3m: %w", err)
		}
	}
	for i, cancel := range s.CanceledBy {
		if cancel.EntityID == "" {
			return fmt.Errorf("canceled_by %d: missing entity_id", i)
		}
		if cancel.To == "" {
			return fmt.Errorf("canceled_by %d: missing to", i)
		}
		if cancel.Within == "" {
			return fmt.Errorf("canceled_by %d: missing within", i)
		}
		if _, err := time.ParseDuration(cancel.Within); err != nil {
			return fmt.Errorf("canceled_by %d: within must be a duration such as 30s or 3m: %w", i, err)
		}
	}

	switch s.Trigger.Source {
	case "", "history", "traces":
	default:
		return fmt.Errorf("trigger.source must be \"history\" or \"traces\", got %q", s.Trigger.Source)
	}

	if s.IsTraceBased() {
		// Trace-sourced specs: expectations are optional. With no expectations,
		// health check mode passes only stopped traces with no error. When
		// expectations are present, vibe is optional per-expectation.
		for i, exp := range s.Expectations {
			if err := validateExpectation(i, exp, false); err != nil {
				return err
			}
		}
	} else {
		// History-sourced specs: trigger entity/to and expectations with vibe are required.
		if s.Trigger.EntityID == "" {
			return fmt.Errorf("audit spec must have a trigger.entity_id")
		}
		if s.Trigger.To == "" {
			return fmt.Errorf("audit spec must have a trigger.to")
		}
		if len(s.Expectations) == 0 {
			return fmt.Errorf("audit spec must have at least one expectation")
		}
		for i, exp := range s.Expectations {
			if err := validateExpectation(i, exp, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// EffectiveTolerance returns the spec-specific tolerance when present,
// otherwise the command-level default.
func (s *AuditSpec) EffectiveTolerance(fallback time.Duration) time.Duration {
	if s.Tolerance == "" {
		return fallback
	}
	d, err := time.ParseDuration(s.Tolerance)
	if err != nil {
		return fallback
	}
	return d
}

// ParseAttributeComparison parses an attribute expression like ">= 199", "<= 255",
// "== 204", or "199" (bare value defaults to ==).
func ParseAttributeComparison(expr string) (op string, value float64, err error) {
	expr = strings.TrimSpace(expr)

	var valueStr string

	switch {
	case strings.HasPrefix(expr, ">="):
		op = ">="
		valueStr = strings.TrimSpace(expr[2:])
	case strings.HasPrefix(expr, "<="):
		op = "<="
		valueStr = strings.TrimSpace(expr[2:])
	case strings.HasPrefix(expr, "=="):
		op = "=="
		valueStr = strings.TrimSpace(expr[2:])
	case strings.HasPrefix(expr, ">"):
		op = ">"
		valueStr = strings.TrimSpace(expr[1:])
	case strings.HasPrefix(expr, "<"):
		op = "<"
		valueStr = strings.TrimSpace(expr[1:])
	default:
		op = "=="
		valueStr = expr
	}

	if valueStr == "" {
		return "", 0, fmt.Errorf("empty expression")
	}

	f, parseErr := strconv.ParseFloat(valueStr, 64)
	if parseErr != nil {
		return "", 0, fmt.Errorf("invalid numeric value %q: %w", valueStr, parseErr)
	}

	return op, f, nil
}

// AllEntityIDs lists entities needed for history queries: the trigger, cancellation
// triggers, mode helpers (including applicable aliases), and expectation targets.
// It removes duplicates and omits the trigger entity for trace-sourced specs.
func (s *AuditSpec) AllEntityIDs() []string {
	seen := make(map[string]bool)
	var ids []string

	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	if !s.IsTraceBased() {
		add(s.Trigger.EntityID)
	}
	for _, cancel := range s.CanceledBy {
		add(cancel.EntityID)
	}
	for _, exp := range s.Expectations {
		for _, vibeID := range vibeEntityIDs(exp.Vibe, s.UsesLegacyVibeAliases()) {
			add(vibeID)
		}
		add(exp.EntityID)
	}

	return ids
}

// NoChangeEntityIDs returns targets that need logbook attribution so changes
// from other entities can be distinguished from actions by this automation.
func (s *AuditSpec) NoChangeEntityIDs() []string {
	seen := make(map[string]bool)
	var ids []string
	for _, exp := range s.Expectations {
		if exp.NoChange && !seen[exp.EntityID] {
			seen[exp.EntityID] = true
			ids = append(ids, exp.EntityID)
		}
	}
	return ids
}
