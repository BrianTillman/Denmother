// Package haconfig provides utilities for resolving Home Assistant connection configuration.
package haconfig

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/devname"
	"github.com/BrianTillman/Denmother/internal/hahttp"
	"github.com/BrianTillman/Denmother/internal/project"
)

const (
	// DefaultTokenFile is the path to the auto-generated token in devcontainer
	DefaultTokenFile = "/run/hass/token.env"

	// LocalContainerURL is the fallback HA service URL on the Compose network.
	LocalContainerURL = "http://homeassistant:8123"

	// LocalHostURL is the fallback host URL when no project port is discovered.
	LocalHostURL = "http://localhost:8123"

	// localTokenValidationAttempts bounds retries while waiting for a usable local API token.
	localTokenValidationAttempts = 25
)

// Match the complete short port mapping, including an optional bind address.
// Otherwise 127.0.0.1:8123:80 is misread as host port 1 from the substring 1:8123.
var composePortRe = regexp.MustCompile(`(?:^|[\s"'])(?:[0-9.]+:|\[[0-9a-fA-F:]+\]:)?([0-9]+):(?:8123|80)(?:/tcp)?(?:[\s"']|$)`)

// Instance represents which HA instance to connect to
type Instance string

const (
	// InstanceProd represents the production Home Assistant instance
	InstanceProd Instance = "production"
	// InstanceDev represents the development Home Assistant instance
	InstanceDev Instance = "development"
)

// InstanceFlags holds the CLI flags for both instances
type InstanceFlags struct {
	ProjectRoot string
	ConfigPath  string
	ProdURL     string
	ProdToken   string
	DevURL      string
	DevToken    string
	LegacyURL   string
	LegacyToken string
}

// HAConfig holds the resolved Home Assistant connection configuration
type HAConfig struct {
	URL     string
	Token   string
	IsLocal bool   // True if using local dev container
	Source  string // Description of where config came from
}

type localDevCandidate struct {
	URL           string
	ContainerName string
}

// ResolveConfigContext selects a URL from the flag, a reachable HASS_SERVER,
// HASS_URL, or local discovery, in that order. Token precedence is the flag,
// HASS_TOKEN, HASS_BEARER_TOKEN, then local token discovery.
func ResolveConfigContext(ctx context.Context, flagURL, flagToken string) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	config := &HAConfig{}

	if flagURL != "" {
		config.URL = flagURL
		config.Source = "command-line flag"
		config.IsLocal = isLocalURL(flagURL)
	} else if serverURL := os.Getenv("HASS_SERVER"); serverURL != "" && isHAReachableContext(ctx, serverURL) {
		config.URL = serverURL
		config.Source = "HASS_SERVER environment variable"
		config.IsLocal = true
	} else if envURL := os.Getenv("HASS_URL"); envURL != "" {
		config.URL = envURL
		config.Source = "HASS_URL environment variable"
		config.IsLocal = isLocalURL(envURL)
	} else {
		if flagToken == "" && os.Getenv("HASS_TOKEN") == "" && os.Getenv("HASS_BEARER_TOKEN") == "" {
			localConfig, err := resolveAutoDetectedLocalConfigContext(ctx)
			if err != nil {
				return nil, fmt.Errorf("no HA URL specified and local dev container not available: %w\n\n"+
					"Usage:\n"+
					"  --ha-url URL           Specify Home Assistant URL\n"+
					"  HASS_URL env var       Set environment variable\n"+
					"  (or start local dev)   cd .devcontainer && docker-compose up -d", err)
			}
			return localConfig, nil
		}

		localURL, err := detectLocalURLContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("no HA URL specified and local dev container not available: %w\n\n"+
				"Usage:\n"+
				"  --ha-url URL           Specify Home Assistant URL\n"+
				"  HASS_URL env var       Set environment variable\n"+
				"  (or start local dev)   cd .devcontainer && docker-compose up -d", err)
		}
		config.URL = localURL
		config.IsLocal = true
		config.Source = "local dev container (auto-detected)"
	}

	if err := ValidateURL(config.URL); err != nil {
		return nil, err
	}

	if flagToken != "" {
		config.Token = flagToken
		if !config.IsLocal {
			config.Source += " with token from flag"
		}
	} else if envToken := os.Getenv("HASS_TOKEN"); envToken != "" {
		config.Token = envToken
		if !config.IsLocal {
			config.Source += " with token from HASS_TOKEN"
		}
	} else if envToken := os.Getenv("HASS_BEARER_TOKEN"); envToken != "" {
		config.Token = envToken
		if !config.IsLocal {
			config.Source += " with token from HASS_BEARER_TOKEN"
		}
	} else if config.IsLocal {
		token, err := resolveLocalTokenContext(ctx, config.URL)
		if err != nil {
			return nil, fmt.Errorf("no token specified and could not read a valid local token: %w\n\n"+
				"The dev Home Assistant container may still be starting.\n"+
				"Try again in a few seconds or restart it with:\n"+
				"  docker compose -f .devcontainer/docker-compose.yml restart homeassistant", err)
		}
		config.Token = token
	} else {
		return nil, fmt.Errorf("--ha-token or HASS_TOKEN environment variable is required\n\n" +
			"Get a long-lived access token from your Home Assistant profile,\n" +
			"or start the local dev container for automatic token generation")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return config, nil
}

