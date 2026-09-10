package hatest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
)

// TestNativeRuntimeTargetsAndCleanup is opt-in: it creates native synthetic
// helpers in the selected project's local development instance, then deletes
// its own records. It never uses production/default environment credentials.
func TestNativeRuntimeTargetsAndCleanup(t *testing.T) {
	configPath := os.Getenv("DENMOTHER_RUNTIME_TEST_CONFIG")
	if configPath == "" {
		t.Skip("set DENMOTHER_RUNTIME_TEST_CONFIG to an isolated dm development config")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	config, err := haconfig.ResolveConfigLocalConfigContext(ctx, configPath)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(config.URL)
	if err != nil || !config.IsLocal || (endpoint.Hostname() != "localhost" && endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "::1") {
		t.Fatal("native runtime acceptance requires the selected project's loopback development instance")
	}
	runner, err := NewTestRunnerContext(ctx, config.URL, config.Token, false, false)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := runner.getWSClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	command := func(kind string, params map[string]interface{}, result interface{}) {
		t.Helper()
		raw, err := ws.SendCommandContext(ctx, kind, params)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if result != nil {
			if err := json.Unmarshal(raw, result); err != nil {
				t.Fatalf("decode %s: %v", kind, err)
			}
		}
	}
	var runtime struct {
		Version    string   `json:"version"`
		Components []string `json:"components"`
	}
	command("get_config", nil, &runtime)
	for _, domain := range []string{"input_boolean", "input_number", "input_select", "input_text", "counter"} {
		found := false
		for _, component := range runtime.Components {
			found = found || component == domain
		}
		if !found {
			t.Fatalf("fixture requires %s: {} in configuration.yaml; restart the development instance first", domain)
		}
	}
	t.Logf("native acceptance Home Assistant %s", runtime.Version)

	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "dm_accept_" + hex.EncodeToString(random[:])
	type createdRecord struct{ command, key, id string }
	var created []createdRecord
	// Separate connection/deadline makes teardown independent of test failure or
	// cancellation. Delete only server-returned IDs created by this invocation.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupWS := hasync.NewWSClient(config.URL, config.Token)
		cleanupWS.SetContext(cleanupCtx)
		if err := cleanupWS.ConnectContext(cleanupCtx); err != nil {
			t.Errorf("acceptance teardown connection: %v; remaining records=%v", err, created)
			return
		}
		defer cleanupWS.Close()
		for i := len(created) - 1; i >= 0; i-- {
			record := created[i]
			if _, err := cleanupWS.SendCommandContext(cleanupCtx, record.command, map[string]interface{}{record.key: record.id}); err != nil {
				t.Errorf("acceptance teardown %s %s: %v", record.command, record.id, err)
			}
		}
		entries, err := cleanupWS.FetchEntityRegistryWS()
		if err != nil {
			t.Errorf("verify acceptance entity removal: %v", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.UniqueID, prefix) {
				t.Errorf("acceptance entity remains after teardown: %s", entry.EntityID)
			}
		}
		var states []EntityState
		rawStates, stateErr := cleanupWS.SendCommandContext(cleanupCtx, "get_states", nil)
		if stateErr != nil {
			t.Errorf("verify acceptance state removal: %v", stateErr)
		} else if err := json.Unmarshal(rawStates, &states); err != nil {
			t.Errorf("decode acceptance states: %v", err)
		}
		for _, state := range states {
			if strings.Contains(state.EntityID, "."+prefix) {
				t.Errorf("acceptance state remains after teardown: %s", state.EntityID)
			}
		}
		var areas []struct {
			ID string `json:"area_id"`
		}
		raw, err := cleanupWS.SendCommandContext(cleanupCtx, "config/area_registry/list", nil)
		if err != nil {
			t.Errorf("verify acceptance area removal: %v", err)
		} else if err := json.Unmarshal(raw, &areas); err != nil {
			t.Errorf("decode acceptance areas: %v", err)
		}
		for _, area := range areas {
			if strings.HasPrefix(area.ID, prefix) {
				t.Errorf("acceptance area remains after teardown: %s", area.ID)
			}
		}
		t.Logf("teardown checked removal of %d synthetic helper/area records", len(created))
	})
	var area struct {
		ID string `json:"area_id"`
	}
	command("config/area_registry/create", map[string]interface{}{"name": prefix}, &area)
	if area.ID == "" {
		t.Fatal("created area has no ID")
	}
	created = append(created, createdRecord{"config/area_registry/delete", "area_id", area.ID})
	createHelper := func(domain, suffix string, fields map[string]interface{}, inArea bool) string {
		t.Helper()
		fields["name"] = prefix + "_" + suffix
		var helper struct {
			ID string `json:"id"`
		}
		command(domain+"/create", fields, &helper)
		if helper.ID == "" {
			t.Fatalf("%s create returned no ID", domain)
		}
		created = append(created, createdRecord{domain + "/delete", domain + "_id", helper.ID})
		entries, err := ws.FetchEntityRegistryWS()
		if err != nil {
			t.Fatal(err)
		}
		entityID := ""
		for _, entry := range entries {
			if entry.UniqueID == helper.ID && strings.HasPrefix(entry.EntityID, domain+".") {
				entityID = entry.EntityID
			}
		}
		if entityID == "" {
			t.Fatalf("created %s has no registry entity", domain)
		}
		if inArea {
			command("config/entity_registry/update", map[string]interface{}{"entity_id": entityID, "area_id": area.ID}, nil)
		}
		return entityID
	}
	boolean := createHelper("input_boolean", "boolean", map[string]interface{}{}, true)
	outsider := createHelper("input_boolean", "outside", map[string]interface{}{}, false)
	number := createHelper("input_number", "number", map[string]interface{}{"min": 0, "max": 100, "step": 0.5, "initial": 2.5}, true)
	selectID := createHelper("input_select", "select", map[string]interface{}{"options": []string{"quiet", "active"}, "initial": "quiet"}, true)
	textID := createHelper("input_text", "text", map[string]interface{}{"initial": "before"}, true)
	counter := createHelper("counter", "counter", map[string]interface{}{"initial": 7}, true)
	wantEntities := []string{boolean, number, selectID, textID, counter}
	sort.Strings(wantEntities)
	entities, err := runner.resolveTarget(map[string]interface{}{"area_id": area.ID})
	if err != nil || !reflect.DeepEqual(entities, wantEntities) {
		t.Fatalf("native area expansion: got %v want %v error=%v", entities, wantEntities, err)
	}
	caseConfig := TestConfig{Cleanup: true, Timeout: Duration(3 * time.Second)}
	testCase := TestCase{
		Name: "native area targets and helper restoration",
		Trigger: []ServiceCall{
			{Service: "input_boolean.turn_on", Target: ServiceTarget{AreaID: area.ID}},
			{Service: "input_number.set_value", Target: ServiceTarget{AreaID: area.ID}, Data: map[string]interface{}{"value": 19.5}},
			{Service: "input_select.select_option", Target: ServiceTarget{AreaID: area.ID}, Data: map[string]interface{}{"option": "active"}},
			{Service: "input_text.set_value", Target: ServiceTarget{AreaID: area.ID}, Data: map[string]interface{}{"value": "after"}},
			{Service: "counter.set_value", Target: ServiceTarget{AreaID: area.ID}, Data: map[string]interface{}{"value": 23}},
		},
		Assertions: []Assertion{
			{EntityID: boolean, State: "on"},
			{EntityID: number, State: "19.5"},
			{EntityID: selectID, State: "active"},
			{EntityID: textID, State: "after"},
			{EntityID: counter, State: "23"},
		},
	}
	resolved, err := runner.resolveCaseTargets(testCase)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := runner.snapshotTouchedEntities(resolved, caseConfig)
	if err != nil || len(snapshots) != len(wantEntities) {
		t.Fatalf("native target snapshots: %d error=%v", len(snapshots), err)
	}
	wantOriginal := map[string]string{boolean: "off", outsider: "off", number: "2.5", selectID: "quiet", textID: "before", counter: "7"}
	assertOriginal := func() {
		t.Helper()
		for entity, want := range wantOriginal {
			state, err := runner.client.GetEntityState(entity)
			if err != nil {
				t.Fatal(err)
			}
			if state.State != want {
				t.Errorf("native restoration %s: got %q want %q", entity, state.State, want)
			}
		}
	}
	assertOriginal()
	result := runner.runTestCase(testCase, caseConfig)
	if result.Status != StatusPassed {
		t.Fatalf("native area test: %+v", result)
	}
	assertOriginal()
	// Integration-owned values must also be restored: incrementing exposes a
	// stale internal value even when a direct state API write looks correct.
	for _, check := range []struct{ domain, entity, want string }{{"input_number", number, "3.0"}, {"counter", counter, "8"}} {
		if err := runner.client.CallService(check.domain, "increment", map[string]interface{}{"entity_id": check.entity}); err != nil {
			t.Fatal(err)
		}
		state, err := runner.client.GetEntityState(check.entity)
		if err != nil || state == nil || state.State != check.want {
			t.Fatalf("native %s internal value after restore: got %v want %s error=%v", check.domain, state, check.want, err)
		}
	}
	for _, snapshot := range snapshots {
		if err := runner.restoreSnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	// Assertion failure must still restore all service-domain values.
	testCase.Assertions = []Assertion{{EntityID: textID, State: "intentionally impossible", Timeout: Duration(150 * time.Millisecond)}}
	result = runner.runTestCase(testCase, caseConfig)
	if result.Status != StatusFailed || result.Phase != "assertion" || len(result.CleanupErrors) != 0 {
		t.Fatalf("failed-case cleanup: %+v", result)
	}
	assertOriginal()
	t.Logf("PASS native area extraction, five service-domain mutations/restorations, untouched outsider, integration-owned counter/number values, and failed-case cleanup (%s)", runtime.Version)
}
