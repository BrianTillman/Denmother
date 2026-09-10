package hatest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAssertionDeadlineCancelsInflightHTTP(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { close(started); <-req.Context().Done() }))
	defer server.Close()
	runner := &TestRunner{client: NewTestClient(server.URL, "synthetic")}
	before := time.Now()
	err := runner.waitForAssertion(Assertion{EntityID: "input_boolean.study_lamp", State: "on"}, 300*time.Millisecond)
	if err == nil {
		t.Fatal("stalled request passed")
	}
	if time.Since(before) > time.Second {
		t.Fatal("HTTP request outlived assertion deadline")
	}
	select {
	case <-started:
	default:
		t.Fatal("did not exercise in-flight request")
	}
}

func TestCancellationStillRunsCleanupAndBlocksLaterCases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cleanup := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/services/input_boolean/turn_on" {
			cancel()
		}
		if req.URL.Path == "/api/services/input_boolean/turn_off" {
			cleanup++
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	runner := &TestRunner{client: NewTestClient(server.URL, "synthetic").WithContext(ctx)}
	disabled := false
	config := TestConfig{Cleanup: true, AutoRestore: &disabled}
	first := runner.runTestCase(TestCase{Name: "cancel", Trigger: []ServiceCall{{Service: "input_boolean.turn_on"}}, Cleanup: []ServiceCall{{Service: "input_boolean.turn_off"}}}, config)
	if first.Status != StatusFailed || cleanup != 1 {
		t.Fatalf("result=%+v cleanup=%d", first, cleanup)
	}
	second := runner.runTestCase(TestCase{Name: "later"}, config)
	if second.Phase != "blocked" {
		t.Fatalf("continued after cancellation: %+v", second)
	}
}

func TestDeclaredTraceAssertionsCannotBeSilentlySkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { http.NotFound(w, req) }))
	defer server.Close()
	runner := &TestRunner{client: NewTestClient(server.URL, "synthetic"), wsURL: server.URL, wsToken: "synthetic"}
	result := runner.runTestCase(TestCase{Name: "trace required", TraceAssertions: &TraceAssertions{Automation: "automation.study_motion_lamp"}}, TestConfig{})
	if result.Status != StatusFailed || result.Phase != "trace" {
		t.Fatalf("unavailable declared trace passed without --trace: %+v", result)
	}
}

func TestRestoreAutomationAndTimerUsesServices(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	runner := &TestRunner{client: NewTestClient(server.URL, "synthetic")}
	err := runner.restoreSnapshots([]entitySnapshot{
		{EntityID: "automation.study_motion_lamp", State: "on", Exists: true},
		{EntityID: "timer.study_hold", State: "paused", Exists: true, Attributes: map[string]interface{}{"remaining": "0:00:30"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/api/services/automation/turn_on", "/api/services/timer/start", "/api/services/timer/pause"}
	if fmt.Sprint(paths) != fmt.Sprint(expected) {
		t.Fatalf("restoration used %v", paths)
	}
}