// ResolveInstanceConfigContext resolves the selected instance from specific flags,
// legacy flags, instance-specific environment variables, then legacy variables.
// Development URLs can fall back to local discovery. MCP workers use only the
// target and token supplied by their server.
func ResolveInstanceConfigContext(ctx context.Context, instance Instance, flags InstanceFlags) (*HAConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if os.Getenv("DM_MCP_WORKER_ROOT") != "" {
		// MCP workers have one server-configured target and never fall back to
		// container discovery or host credential files.
		url, token := os.Getenv("HASS_DEV_URL"), os.Getenv("HASS_DEV_TOKEN")
		if instance == InstanceProd {
			url, token = os.Getenv("HASS_PROD_URL"), os.Getenv("HASS_PROD_TOKEN")
		}
		if url == "" || token == "" {
			return nil, fmt.Errorf("configured MCP target URL and credential are required; automatic discovery is disabled")
		}
		if err := ValidateURL(url); err != nil {
			return nil, err
		}
		return &HAConfig{URL: url, Token: token, IsLocal: isLocalURL(url), Source: "MCP server configuration"}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	detect := func() (*HAConfig, error) { return resolveAutoDetectedLocalConfigContext(ctx) }
	if flags.ProjectRoot != "" {
		detect = func() (*HAConfig, error) { return ResolveProjectLocalConfigContext(ctx, flags.ProjectRoot) }
	}
	if flags.ConfigPath != "" {
		detect = func() (*HAConfig, error) { return ResolveConfigLocalConfigContext(ctx, flags.ConfigPath) }
	}
	return resolveInstanceConfigWithDepsContext(ctx, instance, flags, detect)
}

func resolveInstanceConfigWithDepsContext(ctx context.Context, instance Instance, flags InstanceFlags, autoDetectLocalConfig func() (*HAConfig, error)) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if autoDetectLocalConfig == nil {
		switch {
		case flags.ConfigPath != "":
			autoDetectLocalConfig = func() (*HAConfig, error) { return ResolveConfigLocalConfigContext(ctx, flags.ConfigPath) }
		case flags.ProjectRoot != "":
			autoDetectLocalConfig = func() (*HAConfig, error) { return ResolveProjectLocalConfigContext(ctx, flags.ProjectRoot) }
		default:
			autoDetectLocalConfig = func() (*HAConfig, error) { return resolveAutoDetectedLocalConfigContext(ctx) }
		}
	}

	config := &HAConfig{}

	var flagURL, flagToken string
	var envURLVar, envTokenVar string
	var legacyURLVar, legacyTokenVar string

	switch instance {
	case InstanceProd:
		flagURL = flags.ProdURL
		flagToken = flags.ProdToken
		envURLVar = "HASS_PROD_URL"
		envTokenVar = "HASS_PROD_TOKEN"
		legacyURLVar = "HASS_URL"
		legacyTokenVar = "HASS_TOKEN"
	case InstanceDev:
		flagURL = flags.DevURL
		flagToken = flags.DevToken
		envURLVar = "HASS_DEV_URL"
		envTokenVar = "HASS_DEV_TOKEN"
		legacyURLVar = "HASS_SERVER"
		legacyTokenVar = "HASS_BEARER_TOKEN"
	default:
		return nil, fmt.Errorf("unknown instance type: %s", instance)
	}

	if flagURL == "" && flags.LegacyURL != "" {
		flagURL = flags.LegacyURL
	}
	if flagToken == "" && flags.LegacyToken != "" {
		flagToken = flags.LegacyToken
	}

	if flagURL != "" {
		config.URL = flagURL
		config.Source = "command-line flag"
		config.IsLocal = isLocalURL(flagURL)
	} else if envURL := os.Getenv(envURLVar); envURL != "" {
		config.URL = envURL
		config.Source = fmt.Sprintf("%s environment variable", envURLVar)
		config.IsLocal = isLocalURL(envURL)
	} else if legacyURL := os.Getenv(legacyURLVar); legacyURL != "" {
		if instance == InstanceDev && !isHAReachableContext(ctx, legacyURL) {
		} else {
			config.URL = legacyURL
			config.Source = fmt.Sprintf("%s environment variable (legacy)", legacyURLVar)
			config.IsLocal = isLocalURL(legacyURL) || instance == InstanceDev
		}
	}

	var autoDetectErr error
	if config.URL == "" && instance == InstanceDev {
		if flagToken == "" && os.Getenv(envTokenVar) == "" && os.Getenv(legacyTokenVar) == "" {
			localConfig, err := autoDetectLocalConfig()
			if err == nil {
				return localConfig, nil
			}
			autoDetectErr = err
		} else {
			localConfig, err := autoDetectLocalConfig()
			if err == nil {
				config.URL = localConfig.URL
				config.IsLocal = true
				config.Source = "local dev container (auto-detected)"
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if config.URL == "" {
		if instance == InstanceProd {
			return nil, fmt.Errorf("no production HA URL specified\n\n" +
				"Usage:\n" +
				"  --prod-url URL           Specify production Home Assistant URL\n" +
				"  HASS_PROD_URL env var    Set environment variable\n" +
				"  HASS_URL env var         Legacy environment variable")
		}
		if autoDetectErr != nil {
			return nil, fmt.Errorf("no development HA URL specified and local dev container not available: %w\n\n"+
				"Usage:\n"+
				"  --dev-url URL            Specify development Home Assistant URL\n"+
				"  HASS_DEV_URL env var     Set environment variable\n"+
				"  (or start local dev)     cd .devcontainer && docker-compose up -d", autoDetectErr)
		}
		return nil, fmt.Errorf("no development HA URL specified and local dev container not available\n\n" +
			"Usage:\n" +
			"  --dev-url URL            Specify development Home Assistant URL\n" +
			"  HASS_DEV_URL env var     Set environment variable\n" +
			"  (or start local dev)     cd .devcontainer && docker-compose up -d")
	}

	if err := ValidateURL(config.URL); err != nil {
		return nil, err
	}

	if flagToken != "" {
		config.Token = flagToken
		if config.Source != "" && !config.IsLocal {
			config.Source += " with token from flag"
		}
	} else if envToken := os.Getenv(envTokenVar); envToken != "" {
		config.Token = envToken
		if !config.IsLocal {
			config.Source += fmt.Sprintf(" with token from %s", envTokenVar)
		}
	} else if legacyToken := os.Getenv(legacyTokenVar); legacyToken != "" {
		config.Token = legacyToken
		if !config.IsLocal {
			config.Source += fmt.Sprintf(" with token from %s (legacy)", legacyTokenVar)
		}
	} else if config.IsLocal && (flags.ConfigPath != "" || flags.ProjectRoot != "") {
		// A loopback production URL can be a tunnel; it must not inherit a
		// development credential. Explicit tokens above retain their precedence.
		if instance != InstanceDev {
			return nil, fmt.Errorf("--prod-token or HASS_PROD_TOKEN environment variable is required for the selected production target")
		}
		// Use the same project-scoped resolver as URL auto-detection. Never
		// fall back to global container/token scans for a selected project, or
		// send its token to a different local port, scheme, or URL path.
		localConfig, err := autoDetectLocalConfig()
		if err != nil {
			return nil, fmt.Errorf("could not resolve selected project development credentials: %w; supply --dev-token for an explicit target", err)
		}
		if localConfig == nil || localConfig.Token == "" || strings.TrimRight(localConfig.URL, "/") != strings.TrimRight(config.URL, "/") {
			return nil, fmt.Errorf("explicit development URL does not match an authenticated selected-project runtime; supply --dev-token for this target")
		}
		config.Token = localConfig.Token
		config.Source += " with token from selected project development environment"
	} else if config.IsLocal {
		token, err := resolveLocalTokenContext(ctx, config.URL)
		if err != nil {
			return nil, fmt.Errorf("no token specified and could not read a valid local token: %w\n\n"+
				"The dev Home Assistant container may still be starting.\n"+
				"Try again in a few seconds or restart it with:\n"+
				"  docker compose -f .devcontainer/docker-compose.yml restart homeassistant", err)
		}
		config.Token = token
	} else {
		if instance == InstanceProd {
			return nil, fmt.Errorf("--prod-token or HASS_PROD_TOKEN environment variable is required\n\n" +
				"Get a long-lived access token from your Home Assistant profile")
		}
		return nil, fmt.Errorf("--dev-token or HASS_DEV_TOKEN environment variable is required\n\n" +
			"Get a long-lived access token from your Home Assistant profile,\n" +
			"or start the local dev container for automatic token generation")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return config, nil
}

// ResolveLocalConfigContext discovers local development Home Assistant without
// consulting instance-specific environment variables.
func ResolveLocalConfigContext(ctx context.Context) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	config, err := resolveAutoDetectedLocalConfigContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("no local development HA URL available: %w\n\n"+
			"Start the dev container with:\n"+
			"  docker compose -f .devcontainer/docker-compose.yml up -d", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return config, nil
}

type tokenReader func() (string, error)

type localAutoDetectDeps struct {
	ServerURL    string
	Reachable    func(string) bool
	Discover     func() []localDevCandidate
	ResolveToken func(localDevCandidate) (string, error)
}

func resolveAutoDetectedLocalConfigContext(ctx context.Context) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return resolveAutoDetectedLocalConfigWithDepsContext(ctx, localAutoDetectDeps{
		ServerURL: os.Getenv("HASS_SERVER"),
		Reachable: func(url string) bool { return isHAReachableContext(ctx, url) },
		Discover:  func() []localDevCandidate { return discoverLocalDevCandidatesContext(ctx) },
		ResolveToken: func(candidate localDevCandidate) (string, error) {
			return resolveLocalTokenForCandidateContext(ctx, candidate)
		},
	})
}

func resolveAutoDetectedLocalConfigWithDepsContext(ctx context.Context, deps localAutoDetectDeps) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if deps.Reachable == nil {
		deps.Reachable = func(url string) bool { return isHAReachableContext(ctx, url) }
	}
	if deps.Discover == nil {
		deps.Discover = func() []localDevCandidate { return discoverLocalDevCandidatesContext(ctx) }
	}
	if deps.ResolveToken == nil {
		deps.ResolveToken = func(candidate localDevCandidate) (string, error) {
			return resolveLocalTokenForCandidateContext(ctx, candidate)
		}
	}

	var tokenErrors []string
	for _, candidate := range localDetectionCandidatesWithDeps(deps.ServerURL, deps.Discover) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if candidate.URL == "" || !deps.Reachable(candidate.URL) {
			continue
		}

		token, err := deps.ResolveToken(candidate)
		if err != nil {
			tokenErrors = append(tokenErrors, fmt.Sprintf("%s: %v", candidate.URL, err))
			continue
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &HAConfig{
			URL:     candidate.URL,
			Token:   token,
			IsLocal: true,
			Source:  "local dev container (auto-detected)",
		}, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(tokenErrors) > 0 {
		return nil, fmt.Errorf("local Home Assistant candidates were reachable but no token authenticated: %s", strings.Join(tokenErrors, "; "))
	}
	return nil, fmt.Errorf("home assistant not reachable at %s, worktree dev ports, or %s", LocalContainerURL, LocalHostURL)
}

func resolveLocalTokenContext(ctx context.Context, url string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	readers := []tokenReader{
		func() (string, error) { return ReadTokenFile(DefaultTokenFile) },
		func() (string, error) { return ReadTokenFromContainerContext(ctx) },
	}
	return resolveLocalTokenWithDepsContext(ctx,
		url,
		readers,
		func(url, token string) bool { return isHAAuthenticatedContext(ctx, url, token) },
		localTokenValidationAttempts,
		time.Second,
		nil,
	)
}

func resolveLocalTokenForCandidateContext(ctx context.Context, candidate localDevCandidate) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	readers := []tokenReader{
		func() (string, error) { return ReadTokenFile(DefaultTokenFile) },
	}
	if candidate.ContainerName != "" {
		readers = append(readers, readTokenFromContainerNameContext(ctx, candidate.ContainerName))
	} else {
		readers = append(readers, func() (string, error) { return ReadTokenFromContainerContext(ctx) })
	}
	return resolveLocalTokenWithDepsContext(ctx,
		candidate.URL,
		readers,
		func(url, token string) bool { return isHAAuthenticatedContext(ctx, url, token) },
		localTokenValidationAttempts,
		time.Second,
		nil,
	)
}

func readTokenFromContainerNameContext(ctx context.Context, containerName string) tokenReader {
	return func() (string, error) {
		return readTokenFromContainerWithDepsContext(ctx,
			[]string{containerName},
			func(name string, args ...string) ([]byte, error) {
				return boundedCommandOutput(ctx, name, args...)
			},
		)
	}
}

func resolveLocalTokenWithDepsContext(ctx context.Context,
	url string,
	readers []tokenReader,
	validate func(string, string) bool,
	attempts int,
	delay time.Duration,
	sleep func(time.Duration),
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	sawToken := false
	invalidTokens := map[string]int{}

	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		for _, read := range readers {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			token, err := read()
			if err != nil {
				lastErr = err
				continue
			}
			if token == "" {
				lastErr = fmt.Errorf("token reader returned empty token")
				continue
			}

			sawToken = true
			if nextAttempt, ok := invalidTokens[token]; ok && attempt < nextAttempt {
				lastErr = fmt.Errorf("token was read successfully but is waiting for replacement")
				continue
			}
			if validate(url, token) {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				return token, nil
			}
			invalidTokens[token] = attempt + 2
			lastErr = fmt.Errorf("token was read successfully but authentication failed")
		}

		if attempt < attempts-1 {
			if sleep != nil {
				sleep(delay)
			} else if err := waitForRetry(ctx, delay); err != nil {
				return "", err
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no token readers available")
	}
	if sawToken {
		return "", fmt.Errorf("local token was found but was not yet valid for %s: %w", url, lastErr)
	}
	return "", fmt.Errorf("could not read local token for %s: %w", url, lastErr)
}

// detectLocalURLContext returns the first reachable local candidate, starting
// with HASS_SERVER and then checking container, project, and host defaults.
func detectLocalURLContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return detectLocalURLWithDepsContext(ctx, os.Getenv("HASS_SERVER"), func(url string) bool { return isHAReachableContext(ctx, url) }, func() []localDevCandidate { return discoverLocalDevCandidatesContext(ctx) })
}

func detectLocalURLWithDepsContext(ctx context.Context, serverURL string, reachable func(string) bool, discover func() []localDevCandidate) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	for _, candidate := range localDetectionCandidatesWithDeps(serverURL, discover) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if candidate.URL != "" && reachable(candidate.URL) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return candidate.URL, nil
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("home assistant not reachable at %s, worktree dev ports, or %s", LocalContainerURL, LocalHostURL)
}

func localDetectionCandidatesWithDeps(serverURL string, discover func() []localDevCandidate) []localDevCandidate {
	candidates := []localDevCandidate{}
	if serverURL != "" {
		candidates = append(candidates, localDevCandidate{URL: serverURL})
	}
	candidates = append(candidates, localDevCandidate{URL: LocalContainerURL})
	if discover != nil {
		candidates = append(candidates, discover()...)
	}
	candidates = append(candidates, localDevCandidate{URL: LocalHostURL})

	deduped := make([]localDevCandidate, 0, len(candidates))
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate.URL == "" || seen[candidate.URL] {
			continue
		}
		seen[candidate.URL] = true
		deduped = append(deduped, candidate)
	}
	return deduped
}

