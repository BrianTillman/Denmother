package haconfig

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"

	"github.com/BrianTillman/Denmother/internal/devname"
	"github.com/BrianTillman/Denmother/internal/project"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveLocalTokenWithDepsRetriesUntilTokenIsValid(t *testing.T) {
	t.Parallel()

	attempt := 0
	validateCalls := 0
	token, err := resolveLocalTokenWithDeps(
		"http://localhost:8123",
		[]tokenReader{
			func() (string, error) {
				attempt++
				if attempt < 3 {
					return "bootstrap-token", nil
				}
				return "api-token", nil
			},
		},
		func(_ string, token string) bool {
			validateCalls++
			return token == "api-token"
		},
		5,
		time.Millisecond,
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("resolveLocalTokenWithDeps returned error: %v", err)
	}
	if token != "api-token" {
		t.Fatalf("expected final valid token, got %q", token)
	}
	if attempt != 3 {
		t.Fatalf("expected 3 token read attempts, got %d", attempt)
	}
	if validateCalls != 2 {
		t.Fatalf("expected stale and replacement token to be validated once each, got %d call(s)", validateCalls)
	}
}

func TestResolveLocalTokenWithDepsRechecksSameToken(t *testing.T) {
	t.Parallel()

	validateCalls := 0
	token, err := resolveLocalTokenWithDeps(
		"http://localhost:8123",
		[]tokenReader{
			func() (string, error) {
				return "startup-token", nil
			},
		},
		func(_ string, token string) bool {
			validateCalls++
			return token == "startup-token" && validateCalls == 2
		},
		5,
		time.Millisecond,
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("resolveLocalTokenWithDeps returned error: %v", err)
	}
	if token != "startup-token" {
		t.Fatalf("expected startup token, got %q", token)
	}
	if validateCalls != 2 {
		t.Fatalf("expected token to be validated twice, got %d call(s)", validateCalls)
	}
}

func TestResolveLocalTokenWithDepsFailsWhenTokenNeverValid(t *testing.T) {
	t.Parallel()

	_, err := resolveLocalTokenWithDeps(
		"http://localhost:8123",
		[]tokenReader{
			func() (string, error) { return "bootstrap-token", nil },
		},
		func(_ string, _ string) bool { return false },
		2,
		time.Millisecond,
		func(time.Duration) {},
	)
	if err == nil {
		t.Fatal("expected error when token never becomes valid")
	}
	if !strings.Contains(err.Error(), "not yet valid") {
		t.Fatalf("expected not-yet-valid error, got %v", err)
	}
}

func TestResolveLocalTokenWithDepsFailsWhenReadersError(t *testing.T) {
	t.Parallel()

	_, err := resolveLocalTokenWithDeps(
		"http://localhost:8123",
		[]tokenReader{
			func() (string, error) { return "", errors.New("missing token file") },
			func() (string, error) { return "", errors.New("container not running") },
		},
		func(_ string, _ string) bool { return false },
		1,
		time.Millisecond,
		func(time.Duration) {},
	)
	if err == nil {
		t.Fatal("expected error when token readers fail")
	}
	if !strings.Contains(err.Error(), "could not read local token") {
		t.Fatalf("expected read failure error, got %v", err)
	}
}

func TestReadTokenFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "token.env")
	if err := os.WriteFile(path, []byte("HASS_BEARER_TOKEN=test-token\n"), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	token, err := ReadTokenFile(path)
	if err != nil {
		t.Fatalf("ReadTokenFile returned error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("expected token %q, got %q", "test-token", token)
	}
}

func TestResolveAutoDetectedLocalConfigSkipsReachableCandidateWithInvalidToken(t *testing.T) {
	t.Parallel()

	candidates := []localDevCandidate{
		{
			URL:           "http://localhost:8601",
			ContainerName: "hass-dev-stale-homeassistant",
		},
		{
			URL:           "http://localhost:8602",
			ContainerName: "hass-dev-current-homeassistant",
		},
	}

	config, err := resolveAutoDetectedLocalConfigWithDeps(localAutoDetectDeps{
		Reachable: func(string) bool { return true },
		Discover:  func() []localDevCandidate { return candidates },
		ResolveToken: func(candidate localDevCandidate) (string, error) {
			if candidate.ContainerName == "hass-dev-current-homeassistant" {
				return "current-token", nil
			}
			return "", fmt.Errorf("token for %s did not authenticate", candidate.URL)
		},
	})
	if err != nil {
		t.Fatalf("resolveAutoDetectedLocalConfigWithDeps returned error: %v", err)
	}
	if config.URL != "http://localhost:8602" {
		t.Fatalf("url = %q, want current reachable candidate", config.URL)
	}
	if config.Token != "current-token" {
		t.Fatalf("token = %q, want current-token", config.Token)
	}
}

func TestResolveInstanceConfigPreservesDevAutoDetectTokenError(t *testing.T) {
	t.Setenv("HASS_DEV_URL", "")
	t.Setenv("HASS_DEV_TOKEN", "")
	t.Setenv("HASS_SERVER", "")
	t.Setenv("HASS_BEARER_TOKEN", "")

	_, err := resolveInstanceConfigWithDeps(
		InstanceDev,
		InstanceFlags{},
		func() (*HAConfig, error) {
			return nil, fmt.Errorf("local Home Assistant candidates were reachable but no token authenticated")
		},
	)
	if err == nil {
		t.Fatal("expected development auto-detect error")
	}
	if !strings.Contains(err.Error(), "no development HA URL specified") {
		t.Fatalf("expected development URL context, got %v", err)
	}
	if !strings.Contains(err.Error(), "no token authenticated") {
		t.Fatalf("expected token-auth diagnostic to be preserved, got %v", err)
	}
}

func TestDetectLocalURLUsesReachableWorktreeDevCandidate(t *testing.T) {
	t.Parallel()

	candidates := []localDevCandidate{
		{
			URL:           "http://localhost:8639",
			ContainerName: "hass-dev-example-e15e7503-homeassistant",
		},
	}
	got, err := detectLocalURLWithDeps(
		"",
		func(url string) bool { return url == "http://localhost:8639" },
		func() []localDevCandidate { return candidates },
	)
	if err != nil {
		t.Fatalf("detectLocalURLWithDeps returned error: %v", err)
	}
	if got != "http://localhost:8639" {
		t.Fatalf("url = %q, want %q", got, "http://localhost:8639")
	}
}

func TestParseLocalDevComposePublishedPortWithBindAddress(t *testing.T) {
	for _, tc := range []struct{ mapping, want string }{
		{`"127.0.0.1:8123:80"`, "http://localhost:8123"},
		{`"127.0.0.1:8123:8123"`, "http://localhost:8123"},
		{`'127.0.0.1:80:80'`, "http://localhost:80"},
		{`127.0.0.1:8639:80`, "http://localhost:8639"},
		{`"0.0.0.0:8123:80"`, "http://localhost:8123"},
		{`"[::1]:8123:80"`, "http://localhost:8123"},
		{`"8639:80"`, "http://localhost:8639"},
		{`"8639:8123/tcp"`, "http://localhost:8639"},
	} {
		t.Run(tc.mapping, func(t *testing.T) {
			compose := "services:\n  homeassistant:\n    container_name: hass-dev-example-homeassistant\n  ui-proxy:\n    ports:\n      - " + tc.mapping + "\n"
			got, ok := parseLocalDevCompose(compose)
			if !ok || got.URL != tc.want {
				t.Fatalf("URL = %q, want %q", got.URL, tc.want)
			}
		})
	}
}

func TestDiscoverLocalDevCandidatesFromWorktreeCompose(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	composeDir := filepath.Join(root, ".devcontainer", "worktrees", "hass-dev-example-e15e7503")
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		t.Fatalf("create compose dir: %v", err)
	}
	compose := `services:
  homeassistant:
    container_name: hass-dev-example-e15e7503-homeassistant
  ui-proxy:
    container_name: hass-dev-example-e15e7503-ui-proxy
    ports:
      - "8639:80"
  matter-server:
    container_name: hass-dev-example-e15e7503-matter-server
    ports:
      - "9639:5580"
`
	if err := os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	candidates := discoverLocalDevCandidatesFromRoot(root)
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %#v", len(candidates), candidates)
	}
	if candidates[0].URL != "http://localhost:8639" {
		t.Fatalf("url = %q, want %q", candidates[0].URL, "http://localhost:8639")
	}
	if candidates[0].ContainerName != "hass-dev-example-e15e7503-homeassistant" {
		t.Fatalf("container = %q, want worktree HA container", candidates[0].ContainerName)
	}
}

