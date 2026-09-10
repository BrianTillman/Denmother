package hatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/haaudit"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

func runtimeMockServer(t *testing.T, command func(map[string]interface{}) (interface{}, bool), rest http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/websocket" {
			if rest != nil {
				rest(w, req)
			} else {
				http.NotFound(w, req)
			}
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
		var auth hasync.WSAuthMessage
		if conn.ReadJSON(&auth) != nil {
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})
		for {
			var request map[string]interface{}
			if conn.ReadJSON(&request) != nil {
				return
			}
			result, success := command(request)
			response := map[string]interface{}{"id": request["id"], "type": "result", "success": success, "result": result}
			if !success {
				response["error"] = map[string]interface{}{"code": "unsupported_test_command"}
			}
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestServiceTargetAcceptsNativeListsAndRejectsUnknown(t *testing.T) {
	var call ServiceCall
	if err := yaml.Unmarshal([]byte("service: light.turn_on\ntarget:\n  entity_id: [light.a, light.b]\n  device_id: [device-a, device-b]\n  area_id: kitchen\n"), &call); err != nil {
		t.Fatal(err)
	}
	data, err := serviceCallPayload(call)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data["entity_id"], []string{"light.a", "light.b"}) || data["area_id"] != "kitchen" {
		t.Fatalf("bad target: %#v", data)
	}
	if err := yaml.Unmarshal([]byte("target: {unknown_selector: x}"), &call); err == nil {
		t.Fatal("unknown target silently ignored")
	}
	if _, err := serviceCallPayload(ServiceCall{Target: ServiceTarget{EntityID: "light.a"}, Data: map[string]interface{}{"entity_id": "light.b"}}); err == nil {
		t.Fatal("conflicting targets accepted")
	}
}

func TestRuntimeDeviceAreaTargetResolutionFreezesSnapshotEntities(t *testing.T) {
	server := runtimeMockServer(t, func(command map[string]interface{}) (interface{}, bool) {
		if command["type"] != "extract_from_target" {
			t.Errorf("unexpected command: %v", command)
			return nil, false
		}
		target := command["target"].(map[string]interface{})
		if target["device_id"] != "device-a" && target["area_id"] != "kitchen" {
			t.Errorf("wrong target: %v", target)
		}
		return map[string]interface{}{"referenced_entities": []string{"sensor.temperature", "light.b", "light.a"}}, true
	}, nil)
	runner := &TestRunner{client: NewTestClient(server.URL, "test"), wsURL: server.URL, wsToken: "test"}
	defer runner.Close()
	tc := TestCase{Trigger: []ServiceCall{{Service: "light.turn_on", Target: ServiceTarget{DeviceID: "device-a"}}}, Cleanup: []ServiceCall{{Service: "light.turn_off", Target: ServiceTarget{AreaID: "kitchen"}}}}
	resolved, err := runner.resolveCaseTargets(tc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(touchedEntities(resolved), []string{"light.a", "light.b"}) {
		t.Fatalf("wrong snapshot coverage: %v", touchedEntities(resolved))
	}
	if resolved.Trigger[0].Target.DeviceID != "" || resolved.Cleanup[0].Target.AreaID != "" {
		t.Fatal("dynamic selector not frozen")
	}
	if tc.Trigger[0].Target.DeviceID != "device-a" {
		t.Fatal("resolver mutated reusable spec")
	}
}

func TestMissingRuntimeTargetFailsBeforeMutation(t *testing.T) {
	server := runtimeMockServer(t, func(command map[string]interface{}) (interface{}, bool) {
		return map[string]interface{}{"referenced_entities": []string{}, "missing_devices": []string{"gone"}}, true
	}, func(w http.ResponseWriter, r *http.Request) {
		t.Error("missing target must fail before REST mutation or snapshot")
	})
	runner := &TestRunner{client: NewTestClient(server.URL, "test"), wsURL: server.URL, wsToken: "test"}
	defer runner.Close()
	result := runner.runTestCase(TestCase{Name: "missing", Setup: []StateAction{{EntityID: "input_boolean.x", State: "on"}}, Trigger: []ServiceCall{{Service: "light.turn_on", Target: ServiceTarget{DeviceID: "gone"}}}}, TestConfig{Cleanup: true})
	if result.Status != StatusFailed || result.Phase != "target" {
		t.Fatalf("bad missing-target result: %+v", result)
	}
}

func TestTraceServiceAndEventCaptureContexts(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]interface{}
	server := runtimeMockServer(t, func(command map[string]interface{}) (interface{}, bool) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, command)
		return map[string]interface{}{"context": map[string]interface{}{"id": fmt.Sprintf("context-%d", len(requests))}}, true
	}, nil)
	runner := &TestRunner{client: NewTestClient(server.URL, "test"), wsURL: server.URL, wsToken: "test", traceCapture: true}
	defer runner.Close()
	if err := runner.callTriggerService("input_boolean", "turn_on", map[string]interface{}{"entity_id": "input_boolean.motion"}); err != nil {
		t.Fatal(err)
	}
	if err := runner.fireTriggerEvent("denmother_test", map[string]interface{}{"pressed": true}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.traceContextIDs, []string{"context-1", "context-2"}) {
		t.Fatalf("lost attribution: %v", runner.traceContextIDs)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests[0]["type"] != "call_service" || requests[1]["type"] != "fire_event" {
		t.Fatalf("wrong commands: %v", requests)
	}
	if requests[0]["target"].(map[string]interface{})["entity_id"] != "input_boolean.motion" {
		t.Fatal("target missing from service command")
	}
	if _, exists := requests[0]["service_data"].(map[string]interface{})["entity_id"]; exists {
		t.Fatal("target duplicated in service data")
	}
}