func discoverLocalDevCandidatesContext(ctx context.Context) []localDevCandidate {
	root, err := repoRootForLocalDevContext(ctx)
	if err != nil {
		return nil
	}
	return discoverLocalDevCandidatesFromRoot(root)
}

func repoRootForLocalDevContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	output, err := boundedCommandOutput(ctx, "git", "rev-parse", "--show-toplevel")
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return os.Getwd()
}

func discoverLocalDevCandidatesFromRoot(root string) []localDevCandidate {
	matches, err := filepath.Glob(filepath.Join(root, ".devcontainer", "worktrees", "*", "docker-compose.yml"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)

	candidates := make([]localDevCandidate, 0, len(matches))
	seen := make(map[string]bool)
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		candidate, ok := parseLocalDevCompose(string(data))
		if !ok || candidate.URL == "" || seen[candidate.URL] {
			continue
		}
		seen[candidate.URL] = true
		candidates = append(candidates, candidate)
	}
	return candidates
}

func parseLocalDevCompose(compose string) (localDevCandidate, bool) {
	var candidate localDevCandidate
	for _, line := range strings.Split(compose, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "container_name:") {
			containerName := strings.TrimSpace(strings.TrimPrefix(line, "container_name:"))
			containerName = strings.Trim(containerName, `"'`)
			if strings.HasSuffix(containerName, "-homeassistant") {
				candidate.ContainerName = containerName
			}
			continue
		}

		matches := composePortRe.FindStringSubmatch(line)
		if len(matches) == 2 {
			candidate.URL = "http://localhost:" + matches[1]
		}
	}

	return candidate, candidate.URL != "" || candidate.ContainerName != ""
}