func TestReadTokenFromContainerUsesDiscoveredWorktreeContainer(t *testing.T) {
	t.Parallel()

	token, err := readTokenFromContainerWithDeps(
		[]string{
			"devcontainer-homeassistant",
			"hass-dev-homeassistant",
			"hass-dev-example-e15e7503-homeassistant",
		},
		func(_ string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "exec" && args[1] == "hass-dev-example-e15e7503-homeassistant" {
				return []byte("HASS_BEARER_TOKEN=worktree-token\n"), nil
			}
			return nil, errors.New("container not found")
		},
	)
	if err != nil {
		t.Fatalf("readTokenFromContainerWithDeps returned error: %v", err)
	}
	if token != "worktree-token" {
		t.Fatalf("token = %q, want %q", token, "worktree-token")
	}
}

func clearInstanceTestEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HASS_DEV_URL", "HASS_DEV_TOKEN", "HASS_SERVER", "HASS_BEARER_TOKEN", "HASS_PROD_URL", "HASS_PROD_TOKEN", "HASS_URL", "HASS_TOKEN"} {
		t.Setenv(name, "")
	}
}

func TestExplicitDevURLUsesMatchingProjectCredential(t *testing.T) {
	clearInstanceTestEnvironment(t)
	for _, flags := range []InstanceFlags{
		{ConfigPath: "/selected/ha-config", DevURL: "http://localhost:8216"},
		{ProjectRoot: "/selected", DevURL: "http://localhost:8216/"},
		{ConfigPath: "/selected/ha-config", LegacyURL: "http://localhost:8216"},
	} {
		calls := 0
		got, err := resolveInstanceConfigWithDeps(InstanceDev, flags, func() (*HAConfig, error) {
			calls++
			return &HAConfig{URL: "http://localhost:8216", Token: "project-token", IsLocal: true}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		requested := flags.DevURL
		if requested == "" {
			requested = flags.LegacyURL
		}
		if calls != 1 || got.URL != requested || got.Token != "project-token" || !got.IsLocal {
			t.Fatalf("incorrect project credential resolution: %+v; calls=%d", got, calls)
		}
	}
	t.Setenv("HASS_DEV_URL", "http://localhost:8216")
	got, err := resolveInstanceConfigWithDeps(InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config"}, func() (*HAConfig, error) {
		return &HAConfig{URL: "http://localhost:8216", Token: "project-token", IsLocal: true}, nil
	})
	if err != nil || got.Token != "project-token" {
		t.Fatalf("environment URL did not reuse matching project token: %+v %v", got, err)
	}
}

func TestExplicitDevURLNeverReusesDifferentProjectEndpoint(t *testing.T) {
	clearInstanceTestEnvironment(t)
	for _, candidate := range []*HAConfig{
		{URL: "http://localhost:8217", Token: "other-port"},
		{URL: "https://localhost:8216", Token: "other-scheme"},
		{URL: "http://localhost:8216/proxy", Token: "other-path"},
		{URL: "http://127.0.0.1:8216", Token: "different-host"},
		{URL: "http://localhost:8216"},
		nil,
	} {
		got, err := resolveInstanceConfigWithDeps(InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", DevURL: "http://localhost:8216"}, func() (*HAConfig, error) { return candidate, nil })
		if err == nil || got != nil || !strings.Contains(err.Error(), "--dev-token") {
			t.Fatalf("accepted mismatched credential: %+v %v", got, err)
		}
	}
	sentinel := errors.New("selected runtime unavailable")
	got, err := resolveInstanceConfigWithDeps(InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", DevURL: "http://localhost:8216"}, func() (*HAConfig, error) { return nil, sentinel })
	if got != nil || !errors.Is(err, sentinel) {
		t.Fatalf("lost scoped resolver failure: %+v %v", got, err)
	}
}

func TestExplicitInstanceTokensBypassProjectDiscovery(t *testing.T) {
	clearInstanceTestEnvironment(t)
	detect := func() (*HAConfig, error) { t.Fatal("explicit credential must bypass discovery"); return nil, nil }
	for _, tc := range []struct {
		instance Instance
		flags    InstanceFlags
	}{
		{InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", DevURL: "http://localhost:9000", DevToken: "explicit"}},
		{InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", DevURL: "https://dev.example.com", DevToken: "explicit"}},
		{InstanceProd, InstanceFlags{ConfigPath: "/selected/ha-config", ProdURL: "http://localhost:9000", ProdToken: "explicit"}},
		{InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", LegacyURL: "http://localhost:9000", LegacyToken: "explicit"}},
	} {
		got, err := resolveInstanceConfigWithDeps(tc.instance, tc.flags, detect)
		if err != nil || got.Token != "explicit" {
			t.Fatalf("explicit token rejected: %+v %v", got, err)
		}
	}
	t.Setenv("HASS_DEV_TOKEN", "environment")
	got, err := resolveInstanceConfigWithDeps(InstanceDev, InstanceFlags{ConfigPath: "/selected/ha-config", DevURL: "http://localhost:9000"}, detect)
	if err != nil || got.Token != "environment" {
		t.Fatalf("environment token rejected: %+v %v", got, err)
	}
}

func TestProjectProductionLoopbackDoesNotInheritDevelopmentToken(t *testing.T) {
	clearInstanceTestEnvironment(t)
	got, err := resolveInstanceConfigWithDeps(InstanceProd, InstanceFlags{ConfigPath: "/selected/ha-config", ProdURL: "http://localhost:8216"}, func() (*HAConfig, error) {
		t.Fatal("production must not discover a development token")
		return nil, nil
	})
	if got != nil || err == nil || !strings.Contains(err.Error(), "--prod-token") {
		t.Fatalf("production inherited development token: %+v %v", got, err)
	}
}

func TestExplicitDevURLResolvesSelectedRuntimeTokenFile(t *testing.T) {
	clearInstanceTestEnvironment(t)
	var unrelatedRequests atomic.Int32
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unrelatedRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unrelated.Close()
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer selected-token" {
			t.Error("wrong project credential sent")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer selected.Close()
	root := t.TempDir()
	config := filepath.Join(root, "ha-config")
	if err := os.MkdirAll(config, 0755); err != nil {
		t.Fatal(err)
	}
	settings, err := project.ForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	writeRuntime := func(name, serverURL, token string) string {
		parsed, err := url.Parse(serverURL)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, ".devcontainer", "worktrees", strings.TrimSuffix(name, "-homeassistant"))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		compose := fmt.Sprintf("services:\n  homeassistant:\n    container_name: %s\n  ui-proxy:\n    ports:\n      - \"127.0.0.1:%s:80\"\n", name, parsed.Port())
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "token.env"), []byte("HASS_BEARER_TOKEN="+token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return "http://localhost:" + parsed.Port()
	}
	writeRuntime("aaa-unrelated-homeassistant", unrelated.URL, "unrelated-token")
	selectedURL := writeRuntime(devname.HomeAssistantContainer(settings.RuntimeKey()), selected.URL, "selected-token")
	resolved, err := ResolveInstanceConfig(InstanceDev, InstanceFlags{ConfigPath: config, DevURL: selectedURL})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.URL != selectedURL || resolved.Token != "selected-token" {
		t.Fatalf("wrong selected runtime: %+v", resolved)
	}
	if unrelatedRequests.Load() != 0 {
		t.Fatalf("probed an unrelated runtime %d times", unrelatedRequests.Load())
	}
}
