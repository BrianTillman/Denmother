package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const devFixtureSchemaVersion = "dev_state_fixtures.v1"

type devFixtureDocument struct {
	SchemaVersion string                      `json:"schema_version"`
	Description   string                      `json:"description,omitempty"`
	Source        map[string]any              `json:"source,omitempty"`
	Entities      map[string]devScenarioState `json:"entities"`
}

// Accept legacy flat entity maps. For versioned envelopes, reject unknown
// versions, unknown fields, and invalid records.
func decodeDevFixtureDocument(data []byte) (*devFixtureDocument, error) {
	var keys map[string]json.RawMessage
	if err := strictDevJSON(data, &keys); err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, fmt.Errorf("fixtures require a JSON object")
	}
	document := &devFixtureDocument{SchemaVersion: devFixtureSchemaVersion}
	_, envelope := keys["entities"]
	if _, present := keys["schema_version"]; present {
		envelope = true
	}
	if envelope {
		if err := strictDevJSON(data, document); err != nil {
			return nil, err
		}
		if document.SchemaVersion != devFixtureSchemaVersion {
			return nil, fmt.Errorf("unsupported fixture schema_version %q; expected %s", document.SchemaVersion, devFixtureSchemaVersion)
		}
		if _, present := keys["schema_version"]; !present {
			return nil, fmt.Errorf("fixture envelope requires schema_version %q", devFixtureSchemaVersion)
		}
	} else {
		if err := strictDevJSON(data, &document.Entities); err != nil {
			return nil, err
		}
	}
	if document.Entities == nil {
		return nil, fmt.Errorf("fixtures require an entities object")
	}
	if len(document.Entities) > 0 {
		if err := validateDevStates(document.Entities); err != nil {
			return nil, err
		}
	}
	return document, nil
}

func strictDevJSON(data []byte, value any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON must contain valid UTF-8")
	}
	// encoding/json replaces unpaired UTF-16 surrogate escapes with U+FFFD;
	// reject those escapes rather than changing a user's intended HA state.
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		if i+1 >= len(data) {
			break
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			break
		}
		code, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil {
			break
		}
		i += 5
		if code >= 0xD800 && code <= 0xDBFF {
			if i+7 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return fmt.Errorf("JSON contains an unpaired Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return fmt.Errorf("JSON contains an unpaired Unicode surrogate")
			}
			i += 6
		} else if code >= 0xDC00 && code <= 0xDFFF {
			return fmt.Errorf("JSON contains an unpaired Unicode surrogate")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}
