package validator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/devname"
	"github.com/BrianTillman/Denmother/internal/project"
	"github.com/fatih/color"
)

var (
	lookPath      = exec.LookPath
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, err
	}
	commandRunWith = func(ctx context.Context, name string, args []string, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.WaitDelay = time.Second
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
)

func validateSchema(configPath string) error {
	return checkSchema(configPath).Err()
}

// checkSchema runs Home Assistant's check_config against this checkout's runtime.
func checkSchema(configPath string) CheckResult {
	return checkSchemaContext(context.Background(), configPath)
}

func checkSchemaContext(ctx context.Context, configPath string) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ResultFromError("schema", err)
	}
	color.New(color.FgBlue).Println("Validating Home Assistant schema...")
	fmt.Println()

	details := map[string]any{
		"config_path": configPath,
	}
	worktreeContainer := worktreeHAContainerName(configPath)
	if worktreeContainer != "" {
		details["worktree_container"] = worktreeContainer
	}

	if _, err := lookPath("docker"); err != nil {
		summary := "schema validation incomplete: docker not found"
		color.Yellow("  %s Docker not found, schema validation incomplete", warningMark)
		fmt.Println("    Start the worktree HA with `./dm dev up --json` or set HA_CONTAINER")
		fmt.Println()
		return incompleteResult("schema", summary, []string{summary}, details)
	}

	containerName, checkedContainers, probeErr := resolveRunningHAContainerContext(ctx, configPath)
	details["checked_containers"] = checkedContainers
	if err := ctx.Err(); err != nil {
		return ResultFromError("schema", err)
	}
	if probeErr != nil {
		return ResultFromError("schema", probeErr)
	}
	if containerName == "" {
		summary := "schema validation incomplete: no matching Home Assistant container running"
		if worktreeContainer != "" {
			summary = fmt.Sprintf("schema validation incomplete: worktree container %s is not running", worktreeContainer)
		}
		color.Yellow("  %s No matching Home Assistant container running, schema validation incomplete", warningMark)
		fmt.Printf("    Checked: %s\n", strings.Join(checkedContainers, ", "))
		fmt.Println("    Start this worktree with `./dm dev up --json` or set HA_CONTAINER to this checkout's container")
		fmt.Println()
		return incompleteResult("schema", summary, []string{summary}, details)
	}
	details["container"] = containerName
	if err := verifySchemaSourceContext(ctx, configPath, containerName); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ResultFromError("schema", err)
		}
		return incompleteResult("schema", err.Error(), []string{err.Error()}, details)
	}

	var stdout, stderr bytes.Buffer
	err := commandRunWith(ctx, "docker", []string{
		"exec", containerName,
		"python3", "-m", "homeassistant", "--script", "check_config", "-c", "/config",
	}, &stdout, &stderr)
	if ctx.Err() != nil {
		return ResultFromError("schema", ctx.Err())
	}
	output := stdout.String() + stderr.String()
	findings := schemaFindings(output)
	if len(output) > 0 {
		details["output"] = truncateSchemaOutput(output)
	}

	if err != nil || containsErrors(output) {
		if len(findings) == 0 {
			if err != nil {
				findings = []string{fmt.Sprintf("check_config failed in %s: %s", containerName, err.Error())}
			} else {
				findings = []string{fmt.Sprintf("check_config reported invalid configuration in %s", containerName)}
			}
		}
		if snippet := truncateSchemaOutput(output); snippet != "" {
			findings = append(findings, "check_config output:", snippet)
		} else {
			findings = append(findings, "check_config produced no output")
		}
		summary := fmt.Sprintf("schema validation failed in %s: %s", containerName, findings[0])
		color.Red("  %s Home Assistant schema validation failed:", crossMark)
		fmt.Println()
		for _, line := range findings {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
		return failedResult("schema", summary, findings, details)
	}

	color.Green("  %s Home Assistant schema validation passed", checkMark)
	fmt.Println()
	return CheckResult{
		Name:    "schema",
		Status:  CheckPassed,
		Summary: fmt.Sprintf("schema validation passed in %s", containerName),
		Details: details,
	}
}

func resolveRunningHAContainer(configPath string) (string, []string, bool) {
	name, names, err := resolveRunningHAContainerContext(context.Background(), configPath)
	return name, names, err == nil && name != ""
}

func resolveRunningHAContainerContext(ctx context.Context, configPath string) (string, []string, error) {
	containerNames := candidateHAContainerNames(configPath)
	for _, name := range containerNames {
		if ctx.Err() != nil {
			return "", containerNames, ctx.Err()
		}
		output, err := commandOutput(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", containerNames, err
		}
		if err == nil && strings.TrimSpace(string(output)) == "true" {
			return name, containerNames, nil
		}
	}
	return "", containerNames, nil
}

func candidateHAContainerNames(configPath string) []string {
	if containerName := strings.TrimSpace(os.Getenv("HA_CONTAINER")); containerName != "" {
		return []string{containerName}
	}
	if name := worktreeHAContainerName(configPath); name != "" {
		return []string{name}
	}
	return nil
}

func worktreeHAContainerName(configPath string) string {
	settings, err := project.ForConfig(configPath)
	if err != nil {
		return ""
	}
	root := settings.RuntimeKey()
	if root == "" {
		return ""
	}
	return devname.HomeAssistantContainer(root)
}

func schemaFindings(output string) []string {
	var findings []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "Invalid config") ||
			strings.Contains(line, "ERROR") ||
			strings.Contains(line, "invalid option") ||
			strings.Contains(line, "is an invalid") ||
			strings.Contains(line, "not a valid") ||
			strings.Contains(line, "required key") ||
			strings.Contains(line, "unknown") ||
			strings.Contains(line, "extra keys not allowed") {
			findings = append(findings, line)
		}
	}
	return findings
}

func truncateSchemaOutput(output string) string {
	const limit = 4000
	output = strings.TrimSpace(output)
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "\n...[truncated]..."
}

func containsErrors(output string) bool {
	errorIndicators := []string{
		"Invalid config",
		"is an invalid option",
		"not a valid value",
		"required key not provided",
		"ERROR",
		"extra keys not allowed",
	}

	for _, indicator := range errorIndicators {
		if strings.Contains(output, indicator) {
			return true
		}
	}
	return false
}

const warningMark = "⚠"