func TestTraceAttributionRejectsConcurrentPassingRun(t *testing.T) {
	now := time.Now()
	traces := []haaudit.AutomationTrace{{RunID: "unrelated-pass", State: "stopped", Timestamp: now}, {RunID: "ours-fails", State: "stopped", Timestamp: now.Add(-time.Hour)}}
	full := map[string]*FullTrace{
		"unrelated-pass": {Context: TraceContext{ID: "other", ParentID: "other-client"}, State: "stopped"},
		"ours-fails":     {Context: TraceContext{ID: "ours", ParentID: "command"}, State: "stopped", Error: "action failed"},
	}
	expect := &TraceAssertions{ExpectActions: []ExpectedAction{{Service: "light.turn_on"}}}
	selected, got, err := selectAttributedTrace(traces, nil, []string{"command"}, expect, func(id string) (*FullTrace, error) { return full[id], nil })
	if err != nil {
		t.Fatal(err)
	}
	if selected.RunID != "ours-fails" || got.Error != "action failed" {
		t.Fatalf("selected unrelated or passing run: %+v %+v", selected, got)
	}
	// Two service actions can create two legitimate runs. Never guess by picking
	// whichever satisfies assertions; a trigger index is explicit attribution.
	full["unrelated-pass"].Context.ParentID = "second-command"
	if _, _, err := selectAttributedTrace(traces, nil, []string{"command", "second-command"}, expect, func(id string) (*FullTrace, error) { return full[id], nil }); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity hidden: %v", err)
	}
	index := 1
	runner := &TestRunner{traceContextIDs: []string{"second-command", "command"}}
	selected, _, err = selectAttributedTrace(traces, nil, runner.traceContexts(&TraceAssertions{TriggerIndex: &index}), expect, func(id string) (*FullTrace, error) { return full[id], nil })
	if err != nil || selected.RunID != "ours-fails" {
		t.Fatalf("trigger index failed: %+v %v", selected, err)
	}
	traces[0].State = "running"
	if _, _, err := selectAttributedTrace(traces, nil, []string{"command", "second-command"}, expect, func(id string) (*FullTrace, error) { return full[id], nil }); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("running concurrent trace hidden: %v", err)
	}
}

func TestRuntimeTraceRejectsErrorOnlyInFullTrace(t *testing.T) {
	server := runtimeMockServer(t, func(command map[string]interface{}) (interface{}, bool) {
		switch command["type"] {
		case "config/entity_registry/list":
			return []interface{}{}, true
		case "trace/list":
			return []map[string]interface{}{{"run_id": "ours", "state": "stopped"}}, true
		case "trace/get":
			return FullTrace{RunID: "ours", State: "stopped", Context: TraceContext{ID: "execution", ParentID: "command"}, Error: "service failed"}, true
		default:
			t.Errorf("unexpected command: %v", command)
			return nil, false
		}
	}, nil)
	runner := &TestRunner{client: NewTestClient(server.URL, "test"), wsURL: server.URL, wsToken: "test", traceContextIDs: []string{"command"}}
	defer func() {
		if runner.wsClient != nil {
			runner.wsClient.Close()
		}
	}()
	result := &TestCaseResult{Status: StatusPassed}
	runner.runTraceValidation(TestCase{TraceAssertions: &TraceAssertions{Automation: "automation.test"}}, time.Now(), nil, result)
	if result.Status != StatusFailed || !strings.Contains(result.TraceError, "service failed") {
		t.Fatalf("full trace error ignored: %+v", result)
	}
}

