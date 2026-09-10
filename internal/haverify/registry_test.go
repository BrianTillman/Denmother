package haverify

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BrianTillman/Denmother/internal/hasync"
)

func TestFindStaleRestoredAutomations(t *testing.T) {
	states := []hasync.EntityState{
		{EntityID: "automation.retired_beta", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "retired_beta"}},
		{EntityID: "automation.active", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "active_id"}},
		{EntityID: "automation.unavailable_not_restored", State: "unavailable", Attributes: map[string]interface{}{"restored": false, "id": "not_restored"}},
		{EntityID: "automation.restored_but_available", State: "off", Attributes: map[string]interface{}{"restored": true, "id": "available_id"}},
		{EntityID: "automation.missing_id", State: "unavailable", Attributes: map[string]interface{}{"restored": true}},
		{EntityID: "automation.non_string_id", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": 42}},
		{EntityID: "light.retired", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "retired_light"}},
	}
	localIDs := map[string]struct{}{"active_id": {}}

	got := FindStaleRestoredAutomations(states, localIDs)
	want := []StaleRestoredAutomation{{EntityID: "automation.retired_beta", UniqueID: "retired_beta"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindStaleRestoredAutomations() = %#v, want %#v", got, want)
	}
}

func TestFindStaleRestoredAutomationsSortsResults(t *testing.T) {
	states := []hasync.EntityState{
		{EntityID: "automation.zeta", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "zeta"}},
		{EntityID: "automation.alpha", State: "unavailable", Attributes: map[string]interface{}{"restored": true, "id": "alpha"}},
	}

	got := FindStaleRestoredAutomations(states, nil)
	if len(got) != 2 || got[0].EntityID != "automation.alpha" || got[1].EntityID != "automation.zeta" {
		t.Fatalf("expected deterministic entity order, got %#v", got)
	}
}

func TestLoadAutomationIDs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "presence", "sample.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("- id: stable_id\n  alias: Stable\n- alias: No ID\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadAutomationIDs(root)
	if err != nil {
		t.Fatalf("LoadAutomationIDs() error = %v", err)
	}
	if _, ok := got["stable_id"]; !ok || len(got) != 1 {
		t.Fatalf("LoadAutomationIDs() = %#v, want stable_id only", got)
	}
}
