package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SchemaVersion is the stable contract version for every --json command output.
const SchemaVersion = "dm.operator.v1"

// Status is the normalized outcome for commands and workflow steps.
type Status string

const (
	StatusSuccess Status = "success"
	StatusWarning Status = "warning"
	StatusPartial Status = "partial"
	StatusFailure Status = "failure"
)

const (
	// Exit codes for the shared operator runtime.
	ExitSuccess = 0
	ExitFailure = 1
	ExitWarning = 2
	ExitPartial = 3
)

// Step captures one significant part of a command or workflow.
type Step struct {
	ID               string         `json:"id"`
	RuleID           string         `json:"rule_id,omitempty"`
	Title            string         `json:"title"`
	Status           Status         `json:"status"`
	Summary          string         `json:"summary"`
	Duration         string         `json:"duration,omitempty"`
	Artifacts        []string       `json:"artifacts,omitempty"`
	NextCommands     []string       `json:"next_commands,omitempty"`
	SafeReproCommand string         `json:"safe_repro_command,omitempty"`
	FixCommand       string         `json:"fix_command,omitempty"`
	RequiresHuman    bool           `json:"requires_human,omitempty"`
	Mutates          bool           `json:"mutates,omitempty"`
	Details          map[string]any `json:"details,omitempty"`
	Hints            []string       `json:"hints,omitempty"`
	ErrorCode        string         `json:"error_code,omitempty"`
	NextActions      []Action       `json:"next_actions,omitempty"`
}

// Action is an executable recommendation. Arguments must be passed directly,
// without a shell; authorization and credentials are supplied by the caller.
type Action struct {
	Executable    string   `json:"executable"`
	Args          []string `json:"args"`
	CWD           string   `json:"cwd"`
	MutationScope []string `json:"mutation_scope"`
	RequiresHuman bool     `json:"requires_human"`
	RequiredEnv   []string `json:"required_env,omitempty"`
}

// Result is the shared human- and machine-readable summary shape.
type Result struct {
	SchemaVersion string    `json:"schema_version"`
	RunID         string    `json:"run_id"`
	Command       string    `json:"command"`
	Profile       string    `json:"profile,omitempty"`
	CWD           string    `json:"cwd,omitempty"`
	GitHead       string    `json:"git_head,omitempty"`
	GitDirty      bool      `json:"git_dirty,omitempty"`
	Status        Status    `json:"status"`
	Summary       string    `json:"summary"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	Duration      string    `json:"duration"`
	Target        *Target   `json:"target,omitempty"`
	Steps         []Step    `json:"steps,omitempty"`
	Hints         []string  `json:"hints,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	Evidence      string    `json:"evidence,omitempty"`
	Truncated     bool      `json:"truncated,omitempty"`
}

// Runtime manages a command's shared result state and output emission.
type Runtime struct {
	jsonOutput bool
	out        io.Writer
	result     Result
	transform  func(*Result) error
}

// SetTransform installs the CLI's final presentation and portability policy.
// Library users retain the unmodified operator contract by default.
func (rt *Runtime) SetTransform(transform func(*Result) error) { rt.transform = transform }

// NewRuntime creates a shared operator runtime for a command.
func NewRuntime(command string, jsonOutput bool, out io.Writer) *Runtime {
	return NewRuntimeContext(context.Background(), command, jsonOutput, out)
}

// NewRuntimeContext cancels optional Git metadata discovery with the command.
func NewRuntimeContext(ctx context.Context, command string, jsonOutput bool, out io.Writer) *Runtime {
	started := time.Now().UTC()
	cwd, _ := os.Getwd()
	gitHead := gitOutputContext(ctx, "rev-parse", "--short=12", "HEAD")

	return &Runtime{
		jsonOutput: jsonOutput,
		out:        out,
		result: Result{
			SchemaVersion: SchemaVersion,
			RunID:         fmt.Sprintf("%s-%d", started.Format("20060102T150405.000000000Z"), os.Getpid()),
			Command:       command,
			CWD:           cwd,
			GitHead:       gitHead,
			GitDirty:      gitDirtyContext(ctx),
			StartedAt:     started,
		},
	}
}

// SetProfile records the active workflow profile.
func (rt *Runtime) SetProfile(profile string) {
	rt.result.Profile = profile
}

// SetTarget records the resolved target for the command.
func (rt *Runtime) SetTarget(target *Target) {
	rt.result.Target = target
}

