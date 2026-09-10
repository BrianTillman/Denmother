package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"

	"github.com/BrianTillman/Denmother/internal/devname"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	devJSON        bool
	devHAPort      int
	devMatterPort  int
	devPrepareOnly bool
)

const (
	devHomeAssistantImage = "ghcr.io/home-assistant/home-assistant:2026.9.1"
	devMatterServerImage  = "ghcr.io/matter-js/python-matter-server:8.1.2"
	devUIProxyImage       = "nginx:1.29.8-alpine"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Manage isolated local Home Assistant dev environments",
	Long: `Manage per-worktree Home Assistant dev environments.

These commands generate a compose file under .devcontainer/worktrees/ with
project-scoped container names and volumes. The generated environment reuses
the repository's devcontainer startup scripts, storage seed, mock entities, and
token generation flow.`,
}

var devUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Create and start the current worktree's local HA environment",
	RunE:  runDevUp,
}

var devDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the current worktree's local HA environment",
	RunE:  runDevDown,
}

var devResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Recreate the current worktree's local HA environment and volumes",
	RunE:  runDevReset,
}

var devStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Inspect the current worktree's local HA environment",
	RunE:  runDevStatus,
}

type devEnvironment struct {
	ProjectName  string `json:"project_name"`
	ConfigRoot   string `json:"config_root"`
	ProjectRoot  string `json:"project_root"`
	ComposeFile  string `json:"compose_file"`
	HAURL        string `json:"ha_url"`
	HAPort       int    `json:"ha_port"`
	MatterPort   int    `json:"matter_port"`
	TokenCommand string `json:"token_command"`
}

type devStatusSummary struct {
	Environment       *devEnvironment `json:"environment"`
	ComposeFileExists bool            `json:"compose_file_exists"`
	DockerAvailable   bool            `json:"docker_available"`
	RunningServices   []string        `json:"running_services,omitempty"`
	MissingServices   []string        `json:"missing_services,omitempty"`
	HAReachable       bool            `json:"ha_reachable"`
}

func init() {
	for _, cmd := range []*cobra.Command{devUpCmd, devDownCmd, devResetCmd, devStatusCmd} {
		cmd.Flags().BoolVar(&devJSON, "json", false, "Emit a machine-readable JSON summary")
	}
	for _, cmd := range []*cobra.Command{devUpCmd, devResetCmd} {
		cmd.Flags().IntVar(&devHAPort, "ha-port", 0, "Host port for Home Assistant; defaults to a worktree-derived port")
		cmd.Flags().IntVar(&devMatterPort, "matter-port", 0, "Host port for Matter server; defaults to a worktree-derived port")
		cmd.Flags().BoolVar(&devPrepareOnly, "prepare-only", false, "Write the compose file without running docker compose")
	}

	devCmd.AddCommand(devUpCmd)
	devCmd.AddCommand(devDownCmd)
	devCmd.AddCommand(devResetCmd)
	devCmd.AddCommand(devStatusCmd)
	rootCmd.AddCommand(devCmd)
}

func runDevUp(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devJSON, cmd.OutOrStdout())
	rt.SetProfile("up")

	env, err := prepareDevEnvironment()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "prepare-dev",
			Title:   "Prepare isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment preparation failed")
	}

	rt.AddStep(operator.Step{
		ID:        "prepare-dev",
		Title:     "Prepare isolated dev environment",
		Status:    operator.StatusSuccess,
		Summary:   fmt.Sprintf("prepared %s", env.ProjectName),
		Artifacts: []string{env.ComposeFile},
		Details: map[string]any{
			"environment": env,
		},
	})

	if !devPrepareOnly {
		if err := runDockerCompose(env, "up", "-d", "--force-recreate", "homeassistant", "ui-proxy"); err != nil {
			rt.AddStep(operator.Step{
				ID:      "start-dev",
				Title:   "Start isolated dev environment",
				Status:  operator.StatusFailure,
				Summary: err.Error(),
			})
			return rt.Complete(operator.StatusFailure, "dev environment start failed")
		}
		rt.AddStep(operator.Step{
			ID:      "start-dev",
			Title:   "Start isolated dev environment",
			Status:  operator.StatusSuccess,
			Summary: fmt.Sprintf("started %s at %s", env.ProjectName, env.HAURL),
			Mutates: true,
			Details: map[string]any{
				"environment": env,
			},
		})
	}

	if !devJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\nURL: %s\nCompose: %s\n", env.ProjectName, env.HAURL, env.ComposeFile)
	}
	if devPrepareOnly {
		return rt.Complete(operator.StatusSuccess, "development environment prepared without Docker execution")
	}
	if err := bootstrapPortableDev(cmd.Context(), env); err != nil {
		rt.AddStep(operator.Step{ID: "authenticate-dev", Title: "Authenticate local development HA", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "development containers started but readiness verification failed")
	}
	return rt.Complete(operator.StatusSuccess, fmt.Sprintf("development environment started at %s", env.HAURL))
}

