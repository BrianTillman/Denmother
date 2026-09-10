package validator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/devname"
)

func TestCandidateHAContainerNamesUsesWorktreeContainer(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)

	got := candidateHAContainerNames(config)
	want := []string{devname.HomeAssistantContainer(filepath.Dir(config))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidateHAContainerNames() = %#v, want %#v", got, want)
	}
}

func TestCandidateHAContainerNamesHonorsEnvOverride(t *testing.T) {
	t.Setenv("HA_CONTAINER", "custom-ha")
	config := newSchemaConfigDir(t)

	got := candidateHAContainerNames(config)
	want := []string{"custom-ha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidateHAContainerNames() = %#v, want %#v", got, want)
	}
}

func TestCandidateHAContainerNamesDoesNotIncludeLegacyNames(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)

	got := candidateHAContainerNames(config)
	for _, name := range got {
		if name == "devcontainer-homeassistant" || name == "hass-dev-homeassistant" {
			t.Fatalf("legacy container %q must not be a default candidate: %#v", name, got)
		}
	}
}

func TestResolveRunningHAContainerIgnoresAnotherCheckout(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)
	want := devname.HomeAssistantContainer(filepath.Dir(config))

	oldCommandOutput := commandOutput
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		if len(args) >= 4 && (args[3] == "devcontainer-homeassistant" || args[3] == "hass-dev-homeassistant") {
			return []byte("true\n"), nil
		}
		if len(args) >= 4 && args[3] == want {
			return []byte("false\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	defer func() { commandOutput = oldCommandOutput }()

	got, checked, ok := resolveRunningHAContainer(config)
	if ok {
		t.Fatalf("another checkout satisfied schema container selection: got %q checked %#v", got, checked)
	}
	if len(checked) != 1 || checked[0] != want {
		t.Fatalf("checked = %#v, want only %q", checked, want)
	}
}

func TestResolveRunningHAContainerFindsWorktreeContainer(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)
	want := devname.HomeAssistantContainer(filepath.Dir(config))

	oldCommandOutput := commandOutput
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		if len(args) >= 4 && args[3] == want {
			return []byte("true\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	defer func() { commandOutput = oldCommandOutput }()

	got, checked, ok := resolveRunningHAContainer(config)
	if !ok {
		t.Fatal("expected running worktree container")
	}
	if got != want {
		t.Fatalf("container = %q, want %q", got, want)
	}
	if len(checked) != 1 || checked[0] != want {
		t.Fatalf("checked = %#v", checked)
	}
}

func TestValidateSchemaUsesWorktreeContainer(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)
	want := devname.HomeAssistantContainer(filepath.Dir(config))

	oldLookPath := lookPath
	oldCommandOutput := commandOutput
	oldCommandRunWith := commandRunWith

	lookPath = func(file string) (string, error) {
		return "/usr/bin/docker", nil
	}
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		if len(args) >= 4 && args[3] == want {
			return []byte("true\n"), nil
		}
		return nil, errors.New("unexpected inspect")
	}
	var usedArgs []string
	commandRunWith = func(ctx context.Context, name string, args []string, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
		usedArgs = append([]string(nil), args...)
		return nil
	}
	defer func() {
		lookPath = oldLookPath
		commandOutput = oldCommandOutput
		commandRunWith = oldCommandRunWith
	}()

	if err := validateSchema(config); err != nil {
		t.Fatalf("validateSchema returned error: %v", err)
	}
	if len(usedArgs) < 2 || usedArgs[1] != want {
		t.Fatalf("docker exec args = %#v, want container %q", usedArgs, want)
	}
}

func TestValidateSchemaIncompleteWhenDockerMissing(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)

	oldLookPath := lookPath
	lookPath = func(file string) (string, error) {
		return "", errors.New("docker missing")
	}
	defer func() { lookPath = oldLookPath }()

	err := validateSchema(config)
	if !IsIncomplete(err) {
		t.Fatalf("expected incomplete schema result, got %v", err)
	}
}

func TestValidateSchemaIncompleteWhenWorktreeContainerMissing(t *testing.T) {
	t.Setenv("HA_CONTAINER", "")
	config := newSchemaConfigDir(t)

	oldLookPath := lookPath
	oldCommandOutput := commandOutput
	lookPath = func(file string) (string, error) {
		return "/usr/bin/docker", nil
	}
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		return []byte("false\n"), nil
	}
	defer func() {
		lookPath = oldLookPath
		commandOutput = oldCommandOutput
	}()

	err := validateSchema(config)
	if !IsIncomplete(err) {
		t.Fatalf("expected incomplete schema result, got %v", err)
	}
}

func TestValidateSchemaRetainsActionableFindings(t *testing.T) {
	t.Setenv("HA_CONTAINER", "custom-ha")
	config := newSchemaConfigDir(t)

	oldLookPath := lookPath
	oldCommandOutput := commandOutput
	oldCommandRunWith := commandRunWith
	lookPath = func(file string) (string, error) {
		return "/usr/bin/docker", nil
	}
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		return []byte("true\n"), nil
	}
	commandRunWith = func(ctx context.Context, name string, args []string, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
		stderr.WriteString("Invalid config for [automation]: unknown device abc123\n")
		return errors.New("exit 1")
	}
	defer func() {
		lookPath = oldLookPath
		commandOutput = oldCommandOutput
		commandRunWith = oldCommandRunWith
		_ = os.Unsetenv("HA_CONTAINER")
	}()

	err := validateSchema(config)
	if err == nil {
		t.Fatal("expected schema failure")
	}
	if IsIncomplete(err) {
		t.Fatal("schema failure must not be reported as incomplete")
	}
	result := ResultFromError("schema", err)
	if result.Status != CheckFailed {
		t.Fatalf("status = %q", result.Status)
	}
	joined := strings.Join(result.Findings, "\n")
	if !strings.Contains(joined, "unknown device abc123") {
		t.Fatalf("findings missing source/reason: %#v", result.Findings)
	}
	if !strings.Contains(result.Summary, "unknown device abc123") {
		t.Fatalf("summary missing finding: %q", result.Summary)
	}
	if !strings.Contains(joined, "check_config output:") {
		t.Fatalf("findings missing raw output: %#v", result.Findings)
	}
}

func TestValidateSchemaRetainsEmptyOutputFailure(t *testing.T) {
	t.Setenv("HA_CONTAINER", "custom-ha")
	config := newSchemaConfigDir(t)

	oldLookPath := lookPath
	oldCommandOutput := commandOutput
	oldCommandRunWith := commandRunWith
	lookPath = func(file string) (string, error) {
		return "/usr/bin/docker", nil
	}
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "inspect" && args[2] == "{{json .Mounts}}" {
			return json.Marshal([]map[string]string{{"Source": config, "Destination": "/config"}})
		}
		return []byte("true\n"), nil
	}
	commandRunWith = func(ctx context.Context, name string, args []string, stdout *bytes.Buffer, stderr *bytes.Buffer) error {
		return errors.New("exit status 1")
	}
	defer func() {
		lookPath = oldLookPath
		commandOutput = oldCommandOutput
		commandRunWith = oldCommandRunWith
		_ = os.Unsetenv("HA_CONTAINER")
	}()

	err := validateSchema(config)
	if err == nil || IsIncomplete(err) {
		t.Fatalf("expected failed schema, got %v", err)
	}
	result := ResultFromError("schema", err)
	joined := strings.Join(result.Findings, "\n")
	if !strings.Contains(joined, "produced no output") {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func newSchemaConfigDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "ha-config")
	if err := os.Mkdir(config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	return config
}
