package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/operator"
)

var evidenceDir string
var compactOutput bool

func newCommandRuntime(command string, jsonOutput bool, out io.Writer) *operator.Runtime {
	rt := operator.NewRuntimeContext(commandContext(), command, jsonOutput, out)
	rt.SetTransform(func(result *operator.Result) error {
		if err := portableResult(result); err != nil {
			return err
		}
		if jsonOutput && (compactOutput || evidenceDir != "") && !installationCommandName(command) {
			return saveEvidence(result, compactOutput)
		}
		return nil
	})
	return rt
}

// Normalize only fields that promise commands. Never interpret logs, findings,
// templates, or other untrusted evidence as executable recommendations.
func commandField(key string) bool {
	switch key {
	case "command", "commands", "next_commands", "validation_commands", "expanded_commands", "broad_commands", "alternatives", "safe_repro_command", "fix_command":
		return true
	}
	return false
}

func portableResult(result *operator.Result) error {
	// A capability catalog describes commands; it is not a queue of actions.
	if result.Command == "capabilities" {
		return nil
	}
	for i := range result.Steps {
		step := &result.Steps[i]
		data, err := json.Marshal(step)
		if err != nil {
			return err
		}
		var doc map[string]any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&doc); err != nil {
			return err
		}
		var actions []operator.Action
		seen := map[string]bool{}
		var walk func(any, string) any
		walk = func(value any, key string) any {
			switch v := value.(type) {
			case map[string]any:
				keys := make([]string, 0, len(v))
				for k := range v {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if commandField(k) || recommendationContainer(k) {
						v[k] = walk(v[k], k)
					}
				}
			case []any:
				for j := range v {
					v[j] = walk(v[j], key)
				}
			case string:
				if commandField(key) {
					display, action := portableRecommendation(v, result.Target)
					if action != nil {
						encoded, _ := json.Marshal(action)
						if !seen[string(encoded)] {
							seen[string(encoded)] = true
							actions = append(actions, *action)
						}
					}
					return display
				}
			}
			return value
		}
		// Capability command paths are stable identifiers, not executable next
		// actions. Rewriting them breaks --command discovery and its schema.
		if result.Command != "capabilities" || step.ID != "capabilities" {
			walk(doc, "")
		}
		data, err = json.Marshal(doc)
		if err != nil {
			return err
		}
		decoder = json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(step); err != nil {
			return err
		}
		step.NextActions = actions
		if step.Status == operator.StatusFailure && step.ErrorCode == "" {
			if step.RuleID != "" {
				step.ErrorCode = step.RuleID
			} else {
				step.ErrorCode = "step_failed"
			}
		}
		if step.Status == operator.StatusPartial && step.ErrorCode == "" {
			step.ErrorCode = "verification_incomplete"
		}
		if step.ErrorCode != "" && result.Status == operator.StatusFailure && (result.ErrorCode == "" || result.ErrorCode == "command_failed") {
			result.ErrorCode = step.ErrorCode
		}
	}
	return nil
}

func recommendationContainer(key string) bool {
	switch key {
	case "details", "context", "baseline", "fast_tests", "recommended_commands", "tasks", "next", "test_plan", "issue_groups", "files_with_issues":
		return true
	}
	return false
}