func runDevDown(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devJSON, cmd.OutOrStdout())
	rt.SetProfile("down")

	env, err := currentDevEnvironment()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-dev",
			Title:   "Resolve isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment resolution failed")
	}
	if err := runDockerCompose(env, "down", "--remove-orphans"); err != nil {
		rt.AddStep(operator.Step{
			ID:      "stop-dev",
			Title:   "Stop isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment stop failed")
	}
	rt.AddStep(operator.Step{
		ID:      "stop-dev",
		Title:   "Stop isolated dev environment",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("stopped %s", env.ProjectName),
		Mutates: true,
		Details: map[string]any{
			"environment": env,
		},
	})
	return rt.Complete(operator.StatusSuccess, fmt.Sprintf("stopped dev environment %s", env.ProjectName))
}

func runDevReset(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devJSON, cmd.OutOrStdout())
	rt.SetProfile("reset")

	env, err := prepareDevEnvironment()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "prepare-dev",
			Title:   "Prepare isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment preparation failed")
	}

	rt.AddStep(operator.Step{
		ID:        "prepare-dev",
		Title:     "Prepare isolated dev environment",
		Status:    operator.StatusSuccess,
		Summary:   fmt.Sprintf("prepared %s", env.ProjectName),
		Artifacts: []string{env.ComposeFile},
		Details: map[string]any{
			"environment": env,
		},
	})

	// --prepare-only must never tear down volumes or start containers.
	if devPrepareOnly {
		return rt.Complete(operator.StatusSuccess, fmt.Sprintf("prepared reset compose for %s without docker execution", env.ProjectName))
	}

	if err := runDockerCompose(env, "down", "-v", "--remove-orphans"); err != nil {
		rt.AddStep(operator.Step{
			ID:      "reset-dev",
			Title:   "Reset isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment reset failed")
	}
	rt.AddStep(operator.Step{
		ID:      "reset-dev",
		Title:   "Reset isolated dev environment",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("removed containers and volumes for %s", env.ProjectName),
		Mutates: true,
		Details: map[string]any{
			"environment": env,
		},
	})
	if err := runDockerCompose(env, "up", "-d", "--force-recreate", "homeassistant", "ui-proxy"); err != nil {
		rt.AddStep(operator.Step{
			ID:      "restart-dev",
			Title:   "Restart isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment restart failed")
	}
	rt.AddStep(operator.Step{
		ID:      "restart-dev",
		Title:   "Restart isolated dev environment",
		Status:  operator.StatusSuccess,
		Summary: fmt.Sprintf("restarted %s at %s", env.ProjectName, env.HAURL),
		Mutates: true,
	})
	if err := bootstrapPortableDev(cmd.Context(), env); err != nil {
		rt.AddStep(operator.Step{ID: "authenticate-dev", Title: "Authenticate local development HA", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "development reset could not verify readiness")
	}
	return rt.Complete(operator.StatusSuccess, fmt.Sprintf("reset dev environment %s", env.ProjectName))
}

func runDevStatus(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("dev", devJSON, cmd.OutOrStdout())
	rt.SetProfile("status")

	env, err := currentDevEnvironment()
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-dev",
			Title:   "Resolve isolated dev environment",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "dev environment resolution failed")
	}

	summary := inspectDevStatus(env)
	status := operator.StatusSuccess
	stepSummary := fmt.Sprintf("%s is running at %s", env.ProjectName, env.HAURL)
	if !summary.HAReachable || len(summary.MissingServices) > 0 {
		status = operator.StatusPartial
		stepSummary = fmt.Sprintf("%s is not ready", env.ProjectName)
	}
	if !summary.ComposeFileExists {
		status = operator.StatusWarning
		stepSummary = fmt.Sprintf("%s has not been prepared", env.ProjectName)
	}
	if !summary.DockerAvailable && summary.ComposeFileExists {
		status = operator.StatusWarning
		stepSummary = "docker compose status is unavailable"
	}

	step := operator.Step{
		ID:      "dev-status",
		Title:   "Inspect isolated dev environment",
		Status:  status,
		Summary: stepSummary,
		Details: map[string]any{
			"status":        summary,
			"next_commands": []string{"./dm dev up --json", "./dm check --ensure-dev --json"},
		},
		NextCommands: []string{"./dm dev up --json", "./dm check --ensure-dev --json"},
	}
	if status != operator.StatusSuccess {
		step.Hints = append(step.Hints, "Run `./dm dev up --json` to prepare and start the worktree-isolated local HA environment.")
	}
	rt.AddStep(step)

	if !devJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\nURL: %s\nCompose: %s\n", env.ProjectName, env.HAURL, env.ComposeFile)
	}

	return rt.Complete(status, stepSummary)
}

