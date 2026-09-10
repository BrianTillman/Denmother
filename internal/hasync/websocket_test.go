package hasync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func websocketTestServer(t *testing.T, serve func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		serve(conn)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestConnectContextBoundsAuthenticationReads(t *testing.T) {
	for _, stage := range []string{"auth_required", "auth_response"} {
		t.Run(stage, func(t *testing.T) {
			closed := make(chan struct{})
			server := websocketTestServer(t, func(conn *websocket.Conn) {
				defer close(closed)
				if stage == "auth_response" {
					conn.WriteJSON(WSMessage{Type: "auth_required"})
					var auth WSAuthMessage
					if err := conn.ReadJSON(&auth); err != nil {
						return
					}
				}
				// Withhold the next message until the client closes the socket.
				conn.ReadMessage()
			})
			client := NewWSClient(server.URL, "synthetic-test-token")
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			if err := client.ConnectContext(ctx); err == nil {
				t.Fatal("authentication without a server response succeeded")
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("authentication exceeded caller deadline: %s", elapsed)
			}
			if client.conn != nil {
				t.Fatal("failed authentication retained a connection")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed authentication did not close the socket")
			}
		})
	}
}

func TestConnectContextCancellationClosesHandshake(t *testing.T) {
	ready := make(chan struct{})
	server := websocketTestServer(t, func(conn *websocket.Conn) {
		close(ready)
		conn.ReadMessage()
	})
	client := NewWSClient(server.URL, "synthetic-test-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- client.ConnectContext(ctx) }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("server did not receive upgrade")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt authentication")
	}
}

func TestConnectRejectsInvalidAuthentication(t *testing.T) {
	for _, response := range []string{"auth_invalid", "unexpected"} {
		t.Run(response, func(t *testing.T) {
			server := websocketTestServer(t, func(conn *websocket.Conn) {
				conn.WriteJSON(WSMessage{Type: "auth_required"})
				var auth WSAuthMessage
				if conn.ReadJSON(&auth) == nil {
					conn.WriteJSON(WSMessage{Type: response})
				}
			})
			client := NewWSClient(server.URL, "synthetic-test-token")
			err := client.Connect()
			if err == nil || client.conn != nil {
				t.Fatalf("invalid auth accepted: conn=%v err=%v", client.conn, err)
			}
			if strings.Contains(err.Error(), client.token) {
				t.Fatal("authentication error disclosed token")
			}
		})
	}
}

func TestConnectionRemainsUsableAfterAuthenticationContextEnds(t *testing.T) {
	server := websocketTestServer(t, func(conn *websocket.Conn) {
		conn.WriteJSON(WSMessage{Type: "auth_required"})
		var auth WSAuthMessage
		if err := conn.ReadJSON(&auth); err != nil {
			t.Errorf("read auth: %v", err)
			return
		}
		if auth.Type != "auth" || auth.AccessToken != "synthetic-test-token" {
			t.Error("unexpected authentication message")
			return
		}
		conn.WriteJSON(WSMessage{Type: "auth_ok"})
		var cmd WSCommandMessage
		if err := conn.ReadJSON(&cmd); err != nil {
			t.Errorf("read command after auth: %v", err)
			return
		}
		conn.WriteJSON(WSMessage{ID: cmd.ID, Type: "result", Success: true, Result: []byte(`[]`)})
	})
	client := NewWSClient(server.URL, "synthetic-test-token")
	t.Cleanup(func() { client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := client.ConnectContext(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	<-ctx.Done()
	if _, err := client.FetchAreasWS(); err != nil {
		t.Fatalf("command after authentication context ended: %v", err)
	}
	if err := client.Connect(); err == nil {
		t.Fatal("reconnecting an open client should fail without leaking the old connection")
	}
	if err := client.Close(); err != nil || client.conn != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := client.SendCommand("config/area_registry/list"); err == nil {
		t.Fatal("command after close should fail")
	}
	if _, err := client.SendCommandWithParams("trace/list", nil); err == nil {
		t.Fatal("parameterized command after close should fail")
	}
}

func TestCommandDeadlineIncludesUnrelatedMessages(t *testing.T) {
	server := websocketTestServer(t, func(conn *websocket.Conn) {
		conn.WriteJSON(WSMessage{Type: "auth_required"})
		var auth WSAuthMessage
		conn.ReadJSON(&auth)
		conn.WriteJSON(WSMessage{Type: "auth_ok"})
		var command WSCommandMessage
		if conn.ReadJSON(&command) != nil {
			return
		}
		for i := 0; i < 100; i++ {
			if conn.WriteJSON(WSMessage{ID: command.ID + 1, Type: "event"}) != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	client := NewWSClient(server.URL, "synthetic")
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.SendCommandContext(ctx, "test", nil); err == nil {
		t.Fatal("missing response passed")
	}
	if time.Since(start) > time.Second {
		t.Fatal("unrelated traffic extended deadline")
	}
	if _, err := client.SendCommand("test"); err == nil {
		t.Fatal("reused timed-out socket")
	}
}