// isLocalURL checks if the URL is a known local development URL.
// Parse scheme/host so userinfo cannot spoof a local prefix while the
// real destination is remote (for example http://localhost:@prod.example/).
func isLocalURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	// Reject credentials in the authority; they are never valid for local DM targets.
	if parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1", "homeassistant", "hass":
		return true
	default:
		return false
	}
}

// isHAReachableContext checks for an HA API response within two seconds or
// the caller deadline, whichever comes first.
func isHAReachableContext(ctx context.Context, url string) bool {
	if ValidateURL(url) != nil {
		return false
	}
	client := hahttp.NewClient(2 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/api/", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// HA returns 401 without auth, but that means it's running
	return ctx.Err() == nil && (resp.StatusCode == 200 || resp.StatusCode == 401)
}

func isHAAuthenticatedContext(ctx context.Context, url, token string) bool {
	if ValidateURL(url) != nil {
		return false
	}
	client := hahttp.NewClient(2 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/api/", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return ctx.Err() == nil && resp.StatusCode == http.StatusOK
}

// ReadTokenFromContainerContext reads the HA token through docker exec when
// the container token file is not accessible from the host.
func ReadTokenFromContainerContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return readTokenFromContainerWithDepsContext(ctx,
		candidateTokenContainerNamesContext(ctx),
		func(name string, args ...string) ([]byte, error) {
			return boundedCommandOutput(ctx, name, args...)
		},
	)
}

func candidateTokenContainerNamesContext(ctx context.Context) []string {
	containerNames := []string{
		"devcontainer-homeassistant",
		"hass-dev-homeassistant",
	}
	seen := map[string]bool{
		"devcontainer-homeassistant": true,
		"hass-dev-homeassistant":     true,
	}
	for _, candidate := range discoverLocalDevCandidatesContext(ctx) {
		if candidate.ContainerName == "" || seen[candidate.ContainerName] {
			continue
		}
		seen[candidate.ContainerName] = true
		containerNames = append(containerNames, candidate.ContainerName)
	}
	return containerNames
}

func readTokenFromContainerWithDepsContext(ctx context.Context, containerNames []string, commandOutput func(string, ...string) ([]byte, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	for _, name := range containerNames {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		output, err := commandOutput("docker", "exec", name, "cat", "/run/hass/token.env")
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "HASS_BEARER_TOKEN=") {
					token := strings.TrimPrefix(line, "HASS_BEARER_TOKEN=")
					if token != "" {
						return token, nil
					}
				}
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("could not read token from container (tried: %v)", containerNames)
}

// ReadTokenFile reads the HASS_BEARER_TOKEN from a token file.
// The file format is: HASS_BEARER_TOKEN=<token>
func ReadTokenFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open token file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "HASS_BEARER_TOKEN=") {
			token := strings.TrimPrefix(line, "HASS_BEARER_TOKEN=")
			if token == "" {
				return "", fmt.Errorf("token file contains empty token")
			}
			return token, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading token file: %w", err)
	}

	return "", fmt.Errorf("HASS_BEARER_TOKEN not found in token file")
}

