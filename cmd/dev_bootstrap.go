package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/project"
	"github.com/BrianTillman/Denmother/internal/util"
)

type devLoginCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// bootstrapPortableDev authenticates the provisioned dev service through HA's
// onboarding/login API, leaving HA to manage its auth storage.
func bootstrapPortableDev(ctx context.Context, env *devEnvironment) error {
	settings, err := project.ForConfig(env.ConfigRoot)
	if err != nil {
		return err
	}
	if settings.DevComposeTemplate != "" {
		deadlineCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		for {
			if _, err := haconfig.ResolveConfigLocalConfigContext(deadlineCtx, settings.ConfigDir); err == nil {
				return nil
			}
			select {
			case <-deadlineCtx.Done():
				return fmt.Errorf("custom development runtime authentication: %w", deadlineCtx.Err())
			case <-time.After(time.Second):
			}
		}
	} // The explicit template owns its bootstrap.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client := hahttp.NewClient(10 * time.Second)
	request := func(method, path, contentType, token string, body []byte, out any) error {
		req, err := http.NewRequestWithContext(ctx, method, env.HAURL+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("development %s returned HTTP %d", path, response.StatusCode)
		}
		if out != nil {
			return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
		}
		return nil
	}
	post := func(path, token string, payload any, out any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return request(http.MethodPost, path, "application/json", token, data, out)
	}
	var onboarding []devOnboardingStep
	for {
		var err error
		onboarding, err = readPortableOnboarding(ctx, env.HAURL)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("development HA readiness: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	onboardingComplete := true
	for _, step := range onboarding {
		if !step.Done {
			onboardingComplete = false
		}
	}
	tokenPath := filepath.Join(filepath.Dir(env.ComposeFile), "token.env")
	if data, err := os.ReadFile(tokenPath); err == nil {
		token := strings.TrimSpace(strings.TrimPrefix(string(data), "HASS_BEARER_TOKEN="))
		if onboardingComplete && token != "" && request(http.MethodGet, "/api/", "", token, nil, nil) == nil {
			if err := waitPortableDevRunning(ctx, env.HAURL, token); err != nil {
				return err
			}
			if err := storePortableDevToken(ctx, env, data); err != nil {
				return err
			}
			return applyPortableDevFixtures(ctx, env)
		}
	}
	credentialsPath := filepath.Join(filepath.Dir(env.ComposeFile), "credentials.json")
	credentials := devLoginCredentials{Username: "denmother", Password: rand.Text()}
	if data, err := os.ReadFile(credentialsPath); err == nil {
		if err := json.Unmarshal(data, &credentials); err != nil {
			return fmt.Errorf("invalid local development credentials file")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		data, _ := json.Marshal(credentials)
		if err := util.WriteFileAtomic(credentialsPath, data, 0600); err != nil {
			return err
		}
	}
	clientID := env.HAURL + "/"
	var code string
	userDone := len(onboarding) == 0
	for _, step := range onboarding {
		if step.Step == "user" {
			userDone = step.Done
		}
	}
	if !userDone {
		var response struct {
			AuthCode string `json:"auth_code"`
		}
		if err := post("/api/onboarding/users", "", map[string]string{"name": "Denmother Development", "username": credentials.Username, "password": credentials.Password, "client_id": clientID, "language": "en"}, &response); err != nil {
			return err
		}
		code = response.AuthCode
	} else {
		var flow struct {
			FlowID string `json:"flow_id"`
		}
		if err := post("/auth/login_flow", "", map[string]any{"client_id": clientID, "handler": []any{"homeassistant", nil}, "redirect_uri": clientID}, &flow); err != nil {
			return err
		}
		var response struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		}
		if err := post("/auth/login_flow/"+flow.FlowID, "", map[string]string{"client_id": clientID, "username": credentials.Username, "password": credentials.Password}, &response); err != nil {
			return err
		}
		if response.Type != "create_entry" {
			return fmt.Errorf("development login failed; reset this local runtime if its credentials were changed")
		}
		code = response.Result
	}
	if code == "" {
		return fmt.Errorf("development authentication returned no authorization code")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}}
	var authorization struct {
		AccessToken string `json:"access_token"`
	}
	if err := request(http.MethodPost, "/auth/token", "application/x-www-form-urlencoded", "", []byte(form.Encode()), &authorization); err != nil {
		return err
	}
	if authorization.AccessToken == "" {
		return fmt.Errorf("development authentication returned no access token")
	}
	if err := finishPortableOnboarding(onboarding, authorization.AccessToken, clientID, post); err != nil {
		return err
	}
	ws := hasync.NewWSClient(env.HAURL, authorization.AccessToken)
	if err := ws.ConnectContext(ctx); err != nil {
		return err
	}
	defer ws.Close()
	result, err := ws.SendCommandContext(ctx, "auth/long_lived_access_token", map[string]any{"client_name": "Denmother " + time.Now().UTC().Format(time.RFC3339Nano), "lifespan": 365})
	if err != nil {
		return fmt.Errorf("create local development token: %w", err)
	}
	var token string
	if err := json.Unmarshal(result, &token); err != nil || token == "" {
		return fmt.Errorf("invalid development token response")
	}
	data := []byte("HASS_BEARER_TOKEN=" + token + "\n")
	if err := util.WriteFileAtomic(tokenPath, data, 0600); err != nil {
		return err
	}
	if err := storePortableDevToken(ctx, env, data); err != nil {
		return err
	}
	if err := waitPortableDevRunning(ctx, env.HAURL, token); err != nil {
		return err
	}
	if err := request(http.MethodGet, "/api/", "", token, nil, nil); err != nil {
		return err
	}
	return applyPortableDevFixtures(ctx, env)
}

// An authenticated HTTP API is available before integrations finish loading.
// Wait for HA's startup barrier before checking or seeding entity state.
func waitPortableDevRunning(ctx context.Context, baseURL, token string) error {
	client := hahttp.NewClient(10 * time.Second)
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/config", nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			var config struct {
				State        string `json:"state"`
				SafeMode     bool   `json:"safe_mode"`
				RecoveryMode bool   `json:"recovery_mode"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&config)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil {
				if config.SafeMode || config.RecoveryMode {
					return fmt.Errorf("development HA entered safe/recovery mode; inspect its configuration errors")
				}
				if strings.EqualFold(config.State, "RUNNING") {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for development HA integrations: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func storePortableDevToken(ctx context.Context, env *devEnvironment, data []byte) error {
	command := exec.CommandContext(ctx, "docker", "exec", "-i", env.ProjectName+"-homeassistant", "sh", "-c", "umask 077; cat > /run/hass/token.env")
	command.Stdin = bytes.NewReader(data)
	if err := command.Run(); err != nil {
		return fmt.Errorf("store local development token: %w", err)
	}
	return nil
}

type devOnboardingStep struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

func readPortableOnboarding(ctx context.Context, baseURL string) ([]devOnboardingStep, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/onboarding", nil)
	if err != nil {
		return nil, err
	}
	response, err := hahttp.NewClient(10 * time.Second).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// HA stops registering onboarding routes after completing setup and restarting.
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("onboarding status returned HTTP %d", response.StatusCode)
	}
	var steps []devOnboardingStep
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&steps); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("onboarding status returned no steps")
	}
	return steps, nil
}

func finishPortableOnboarding(steps []devOnboardingStep, token, clientID string, post func(string, string, any, any) error) error {
	pending := map[string]bool{}
	for _, step := range steps {
		pending[step.Step] = !step.Done
	}
	for _, step := range []string{"core_config", "analytics", "integration"} {
		if !pending[step] {
			continue
		}
		payload := map[string]string{}
		if step == "integration" {
			payload["client_id"] = clientID
			payload["redirect_uri"] = clientID
		}
		// Completing analytics setup preserves HA's default opt-out setting.
		if err := post("/api/onboarding/"+step, token, payload, nil); err != nil {
			return fmt.Errorf("complete development onboarding %s: %w", step, err)
		}
	}
	return nil
}
