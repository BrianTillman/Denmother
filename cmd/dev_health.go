package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"
	"github.com/BrianTillman/Denmother/internal/operator"
)

type devImageStatus struct {
	Desired   string `json:"desired"`
	Running   string `json:"running"`
	DesiredID string `json:"desired_id,omitempty"`
	RunningID string `json:"running_id,omitempty"`
	Verified  bool   `json:"verified"`
	Drift     bool   `json:"drift"`
	Error     string `json:"error,omitempty"`
}

type devDockerOutput func(context.Context, ...string) ([]byte, error)

func runDevDocker(ctx context.Context, args ...string) ([]byte, error) {
	// Never include raw Compose output or token-file contents in diagnostics.
	return exec.CommandContext(ctx, "docker", args...).Output()
}

func inspectDevImage(ctx context.Context, env *devEnvironment, run devDockerOutput) devImageStatus {
	var result devImageStatus
	output, err := run(ctx, "compose", "-p", env.ProjectName, "-f", env.ComposeFile, "config", "--format", "json")
	if err != nil {
		result.Error = "could not resolve the desired Home Assistant image"
		return result
	}
	var config struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if json.Unmarshal(output, &config) != nil || config.Services["homeassistant"].Image == "" {
		result.Error = "resolved Compose configuration has no Home Assistant image"
		return result
	}
	result.Desired = config.Services["homeassistant"].Image
	output, err = run(ctx, "inspect", "--format", "{{json .Config.Image}} {{json .Image}}", env.ProjectName+"-homeassistant")
	if err != nil {
		result.Error = "could not inspect the selected Home Assistant container image"
		return result
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	if decoder.Decode(&result.Running) != nil || decoder.Decode(&result.RunningID) != nil || result.Running == "" || result.RunningID == "" {
		result.Error = "selected container returned no image identity"
		return result
	}
	result.Drift = result.Desired != result.Running
	output, err = run(ctx, "image", "inspect", "--format", "{{.Id}}", result.Desired)
	if err != nil || strings.TrimSpace(string(output)) == "" {
		result.Error = "desired Home Assistant image is not available locally for identity comparison"
		return result
	}
	result.DesiredID = strings.TrimSpace(string(output))
	result.Drift = result.Drift || result.DesiredID != result.RunningID
	result.Verified = true
	return result
}

// An unauthenticated frontend response establishes reachability only. Read the
// token exclusively from this worktree's container, then require HA's startup
// barrier and reject safe/recovery mode before reporting readiness.
func inspectDevReadiness(ctx context.Context, env *devEnvironment, run devDockerOutput) (authenticated, running, ready bool, reason string) {
	output, err := run(ctx, "exec", env.ProjectName+"-homeassistant", "cat", "/run/hass/token.env")
	if err != nil {
		return false, false, false, "could not read the selected local runtime token"
	}
	var token string
	for _, line := range strings.Split(string(output), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "HASS_BEARER_TOKEN="); ok {
			token = strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	if token == "" {
		return false, false, false, "selected local runtime has no confirmed token"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(env.HAURL, "/")+"/api/config", nil)
	if err != nil {
		return false, false, false, "invalid local runtime URL"
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := hahttp.NewClient(2 * time.Second).Do(request)
	if err != nil {
		return false, false, false, "authenticated local readiness request failed"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, false, false, fmt.Sprintf("local readiness returned HTTP %d", response.StatusCode)
	}
	var config struct {
		State        string `json:"state"`
		SafeMode     bool   `json:"safe_mode"`
		RecoveryMode bool   `json:"recovery_mode"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&config) != nil || config.State == "" {
		return false, false, false, "local readiness returned invalid HA configuration"
	}
	running = strings.EqualFold(config.State, "RUNNING")
	if config.SafeMode || config.RecoveryMode {
		return true, running, false, "local HA is in safe/recovery mode"
	}
	if !running {
		return true, false, false, "local HA integrations have not reached RUNNING"
	}
	return true, true, true, ""
}

func devStatusOutcome(summary devStatusSummary) (operator.Status, string) {
	env := summary.Environment
	switch {
	case !summary.ComposeFileExists:
		return operator.StatusWarning, fmt.Sprintf("%s has not been prepared", env.ProjectName)
	case !summary.DockerAvailable:
		return operator.StatusWarning, "docker compose status is unavailable"
	case summary.HAImage.Drift:
		return operator.StatusFailure, fmt.Sprintf("%s Home Assistant image drift: desired %s, running %s", env.ProjectName, summary.HAImage.Desired, summary.HAImage.Running)
	case len(summary.MissingServices) > 0 || !summary.HAReachable || !summary.HAReady || !summary.HAAuthenticated || !summary.HARunning || !summary.HAImage.Verified:
		return operator.StatusPartial, fmt.Sprintf("%s is not ready", env.ProjectName)
	default:
		return operator.StatusSuccess, fmt.Sprintf("%s is authenticated and running at %s with the desired image", env.ProjectName, env.HAURL)
	}
}
