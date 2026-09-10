// Package hasync provides synchronization with Home Assistant instances.
// It supports fetching entities, devices, and areas via REST and WebSocket APIs,
// and can generate mismatch reports comparing configuration with live data.
package hasync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hahttp"
)

// Client handles communication with Home Assistant API
type Client struct {
	ctx        context.Context
	baseURL    string
	token      string
	httpClient *http.Client
}

// EntityState represents an entity state from HA API
type EntityState struct {
	EntityID   string                 `json:"entity_id"`
	State      string                 `json:"state"`
	Attributes map[string]interface{} `json:"attributes"`
}

// Device represents a device from HA API
type Device struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Identifiers   [][]string `json:"identifiers"`
	Model         string     `json:"model"`
	Manufacturer  string     `json:"manufacturer"`
	AreaID        string     `json:"area_id"`
	DisabledBy    string     `json:"disabled_by"`
	ConfigEntries []string   `json:"config_entries"`
}

// EntityRegistryEntry represents an entry from the HA entity registry
type EntityRegistryEntry struct {
	EntityID   string `json:"entity_id"`
	UniqueID   string `json:"unique_id"`
	DeviceID   string `json:"device_id"`
	Platform   string `json:"platform"`
	DisabledBy string `json:"disabled_by"`
	AreaID     string `json:"area_id"`
}

// Area represents an area from HA API
type Area struct {
	AreaID string `json:"area_id"`
	Name   string `json:"name"`
}

// NewClient creates a new Home Assistant API client
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      token,
		httpClient: hahttp.NewClient(30 * time.Second),
	}
}

// FetchEntities retrieves all entity states from Home Assistant
func (c *Client) FetchEntities() ([]EntityState, error) {
	resp, err := c.doRequest("GET", "/api/states")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch entities: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA API returned status %d", resp.StatusCode)
	}

	var entities []EntityState
	if err := json.NewDecoder(resp.Body).Decode(&entities); err != nil {
		return nil, fmt.Errorf("failed to decode entities: %w", err)
	}

	if entities == nil {
		return nil, fmt.Errorf("HA states response must be an array, not null")
	}
	return entities, nil
}

// FetchDevices retrieves all devices from Home Assistant via WebSocket API.
// Use FetchDevicesAndAreas to fetch both over one connection.
func (c *Client) FetchDevices() ([]Device, error) {
	wsClient := NewWSClient(c.baseURL, c.token)
	wsClient.SetContext(c.context())
	if err := wsClient.ConnectContext(c.context()); err != nil {
		return nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	defer wsClient.Close()

	devices, err := wsClient.FetchDevicesWS()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch devices: %w", err)
	}

	return devices, nil
}

// FetchAreas retrieves all areas from Home Assistant via WebSocket API.
// Use FetchDevicesAndAreas to fetch both over one connection.
func (c *Client) FetchAreas() ([]Area, error) {
	wsClient := NewWSClient(c.baseURL, c.token)
	wsClient.SetContext(c.context())
	if err := wsClient.ConnectContext(c.context()); err != nil {
		return nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	defer wsClient.Close()

	areas, err := wsClient.FetchAreasWS()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch areas: %w", err)
	}

	return areas, nil
}

// FetchDevicesAndAreas retrieves both devices and areas using a single WebSocket
// connection. If one fetch fails, the other dataset is still returned.
// Successful datasets are non-nil (including empty arrays); nil means fetch failed.
func (c *Client) FetchDevicesAndAreas() ([]Device, []Area, error) {
	wsClient := NewWSClient(c.baseURL, c.token)
	wsClient.SetContext(c.context())
	if err := wsClient.ConnectContext(c.context()); err != nil {
		return nil, nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	defer wsClient.Close()

	var devices []Device
	var areas []Area
	var errors []string

	if d, err := wsClient.FetchDevicesWS(); err != nil {
		errors = append(errors, fmt.Sprintf("devices: %v", err))
	} else {
		devices = d
	}

	if a, err := wsClient.FetchAreasWS(); err != nil {
		errors = append(errors, fmt.Sprintf("areas: %v", err))
	} else {
		areas = a
	}

	if len(errors) == 2 {
		return nil, nil, fmt.Errorf("failed to fetch: %s", strings.Join(errors, "; "))
	}

	if len(errors) == 1 {
		return devices, areas, fmt.Errorf("partial fetch: %s", errors[0])
	}

	return devices, areas, nil
}

// Ping checks if Home Assistant is reachable
func (c *Client) Ping() error {
	resp, err := c.doRequest("GET", "/api/")
	if err != nil {
		return fmt.Errorf("failed to ping HA: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HA API returned status %d", resp.StatusCode)
	}

	return nil
}

func (c *Client) doRequest(method, path string) (*http.Response, error) {
	if err := haconfig.ValidateURL(c.baseURL); err != nil {
		return nil, err
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(c.context(), method, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}

func (c *Client) WithContext(ctx context.Context) *Client { copy := *c; copy.ctx = ctx; return &copy }
func (c *Client) context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}
