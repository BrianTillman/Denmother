package hatest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/hahttp"
)

// TestClient handles HA API interactions for testing
type TestClient struct {
	ctx        context.Context
	baseURL    string
	token      string
	httpClient *http.Client
}

// EntityState represents a Home Assistant entity state
type EntityState struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes"`
	LastChanged string                 `json:"last_changed"`
	LastUpdated string                 `json:"last_updated"`
}

// NewTestClient creates a test client
func NewTestClient(baseURL, token string) *TestClient {
	return &TestClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      token,
		httpClient: hahttp.NewClient(30 * time.Second),
	}
}

// CallService executes a service call
// POST /api/services/<domain>/<service>
func (c *TestClient) CallService(domain, service string, data map[string]interface{}) error {
	url := fmt.Sprintf("%s/api/services/%s/%s", c.baseURL, domain, service)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal service data: %w", err)
	}

	req, err := http.NewRequestWithContext(c.context(), "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("service call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("service call failed with status %d", resp.StatusCode)
	}

	return nil
}

// GetEntityState retrieves current entity state
// GET /api/states/<entity_id>
func (c *TestClient) GetEntityState(entityID string) (*EntityState, error) {
	url := fmt.Sprintf("%s/api/states/%s", c.baseURL, entityID)

	req, err := http.NewRequestWithContext(c.context(), "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get entity state failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("entity %s not found", entityID)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get entity state failed with status %d", resp.StatusCode)
	}

	var state EntityState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to decode entity state: %w", err)
	}

	return &state, nil
}

// SetEntityState uses turn_on/turn_off for selected domains with on/off states.
// Other domains and states use the states API directly.
func (c *TestClient) SetEntityState(entityID, state string, attributes map[string]interface{}) error {
	domain := getDomain(entityID)

	// Use services so the integration updates its internal state as well as HA
	// state records.
	if state == "on" || state == "off" {
		switch domain {
		case "light", "switch", "fan", "cover", "lock", "climate", "vacuum",
			"media_player", "input_boolean":
			// binary_sensor has no turn_on/turn_off services; it uses the state API below.
			data := map[string]interface{}{
				"entity_id": entityID,
			}
			for k, v := range attributes {
				data[k] = v
			}
			var service string
			if state == "on" {
				service = "turn_on"
			} else {
				service = "turn_off"
			}
			return c.CallService(domain, service, data)
		}
	}

	return c.SetEntityStateDirect(entityID, state, attributes)
}

// SetEntityStateDirect writes an HA state record through POST /api/states/<entity_id>.
// It does not call integration services.
func (c *TestClient) SetEntityStateDirect(entityID, state string, attributes map[string]interface{}) error {
	url := fmt.Sprintf("%s/api/states/%s", c.baseURL, entityID)

	payload := map[string]interface{}{
		"state": state,
	}
	attributes = normalizeDirectStateAttributes(entityID, state, attributes)
	if len(attributes) > 0 {
		payload["attributes"] = attributes
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal state data: %w", err)
	}

	req, err := http.NewRequestWithContext(c.context(), "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("set state failed: %w", err)
	}
	defer resp.Body.Close()

	// 200 = updated existing, 201 = created new
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("set state failed with status %d", resp.StatusCode)
	}

	return nil
}

func normalizeDirectStateAttributes(entityID, state string, attributes map[string]interface{}) map[string]interface{} {
	if getDomain(entityID) != "timer" || state != "active" {
		return attributes
	}

	normalized := make(map[string]interface{}, len(attributes)+2)
	for k, v := range attributes {
		normalized[k] = v
	}
	if _, ok := normalized["duration"]; !ok {
		normalized["duration"] = "0:01:00"
	}
	if _, ok := normalized["finishes_at"]; !ok {
		normalized["finishes_at"] = time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	}
	return normalized
}

// DeleteEntityState removes a directly-created state from Home Assistant's state
// machine. It is used by test auto-restore when a test creates a mock entity
// that did not exist before the test case started.
func (c *TestClient) DeleteEntityState(entityID string) error {
	url := fmt.Sprintf("%s/api/states/%s", c.baseURL, entityID)

	req, err := http.NewRequestWithContext(c.context(), "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete state failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete state failed with status %d", resp.StatusCode)
	}

	return nil
}

// setEntityStateViaService sets entity state using domain service calls.
// It supports on/off states and timer active/idle states.
func (c *TestClient) setEntityStateViaService(entityID, state string, attributes map[string]interface{}) error {
	domain := getDomain(entityID)
	data := map[string]interface{}{
		"entity_id": entityID,
	}
	for k, v := range attributes {
		data[k] = v
	}

	if domain == "timer" {
		switch state {
		case "active":
			if _, ok := data["duration"]; !ok {
				data["duration"] = "00:01:00"
			}
			return c.CallService(domain, "start", data)
		case "idle":
			return c.CallService(domain, "cancel", data)
		default:
			return fmt.Errorf("service method only supports timer states 'active'/'idle', got %q", state)
		}
	}

	if state != "on" && state != "off" {
		return fmt.Errorf("service method only supports 'on'/'off' states, got %q", state)
	}

	var service string
	if state == "on" {
		service = "turn_on"
	} else {
		service = "turn_off"
	}

	return c.CallService(domain, service, data)
}

// FireEvent fires a Home Assistant event
// POST /api/events/<event_type>
func (c *TestClient) FireEvent(eventType string, eventData map[string]interface{}) error {
	url := fmt.Sprintf("%s/api/events/%s", c.baseURL, eventType)

	var jsonData []byte
	var err error
	if eventData != nil {
		jsonData, err = json.Marshal(eventData)
		if err != nil {
			return fmt.Errorf("failed to marshal event data: %w", err)
		}
	} else {
		jsonData = []byte("{}")
	}

	req, err := http.NewRequestWithContext(c.context(), "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fire event failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("fire event failed with status %d", resp.StatusCode)
	}

	return nil
}

// Ping checks if Home Assistant is reachable
func (c *TestClient) Ping() error {
	url := fmt.Sprintf("%s/api/", c.baseURL)

	req, err := http.NewRequestWithContext(c.context(), "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}

	return nil
}

// parseService splits "domain.service" into domain and service
func parseService(fullService string) (string, string) {
	parts := strings.SplitN(fullService, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", fullService
}

// getDomain extracts domain from entity_id
func getDomain(entityID string) string {
	parts := strings.SplitN(entityID, ".", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

// WithContext returns a client sharing the transport but bound to this operation.
func (c *TestClient) WithContext(ctx context.Context) *TestClient {
	copy := *c
	copy.ctx = ctx
	return &copy
}
func (c *TestClient) context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}