func portableRecommendation(command string, target *operator.Target) (string, *operator.Action) {
	args, ok := splitRecommendation(command)
	if !ok || len(args) == 0 || (args[0] != "./dm" && args[0] != "dm") {
		return command, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return command, nil
	}
	args = args[1:]
	var requiredEnv []string
	acknowledgement := false
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		flag, _, equals := strings.Cut(args[i], "=")
		env := ""
		switch flag {
		case "--prod-token":
			env = "HASS_PROD_TOKEN"
		case "--dev-token", "--ha-token":
			env = "HASS_DEV_TOKEN"
		}
		if env != "" {
			requiredEnv = append(requiredEnv, env)
			if !equals && i+1 < len(args) {
				i++
			}
			continue
		}
		if flag == "--allow-prod" {
			acknowledgement = true
			continue
		}
		filtered = append(filtered, args[i])
	}
	args = filtered
	cli, _, err := rootCmd.Find(args)
	if err != nil {
		return command, nil
	}
	if cli == nil {
		cli = rootCmd
	}
	name := cli.CommandPath()
	config, err := resolvedConfigRoot()
	if err == nil && !hasArgument(args, "--config") && !hasArgument(args, "-c") && projectCommand(name) {
		args = append(args, "--config", config)
	}
	if target != nil && target.URL != "" && name != "dm check" {
		instance := "dev"
		if target.Instance == "production" {
			instance = "prod"
		}
		flag := instance + "-url"
		if cli.Flags().Lookup(flag) != nil && !hasArgument(args, "--dev-url") && !hasArgument(args, "--prod-url") && !hasArgument(args, "--ha-url") {
			args = append(args, "--"+flag, target.URL)
			if instance != "dev" || !target.IsLocal || !strings.Contains(target.Source, "selected project development environment") {
				requiredEnv = append(requiredEnv, "HASS_"+strings.ToUpper(instance)+"_TOKEN")
			}
		}
	}
	cwd, _ := os.Getwd()
	policy := policyForCommand(name)
	action := &operator.Action{Executable: executable, Args: args, CWD: cwd, MutationScope: policy.MutationScope, RequiredEnv: requiredEnv}
	action.RequiresHuman = acknowledgement || ((hasArgument(args, "--prod-url") || (target != nil && target.Instance == "production")) && stringSliceContains(policy.MutationScope, "ha_state")) || name == "dm dev reset"
	parts := []string{shellQuote(executable)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " "), action
}

func projectCommand(name string) bool {
	return name != "dm setup" && !strings.HasPrefix(name, "dm skills") && name != "dm version" && name != "dm capabilities" && name != "dm init" && !strings.HasPrefix(name, "dm release") && !strings.HasPrefix(name, "dm completion")
}

func hasArgument(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

// splitRecommendation parses shell-quoted arguments without executing a shell.
// Unquoted operators and expansions are rejected; quoted content stays literal.
func splitRecommendation(input string) ([]string, bool) {
	var args []string
	var word strings.Builder
	var quote rune
	started, escaped := false, false
	for _, r := range input {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == '#' && !started {
			break
		}
		if strings.ContainsRune(";|&<>`$()\n\r", r) {
			return nil, false
		}
		if r == ' ' || r == '\t' {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, false
	}
	if started {
		args = append(args, word.String())
	}
	for _, arg := range args {
		if arg == "DASHBOARD" || strings.Contains(arg, "<") || strings.Contains(arg, ">") {
			return nil, false
		}
	}
	return args, true
}

func saveEvidence(result *operator.Result, compact bool) error {
	dir := evidenceDir
	if dir == "" {
		dir = projectArtifactPath("artifacts/dm")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, result.RunID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create evidence file: %w", err)
	}
	result.Evidence = file.Name()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(result)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !compact {
		return nil
	}
	// Keep the complete envelope and its statuses. Limit only detail payloads;
	// the full result remains available through evidence, including every trace.
	for i := range result.Steps {
		if result.Steps[i].Details == nil {
			continue
		}
		data, err := json.Marshal(result.Steps[i].Details)
		if err != nil {
			return err
		}
		var details any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&details); err != nil {
			return err
		}
		result.Steps[i].Details = compactValue(details, &result.Truncated).(map[string]any)
	}
	return nil
}

func compactValue(value any, truncated *bool) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "full_trace", "traces", "logbook_entries", "error_log_lines", "output":
				delete(v, key)
				*truncated = true
			default:
				v[key] = compactValue(child, truncated)
			}
		}
	case []any:
		if len(v) > 10 {
			v = v[:10]
			*truncated = true
		}
		for i := range v {
			v[i] = compactValue(v[i], truncated)
		}
		return v
	case string:
		if len(v) > 1200 {
			*truncated = true
			return string([]rune(v)[:min(300, len([]rune(v)))]) + "…"
		}
	}
	return value
}
