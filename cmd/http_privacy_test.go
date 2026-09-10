package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sync/atomic"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/hatest"
)

func TestHTTPFailuresDoNotDiscloseResponseBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, r.Header.Get("Authorization")+" private diagnostic fixture", http.StatusBadGateway)
	}))
	defer server.Close()
	const token = "synthetic-test-token"
	client := hatest.NewTestClient(server.URL, token)
	for name, call := range map[string]func() error{
		"inventory": func() error { _, err := hasync.NewClient(server.URL, token).FetchEntities(); return err },
		"history": func() error {
			_, err := haaudit.FetchHistory(server.URL, token, []string{"sensor.example"}, time.Now().Add(-time.Hour), time.Now())
			return err
		},
		"logbook": func() error {
			return haaudit.EnrichHistoryContexts(server.URL, token, nil, []string{"sensor.example"}, time.Now().Add(-time.Hour), time.Now())
		},
		"scenario": func() error {
			return postHAJSON(&haconfig.HAConfig{URL: server.URL, Token: token}, "/api/states/sensor.example", map[string]string{"state": "on"})
		},
		"logs":         func() error { _, err := haGET(token, server.URL); return err },
		"service":      func() error { return client.CallService("input_boolean", "turn_on", nil) },
		"state":        func() error { _, err := client.GetEntityState("sensor.example"); return err },
		"set-state":    func() error { return client.SetEntityStateDirect("sensor.example", "on", nil) },
		"delete-state": func() error { return client.DeleteEntityState("sensor.example") },
		"event":        func() error { return client.FireEvent("example", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil || !strings.Contains(err.Error(), "502") {
				t.Fatalf("expected HTTP status evidence, got %v", err)
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "private diagnostic") {
				t.Fatal("HTTP error disclosed response body")
			}
		})
	}
}

func TestHTTPClientsDoNotForwardCredentialsOnRedirect(t *testing.T) {
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("[]"))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	for name, call := range map[string]func() error{
		"inventory": func() error { _, err := hasync.NewClient(origin.URL, "synthetic-token").FetchEntities(); return err },
		"service": func() error {
			return hatest.NewTestClient(origin.URL, "synthetic-token").CallService("light", "turn_on", nil)
		},
		"logs": func() error { _, err := haGET("synthetic-token", origin.URL); return err },
		"history": func() error {
			_, err := haaudit.FetchHistory(origin.URL, "synthetic-token", nil, time.Now(), time.Now())
			return err
		},
		"scenario": func() error {
			return postHAJSON(&haconfig.HAConfig{URL: origin.URL, Token: "synthetic-token"}, "/api/states/sensor.example", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			requests.Store(0)
			if err := call(); err == nil {
				t.Error("redirect returned success")
			}
			if requests.Load() != 0 {
				t.Error("request followed redirect to a different service")
			}
		})
	}
}