// AddStep appends a workflow step result.
func (rt *Runtime) AddStep(step Step) {
	if len(step.Artifacts) == 0 && step.Details != nil {
		if artifacts, ok := stringSliceFromAny(step.Details["artifacts"]); ok {
			step.Artifacts = artifacts
		}
	}
	if len(step.NextCommands) == 0 && step.Details != nil {
		if commands, ok := stringSliceFromAny(step.Details["next_commands"]); ok {
			step.NextCommands = commands
		}
	}
	rt.result.Steps = append(rt.result.Steps, step)
}

// AddHint appends a top-level remediation hint.
func (rt *Runtime) AddHint(hint string) {
	if hint == "" {
		return
	}
	rt.result.Hints = append(rt.result.Hints, hint)
}

// PrintPreflight emits the shared target banner for human-readable output.
func (rt *Runtime) PrintPreflight() {
	if rt.jsonOutput || rt.result.Target == nil {
		return
	}

	target := rt.result.Target

	fmt.Fprintln(rt.out)
	fmt.Fprintf(rt.out, "dm %s\n", rt.result.Command)
	if rt.result.Profile != "" {
		fmt.Fprintf(rt.out, "Profile: %s\n", rt.result.Profile)
	}
	fmt.Fprintf(rt.out, "Target: %s\n", target.Name)
	fmt.Fprintf(rt.out, "Risk: %s\n", target.Risk)
	if target.URL != "" {
		fmt.Fprintf(rt.out, "URL: %s\n", target.URL)
	}
	if target.Source != "" {
		fmt.Fprintf(rt.out, "Source: %s\n", target.Source)
	}
	for _, guardrail := range target.Guardrails {
		fmt.Fprintf(rt.out, "Guardrail: %s\n", guardrail)
	}
	fmt.Fprintln(rt.out)
}

// Complete finalizes and emits the shared command result.
func (rt *Runtime) Complete(status Status, summary string) error {
	rt.result.Status = status
	rt.result.Summary = summary
	rt.result.ExitCode = ExitCodeForStatus(status)
	rt.result.EndedAt = time.Now().UTC()
	rt.result.Duration = rt.result.EndedAt.Sub(rt.result.StartedAt).String()
	if status == StatusFailure && rt.result.ErrorCode == "" {
		rt.result.ErrorCode = "command_failed"
	}
	if status == StatusPartial {
		rt.result.ErrorCode = "verification_incomplete"
	}
	if rt.transform != nil {
		if err := rt.transform(&rt.result); err != nil {
			rt.result.Status = StatusFailure
			rt.result.ExitCode = ExitFailure
			rt.result.ErrorCode = "result_processing_failed"
			rt.result.Summary = err.Error()
		}
	}

	if err := rt.emit(); err != nil {
		return err
	}
	if rt.result.ExitCode != ExitSuccess {
		return &ExitError{code: rt.result.ExitCode}
	}
	return nil
}

func (rt *Runtime) emit() error {
	if rt.jsonOutput {
		encoder := json.NewEncoder(rt.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rt.result)
	}

	fmt.Fprintf(rt.out, "Result: %s\n", rt.result.Status)
	fmt.Fprintf(rt.out, "Summary: %s\n", rt.result.Summary)
	for _, hint := range rt.result.Hints {
		fmt.Fprintf(rt.out, "Hint: %s\n", hint)
	}
	fmt.Fprintln(rt.out)
	return nil
}

// ExitCodeForStatus maps normalized result states to stable process exit codes.
func ExitCodeForStatus(status Status) int {
	switch status {
	case StatusSuccess:
		return ExitSuccess
	case StatusWarning:
		return ExitWarning
	case StatusPartial:
		return ExitPartial
	default:
		return ExitFailure
	}
}

// MergeStatus keeps the highest-severity result state.
func MergeStatus(current, next Status) Status {
	if severity(next) > severity(current) {
		return next
	}
	return current
}

func severity(status Status) int {
	switch status {
	case StatusSuccess:
		return 0
	case StatusWarning:
		return 1
	case StatusPartial:
		return 2
	default:
		return 3
	}
}

// ExitError carries a specific process exit code without forcing an extra error message.
type ExitError struct {
	code int
}

func (e *ExitError) Error() string {
	return ""
}

// ExitCode returns the process exit code for the command.
func (e *ExitError) ExitCode() int {
	return e.code
}

func gitOutputContext(ctx context.Context, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func gitDirtyContext(ctx context.Context) bool {
	return gitOutputContext(ctx, "status", "--porcelain") != ""
}

func stringSliceFromAny(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
		return values, true
	default:
		return nil, false
	}
}