func TestRestoreHelpersAndControlsThroughServices(t *testing.T) {
	for _, tc := range []struct {
		snapshot entitySnapshot
		paths    []string
		field    string
		value    interface{}
	}{
		{entitySnapshot{EntityID: "input_number.level", State: "2.5"}, []string{"input_number/set_value"}, "value", 2.5},
		{entitySnapshot{EntityID: "counter.events", State: "7"}, []string{"counter/set_value"}, "value", float64(7)},
		{entitySnapshot{EntityID: "input_text.name", State: "hello"}, []string{"input_text/set_value"}, "value", "hello"},
		{entitySnapshot{EntityID: "input_select.mode", State: "quiet"}, []string{"input_select/select_option"}, "option", "quiet"},
		{entitySnapshot{EntityID: "input_datetime.alarm", State: "07:30:00", Attributes: map[string]interface{}{"has_date": false}}, []string{"input_datetime/set_datetime"}, "time", "07:30:00"},
		{entitySnapshot{EntityID: "light.desk", State: "on", Attributes: map[string]interface{}{"brightness": 120.0, "color_mode": "color_temp", "color_temp_kelvin": 3000.0}}, []string{"light/turn_on"}, "brightness", 120.0},
		{entitySnapshot{EntityID: "switch.desk", State: "off"}, []string{"switch/turn_off"}, "entity_id", "switch.desk"},
		{entitySnapshot{EntityID: "cover.blinds", State: "open", Attributes: map[string]interface{}{"current_position": 40.0}}, []string{"cover/set_cover_position"}, "position", 40.0},
		{entitySnapshot{EntityID: "lock.door", State: "locked"}, []string{"lock/lock"}, "entity_id", "lock.door"},
		{entitySnapshot{EntityID: "fan.desk", State: "on", Attributes: map[string]interface{}{"percentage": 50.0}}, []string{"fan/turn_on"}, "percentage", 50.0},
		{entitySnapshot{EntityID: "climate.room", State: "heat", Attributes: map[string]interface{}{"temperature": 21.0}}, []string{"climate/set_hvac_mode", "climate/set_temperature"}, "hvac_mode", "heat"},
	} {
		t.Run(tc.snapshot.EntityID, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				paths = append(paths, strings.TrimPrefix(r.URL.Path, "/api/services/"))
				var data map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
					t.Error(err)
				}
				if len(paths) == 1 && !reflect.DeepEqual(data[tc.field], tc.value) {
					t.Errorf("wrong restore data: %#v", data)
				}
				_ = json.NewEncoder(w).Encode([]interface{}{})
			}))
			defer server.Close()
			runner := &TestRunner{client: NewTestClient(server.URL, "test")}
			if err := runner.restoreSnapshot(tc.snapshot); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(paths, tc.paths) {
				t.Fatalf("restore used %v, want %v", paths, tc.paths)
			}
		})
	}
}

func TestDirectMockRestoreAndUnsupportedServiceState(t *testing.T) {
	var mu sync.Mutex
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	runner := &TestRunner{client: NewTestClient(server.URL, "test").WithContext(context.Background())}
	if err := runner.restoreSnapshot(entitySnapshot{EntityID: "light.mock", State: "off", Direct: true}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if path != "/api/states/light.mock" {
		t.Fatalf("mock restored as real device: %s", path)
	}
	path = ""
	mu.Unlock()
	if err := runner.restoreSnapshot(entitySnapshot{EntityID: "vacuum.real", State: "cleaning"}); err == nil {
		t.Fatal("claimed to restore ongoing cleaning state")
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "" {
		t.Fatal("unsupported restoration mutated state")
	}
}

func TestFreshUniqueAttributionRequiresReasonAndRejectsConcurrentRuns(t *testing.T) {
	tc := TestCase{Name: "timer", Trigger: []ServiceCall{{Service: "timer.start"}}, Assertions: []Assertion{{EntityID: "input_boolean.lamp", State: "on"}}, TraceAssertions: &TraceAssertions{Automation: "automation.timer", Attribution: "fresh_unique"}}
	if err := tc.Validate(); err == nil {
		t.Fatal("accepted undocumented context fallback")
	}
	tc.TraceAssertions.AttributionReason = "timer.finished loses start context in this isolated runtime"
	if err := tc.Validate(); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]struct{}{"old": {}}
	traces := []haaudit.AutomationTrace{{RunID: "old", State: "stopped"}, {RunID: "new", State: "running"}}
	candidate, err := uniqueFreshTrace(traces, baseline)
	if err != nil || candidate.RunID != "new" {
		t.Fatalf("wrong unique run: %+v %v", candidate, err)
	}
	traces = append(traces, haaudit.AutomationTrace{RunID: "concurrent", State: "stopped"})
	if _, err := uniqueFreshTrace(traces, baseline); err == nil {
		t.Fatal("concurrent run accepted as unique")
	}
	index := 0
	tc.TraceAssertions.TriggerIndex = &index
	if err := tc.Validate(); err == nil {
		t.Fatal("fresh-window attribution cannot claim a command index")
	}
}

func TestTouchedEntitiesDoNotIncludeArbitraryServiceData(t *testing.T) {
	call := ServiceCall{Service: "notify.example", Data: map[string]interface{}{"message": "https://example.com", "temperature": 21.5, "entity_id": "light.desk"}}
	if got := serviceCallEntityIDs(call); !reflect.DeepEqual(got, []string{"light.desk"}) {
		t.Fatalf("data misidentified as mutated entities: %v", got)
	}
}
