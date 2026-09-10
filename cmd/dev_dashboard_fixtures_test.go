package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureSchemaRejectsUnknownVersionsAndMalformedRecords(t *testing.T) {
	cases := []string{
		`{"schema_version":"dev_state_fixtures.v2","entities":{}}`,
		`{"schema_version":1,"entities":{}}`,
		`{"entities":{}}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":{},"typo":true}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":{"sensor.valid":{"state":"1","typo":true}}}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":{"sensor.valid":{"state":true}}}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":{"sensor.valid":null}}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":{"bad/entity":{"state":"1"}}}`,
		`{"schema_version":"dev_state_fixtures.v1","entities":null}`,
		`{"sensor.valid":{"state":"1"},"malformed":"ignored before"}`,
		`{"sensor.valid":{"state":"\ud800"}}`,
	}
	path := filepath.Join(t.TempDir(), "fixtures.json")
	for _, data := range cases {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadDevFixtureStates(path); err == nil {
			t.Fatalf("runtime accepted %s", data)
		}
		if _, err := readFixturePayload(path); err == nil {
			t.Fatalf("export merge accepted %s", data)
		}
	}
}

func TestDevStateUnicodeCharacterLimit(t *testing.T) {
	for _, tc := range []struct {
		state string
		valid bool
	}{{strings.Repeat("🌡", 255), true}, {strings.Repeat("é", 255), true}, {strings.Repeat("🌡", 256), false}, {string([]byte{0xff}), false}} {
		err := validateDevStates(map[string]devScenarioState{"sensor.test": {State: tc.state}})
		if (err == nil) != tc.valid {
			t.Fatalf("length %d valid %v: %v", len(tc.state), tc.valid, err)
		}
	}
	document, err := decodeDevFixtureDocument([]byte(`{"sensor.test":{"state":"\ud83d\ude00"}}`))
	if err != nil || document.Entities["sensor.test"].State != "😀" {
		t.Fatalf("valid surrogate pair: %v %v", document, err)
	}
	raw := append([]byte(`{"sensor.test":{"state":"`), 0xff)
	raw = append(raw, []byte(`"}}`)...)
	if _, err := decodeDevFixtureDocument(raw); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	// Escaped backslashes are text, not Unicode escape sequences.
	text := map[string]devScenarioState{"sensor.test": {State: `\ud800`}}
	data, _ := json.Marshal(text)
	if _, err := decodeDevFixtureDocument(data); err != nil {
		t.Fatalf("literal escape rejected: %v", err)
	}
}
