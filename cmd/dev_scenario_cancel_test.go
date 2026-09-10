package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

type scenarioCommandTransport func(*http.Request) (*http.Response, error)

func (f scenarioCommandTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type scenarioCancelOnClose struct {
	io.Reader
	cancel context.CancelFunc
}

func (body scenarioCancelOnClose) Close() error {
	body.cancel()
	return nil
}

func setupScenarioCommandTest(t *testing.T, content string) {
	t.Helper()
	oldJSON, oldList := devScenarioJSON, devScenarioList
	oldURL, oldToken := devScenarioDevURL, devScenarioDevToken
	oldConfig, oldExplicit := configPath, configExplicit
	oldCompact, oldEvidence, oldTransport := compactOutput, evidenceDir, http.DefaultTransport
	t.Cleanup(func() {
		devScenarioJSON, devScenarioList = oldJSON, oldList
		devScenarioDevURL, devScenarioDevToken = oldURL, oldToken
		configPath, configExplicit = oldConfig, oldExplicit
		compactOutput, evidenceDir, http.DefaultTransport = oldCompact, oldEvidence, oldTransport
	})
	for _, key := range []string{"HASS_DEV_URL", "HASS_DEV_TOKEN", "HASS_SERVER", "HASS_BEARER_TOKEN", "HASS_PROD_URL", "HASS_PROD_TOKEN", "HASS_URL", "HASS_TOKEN"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	configPath, configExplicit = filepath.Join(root, "config"), true
	devScenarioJSON, devScenarioList = true, false
	devScenarioDevURL, devScenarioDevToken = "https://scenario.example.test/ha", "synthetic-scenario-token"
	compactOutput, evidenceDir = false, ""
	writeTestFile(t, filepath.Join(root, ".denmother.yaml"), "version: 1\nconfig_dir: config\ndev_scenarios: cases/scenarios.json\n")
	writeTestFile(t, filepath.Join(configPath, "configuration.yaml"), "default_config:\n")
	writeTestFile(t, filepath.Join(root, "cases/scenarios.json"), content)
}

func executeScenarioFailure(t *testing.T, ctx context.Context, name string) operator.Result {
	t.Helper()
	command := &cobra.Command{}
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetOut(&output)
	if err := runDevScenario(command, []string{name}); err == nil {
		t.Fatalf("expected command failure, got success: %s", output.String())
	}
	var result operator.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("expected one JSON envelope: %v: %s", err, output.String())
	}
	if result.SchemaVersion != operator.SchemaVersion || result.Status != operator.StatusFailure || result.ExitCode == 0 {
		t.Fatalf("failure envelope disagrees with command outcome: %+v", result)
	}
	if bytes.Contains(output.Bytes(), []byte("synthetic-scenario-token")) {
		t.Fatal("scenario output disclosed a credential")
	}
	return result
}

func TestScenarioCommandCancellationAfterSuccessfulStateIsIncomplete(t *testing.T) {
	setupScenarioCommandTest(t, `{"evening":{"states":{"sensor.a":{"state":"21"},"sensor.b":{"state":"22"}}}}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	http.DefaultTransport = scenarioCommandTransport(func(request *http.Request) (*http.Response, error) {
		writes++
		if request.Method != http.MethodPost || request.URL.Path != "/ha/api/states/sensor.a" {
			t.Errorf("unexpected state mutation: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer synthetic-scenario-token" {
			t.Error("scenario omitted selected development credentials")
		}
		var state devScenarioState
		if err := json.NewDecoder(request.Body).Decode(&state); err != nil || state.State != "21" || state.Attributes["denmother_source"] != "scenario" {
			t.Errorf("wrong scenario state/provenance: %+v: %v", state, err)
		}
		// Cancel after a successful HTTP response, exactly when postHAJSONContext
		// closes it, to reproduce cancellation between writes.
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: scenarioCancelOnClose{Reader: strings.NewReader(""), cancel: cancel}, Request: request}, nil
	})
	result := executeScenarioFailure(t, ctx, "evening")
	if writes != 1 {
		t.Fatalf("cancellation should stop remaining writes, got %d", writes)
	}
	if len(result.Steps) != 1 || result.Steps[0].ID != "apply-scenario" || result.Steps[0].Status != operator.StatusFailure {
		t.Fatalf("missing apply failure: %+v", result.Steps)
	}
	step := result.Steps[0]
	if step.Details["applied_count"] != float64(1) || !strings.Contains(step.Summary, "1 of 2") || !strings.Contains(fmt.Sprint(step.Details["failures"]), "context canceled") {
		t.Fatalf("partial application not reported accurately: %+v", step)
	}
}

func TestScenarioCommandDoesNotFallBackToProductionCredentials(t *testing.T) {
	setupScenarioCommandTest(t, `{"evening":{"states":{"sensor.a":{"state":"21"}}}}`)
	devScenarioDevURL, devScenarioDevToken = "", ""
	for _, key := range []string{"HASS_PROD_URL", "HASS_URL"} {
		t.Setenv(key, "https://production.example.test")
	}
	for _, key := range []string{"HASS_PROD_TOKEN", "HASS_TOKEN"} {
		t.Setenv(key, "synthetic-scenario-token")
	}
	requests := 0
	http.DefaultTransport = scenarioCommandTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})
	result := executeScenarioFailure(t, context.Background(), "evening")
	if requests != 0 {
		t.Fatalf("production credentials caused %d HTTP request(s)", requests)
	}
	if result.Target != nil || len(result.Steps) != 1 || result.Steps[0].ID != "resolve-dev" {
		t.Fatalf("must fail development resolution before selecting or mutating production: %+v", result)
	}
	for _, name := range []string{"prod-url", "prod-token", "allow-prod"} {
		if devScenarioCmd.Flags().Lookup(name) != nil {
			t.Errorf("scenario unexpectedly exposes production override --%s", name)
		}
	}
}

func TestScenarioCommandMalformedFileEmitsJSONBeforeAnyRequest(t *testing.T) {
	setupScenarioCommandTest(t, `{"evening":{"states":`)
	requests := 0
	http.DefaultTransport = scenarioCommandTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})
	result := executeScenarioFailure(t, context.Background(), "evening")
	if requests != 0 || result.Target != nil || len(result.Steps) != 1 || result.Steps[0].ID != "load-scenarios" {
		t.Fatalf("malformed scenarios must fail before target resolution or HTTP: requests=%d result=%+v", requests, result)
	}
	if !strings.Contains(result.Steps[0].Summary, "parse scenarios") {
		t.Fatalf("missing parse error details: %+v", result.Steps[0])
	}
}
