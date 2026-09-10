package hasync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"

	"github.com/gorilla/websocket"
)

// WSClient handles WebSocket communication with Home Assistant
type WSClient struct {
	baseURL    string
	token      string
	conn       *websocket.Conn
	msgID      int
	mu         sync.Mutex
	operations chan struct{}
	ctx        context.Context
}

// WSMessage represents a generic WebSocket message
type WSMessage struct {
	ID      int             `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success bool            `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *WSError        `json:"error,omitempty"`
}

// WSError represents an error response from Home Assistant
type WSError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WSAuthMessage is the authentication message
type WSAuthMessage struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token"`
}

// WSCommandMessage is a command message with ID
type WSCommandMessage struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

// NewWSClient creates a new WebSocket client
func NewWSClient(baseURL, token string) *WSClient {
	return &WSClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      token,
		msgID:      0,
		operations: make(chan struct{}, 1),
	}
}

// Connect establishes and authenticates a connection within ten seconds.
func (c *WSClient) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext bounds the entire upgrade and authentication exchange by the
// earlier of the caller's deadline and ten seconds. Cancellation closes the
// in-progress connection; failed authentication never leaves a usable client.
func (c *WSClient) ConnectContext(ctx context.Context) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case c.operations <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.operations }()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return fmt.Errorf("WebSocket client is already connected")
	}
	defer func() {
		if err != nil && ctx.Err() != nil {
			err = fmt.Errorf("WebSocket authentication: %w", ctx.Err())
		}
	}()
	wsURL, err := c.buildWSURL()
	if err != nil {
		return fmt.Errorf("failed to build WebSocket URL: %w", err)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	// DialContext only covers the upgrade. Keep cancellation and the same
	// deadline active through both authentication reads and the token write.
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.Close()
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			conn.Close()
			return
		}
		c.conn = conn
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("failed to set authentication read deadline: %w", err)
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("failed to set authentication write deadline: %w", err)
	}

	var authReq WSMessage
	if err := conn.ReadJSON(&authReq); err != nil {
		return fmt.Errorf("failed to read auth_required: %w", err)
	}
	if authReq.Type != "auth_required" {
		return fmt.Errorf("expected auth_required, got: %s", authReq.Type)
	}

	authMsg := WSAuthMessage{
		Type:        "auth",
		AccessToken: c.token,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		return fmt.Errorf("failed to send auth: %w", err)
	}

	var authResp WSMessage
	if err := conn.ReadJSON(&authResp); err != nil {
		return fmt.Errorf("failed to read auth response: %w", err)
	}
	if authResp.Type == "auth_invalid" {
		return fmt.Errorf("authentication failed: invalid token")
	}
	if authResp.Type != "auth_ok" {
		return fmt.Errorf("expected auth_ok, got: %s", authResp.Type)
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("failed to clear authentication read deadline: %w", err)
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		return fmt.Errorf("failed to clear authentication write deadline: %w", err)
	}
	return nil
}

// Close closes the WebSocket connection
func (c *WSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// commandTimeout is the maximum time to wait for a command response
const commandTimeout = 30 * time.Second

// maxSkippedMessages is the maximum number of unrelated messages to skip before giving up
const maxSkippedMessages = 100

// SetContext binds subsequent commands to an operation's cancellation context.
// Configure it before sharing the client with callers.
func (c *WSClient) SetContext(ctx context.Context) { c.mu.Lock(); defer c.mu.Unlock(); c.ctx = ctx }

func (c *WSClient) SendCommand(cmdType string) (json.RawMessage, error) {
	return c.SendCommandWithParams(cmdType, nil)
}

func (c *WSClient) SendCommandWithParams(cmdType string, params map[string]interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return c.SendCommandContext(ctx, cmdType, params)
}

// SendCommandContext serializes a complete request/response exchange. One
// deadline covers the write and all unrelated messages. Cancellation invalidates
// the connection: a timed-out websocket cannot safely be reused.
func (c *WSClient) SendCommandContext(ctx context.Context, cmdType string, params map[string]interface{}) (result json.RawMessage, err error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	select {
	case c.operations <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.operations }()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.mu.Lock()
	conn := c.conn
	c.msgID++
	id := c.msgID
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("WebSocket client is not connected")
	}
	if _, ok := params["id"]; ok {
		return nil, fmt.Errorf("command params must not contain id")
	}
	if _, ok := params["type"]; ok {
		return nil, fmt.Errorf("command params must not contain type")
	}
	message := make(map[string]interface{}, len(params)+2)
	for key, value := range params {
		message[key] = value
	}
	message["id"] = id
	message["type"] = cmdType
	healthy := false
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if !healthy || ctx.Err() != nil {
			conn.Close()
			c.mu.Lock()
			if c.conn == conn {
				c.conn = nil
			}
			c.mu.Unlock()
		}
	}()
	deadline, _ := ctx.Deadline()
	if err = conn.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err = conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	if err = conn.WriteJSON(message); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}
	for skipped := 0; skipped < maxSkippedMessages; skipped++ {
		var response WSMessage
		if err = conn.ReadJSON(&response); err != nil {
			return nil, fmt.Errorf("read command response: %w", err)
		}
		if response.ID != id {
			continue
		}
		if response.Type != "result" {
			return nil, fmt.Errorf("unexpected command response type %q", response.Type)
		}
		healthy = true
		if !response.Success {
			if response.Error != nil {
				return nil, fmt.Errorf("command failed: %s", response.Error.Code)
			}
			return nil, fmt.Errorf("command unsuccessful")
		}
		return response.Result, nil
	}
	return nil, fmt.Errorf("exceeded maximum skipped messages (%d)", maxSkippedMessages)
}

// FetchDevicesWS fetches devices via WebSocket
func (c *WSClient) FetchDevicesWS() ([]Device, error) {
	result, err := c.SendCommand("config/device_registry/list")
	if err != nil {
		return nil, err
	}

	var devices []Device
	if err := json.Unmarshal(result, &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices: %w", err)
	}

	if devices == nil {
		return nil, fmt.Errorf("expected registry array, got null")
	}

	return devices, nil
}

// FetchAreasWS fetches areas via WebSocket
func (c *WSClient) FetchAreasWS() ([]Area, error) {
	result, err := c.SendCommand("config/area_registry/list")
	if err != nil {
		return nil, err
	}

	var areas []Area
	if err := json.Unmarshal(result, &areas); err != nil {
		return nil, fmt.Errorf("failed to unmarshal areas: %w", err)
	}

	if areas == nil {
		return nil, fmt.Errorf("expected registry array, got null")
	}

	return areas, nil
}

// FetchEntityRegistryWS fetches entity registry entries via WebSocket
func (c *WSClient) FetchEntityRegistryWS() ([]EntityRegistryEntry, error) {
	result, err := c.SendCommand("config/entity_registry/list")
	if err != nil {
		return nil, err
	}

	var entries []EntityRegistryEntry
	if err := json.Unmarshal(result, &entries); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entity registry: %w", err)
	}

	if entries == nil {
		return nil, fmt.Errorf("expected registry array, got null")
	}

	return entries, nil
}

// buildWSURL converts the HTTP base URL to a WebSocket URL
func (c *WSClient) buildWSURL() (string, error) {
	if err := haconfig.ValidateURL(c.baseURL); err != nil {
		return "", err
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}

	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/websocket"

	return parsed.String(), nil
}