// ValidateURL rejects ambiguous targets and URL-embedded secrets before I/O.
func ValidateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(raw) != raw {
		return fmt.Errorf("invalid HA URL: use an absolute http(s) URL without credentials, query, or fragment")
	}
	return nil
}

// ResolveProjectLocalConfigContext probes only runtimes discovered under root.
func ResolveProjectLocalConfigContext(ctx context.Context, root string) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return resolveProjectCandidateContext(ctx, root, "")
}
func ResolveConfigLocalConfigContext(ctx context.Context, config string) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	settings, err := project.ForConfig(config)
	if err != nil {
		return nil, err
	}
	return resolveProjectCandidateContext(ctx, settings.Root, devname.HomeAssistantContainer(settings.RuntimeKey()))
}
func resolveProjectCandidateContext(ctx context.Context, root, container string) (*HAConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, candidate := range discoverLocalDevCandidatesFromRoot(root) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if container != "" && candidate.ContainerName != container {
			continue
		}
		if !isHAReachableContext(ctx, candidate.URL) {
			continue
		}
		token, err := ReadTokenFile(filepath.Join(root, ".devcontainer", "worktrees", strings.TrimSuffix(candidate.ContainerName, "-homeassistant"), "token.env"))
		if err != nil {
			token, err = readTokenFromContainerNameContext(ctx, candidate.ContainerName)()
		}
		if err == nil && isHAAuthenticatedContext(ctx, candidate.URL, token) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &HAConfig{URL: candidate.URL, Token: token, IsLocal: true, Source: "selected project development environment"}, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("selected project development environment is unavailable; run dm dev up with the same --config")
}