func prepareDevEnvironment() (*devEnvironment, error) {
	env, err := currentDevEnvironment()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(env.ComposeFile), 0755); err != nil {
		return nil, err
	}
	if err := writePortableDevAssets(env); err != nil {
		return nil, err
	}
	compose, err := renderDevCompose(env)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(env.ComposeFile, []byte(compose), 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", env.ComposeFile, err)
	}
	return env, nil
}

func currentDevEnvironment() (*devEnvironment, error) {
	settings, err := selectedProject()
	if err != nil {
		return nil, err
	}
	root := settings.Root
	project := devProjectName(settings.RuntimeKey())
	basePort := 8200 + int(devname.Hash(settings.RuntimeKey())%500)
	haPort := devHAPort
	if haPort == 0 {
		haPort = basePort
	}
	matterPort := devMatterPort
	if matterPort == 0 {
		matterPort = basePort + 1000
	}
	composeFile := filepath.Join(root, ".devcontainer", "worktrees", project, "docker-compose.yml")
	return &devEnvironment{
		ProjectName:  project,
		ConfigRoot:   settings.ConfigDir,
		ProjectRoot:  settings.Root,
		ComposeFile:  composeFile,
		HAURL:        fmt.Sprintf("http://localhost:%d", haPort),
		HAPort:       haPort,
		MatterPort:   matterPort,
		TokenCommand: fmt.Sprintf("docker exec %s-homeassistant cat /run/hass/token.env", project),
	}, nil
}

func runDockerCompose(env *devEnvironment, args ...string) error {
	composeArgs := []string{"compose", "-p", env.ProjectName, "-f", env.ComposeFile}
	composeArgs = append(composeArgs, args...)
	command := exec.CommandContext(rootCmd.Context(), "docker", composeArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %w\n%s", strings.Join(composeArgs, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func inspectDevStatus(env *devEnvironment) devStatusSummary {
	summary := devStatusSummary{
		Environment:       env,
		ComposeFileExists: fileExists(env.ComposeFile),
		HAReachable:       devURLReachable(env.HAURL),
	}
	if !summary.ComposeFileExists {
		summary.MissingServices = []string{"homeassistant", "ui-proxy"}
		return summary
	}

	services, err := dockerComposeRunningServices(env)
	if err != nil {
		summary.DockerAvailable = false
		summary.MissingServices = []string{"homeassistant", "ui-proxy"}
		return summary
	}
	summary.DockerAvailable = true
	summary.RunningServices = services

	running := map[string]bool{}
	for _, service := range services {
		running[service] = true
	}
	for _, service := range []string{"homeassistant", "ui-proxy"} {
		if !running[service] {
			summary.MissingServices = append(summary.MissingServices, service)
		}
	}
	return summary
}

func dockerComposeRunningServices(env *devEnvironment) ([]string, error) {
	composeArgs := []string{"compose", "-p", env.ProjectName, "-f", env.ComposeFile, "ps", "--status", "running", "--services"}
	output, err := exec.CommandContext(rootCmd.Context(), "docker", composeArgs...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s failed: %w\n%s", strings.Join(composeArgs, " "), err, strings.TrimSpace(string(output)))
	}
	var services []string
	validServices := map[string]bool{
		"homeassistant": true,
		"matter-server": true,
		"ui-proxy":      true,
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if validServices[line] {
			services = append(services, line)
		}
	}
	sort.Strings(services)
	return services, nil
}

func devURLReachable(url string) bool {
	client := hahttp.NewClient(2 * time.Second)
	resp, err := client.Get(strings.TrimSuffix(url, "/") + "/api/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}

func repoRoot() (string, error) {
	ctx, cancel := context.WithTimeout(commandContext(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	command.WaitDelay = time.Second
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return "", cwdErr
	}
	return cwd, nil
}

func devProjectName(root string) string {
	return devname.Project(root)
}

func renderDevCompose(env *devEnvironment) (string, error) {
	return renderProjectDevCompose(env)

}

func devBindRepoRoot(repoRoot string) (string, error) {
	override := strings.TrimSpace(os.Getenv("DM_DEV_HOST_REPO_ROOT"))
	if override == "" {
		return repoRoot, nil
	}
	if !filepath.IsAbs(override) {
		return "", fmt.Errorf("DM_DEV_HOST_REPO_ROOT must be an absolute path")
	}
	return filepath.Clean(override), nil
}

func yamlQuote(path string) string {
	return fmt.Sprintf("%q", filepath.ToSlash(path))
}

func bindMount(source, target, mode string) string {
	return filepath.ToSlash(source) + ":" + target + ":" + mode
}