// Compatibility entrypoints use background contexts; new callers should pass their operation context.
func ResolveConfig(flagURL, flagToken string) (*HAConfig, error) {
	return ResolveConfigContext(context.Background(), flagURL, flagToken)
}
func ResolveInstanceConfig(instance Instance, flags InstanceFlags) (*HAConfig, error) {
	return ResolveInstanceConfigContext(context.Background(), instance, flags)
}
func resolveInstanceConfigWithDeps(instance Instance, flags InstanceFlags, autoDetectLocalConfig func() (*HAConfig, error)) (*HAConfig, error) {
	return resolveInstanceConfigWithDepsContext(context.Background(), instance, flags, autoDetectLocalConfig)
}
func ResolveLocalConfig() (*HAConfig, error) { return ResolveLocalConfigContext(context.Background()) }
func resolveAutoDetectedLocalConfigWithDeps(deps localAutoDetectDeps) (*HAConfig, error) {
	return resolveAutoDetectedLocalConfigWithDepsContext(context.Background(), deps)
}
func resolveLocalTokenWithDeps(url string, readers []tokenReader, validate func(string, string) bool, attempts int, delay time.Duration, sleep func(time.Duration)) (string, error) {
	return resolveLocalTokenWithDepsContext(context.Background(), url, readers, validate, attempts, delay, sleep)
}
func detectLocalURLWithDeps(serverURL string, reachable func(string) bool, discover func() []localDevCandidate) (string, error) {
	return detectLocalURLWithDepsContext(context.Background(), serverURL, reachable, discover)
}
func ReadTokenFromContainer() (string, error) {
	return ReadTokenFromContainerContext(context.Background())
}
func readTokenFromContainerWithDeps(containerNames []string, commandOutput func(string, ...string) ([]byte, error)) (string, error) {
	return readTokenFromContainerWithDepsContext(context.Background(), containerNames, commandOutput)
}
func ResolveProjectLocalConfig(root string) (*HAConfig, error) {
	return ResolveProjectLocalConfigContext(context.Background(), root)
}
func ResolveConfigLocalConfig(config string) (*HAConfig, error) {
	return ResolveConfigLocalConfigContext(context.Background(), config)
}
