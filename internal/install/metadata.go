package install

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"unicode"
)

const maxManifestSize = 1 << 20
const maxManagedFileSize = 128 << 20
const maxJournalSize = 384 << 20
const maxManagedFiles = 4096

// Metadata controls writes, so ambiguous JSON must not acquire ownership.
func decodeMetadata(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("metadata nesting limit exceeded")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid metadata key")
				}
				name = strings.ToLower(name)
				if seen[name] {
					return fmt.Errorf("duplicate metadata key")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("metadata must contain exactly one JSON value")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

// Keep manifests portable: reject Windows drive/ADS/device aliases on every OS.
func managedPath(name string) bool {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:<>\"|?*") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.HasPrefix(lower, ".denmother-") || strings.HasPrefix(lower, ".dm.") {
			return false
		}
		base, _, _ := strings.Cut(strings.ToUpper(part), ".")
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			return false
		}
		runes := []rune(base)
		if len(runes) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("0123456789¹²³", runes[3]) {
			return false
		}
	}
	return true
}
func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func validateManifest(m *Manifest, dest, kind string) error {
	if m.Format != 1 || m.Owner != "denmother" || m.ID == "" || m.Version == "" || m.Destination != dest || (m.Scope != "user" && m.Scope != "project") || m.Contract != "dm.operator.v1" || len(m.Files) == 0 || len(m.Files) > maxManagedFiles {
		return fmt.Errorf("invalid ownership manifest")
	}
	if kind == "binary" {
		if m.Harness != "" || len(m.Files) != 1 || m.Files[binaryName()] == "" {
			return fmt.Errorf("invalid binary ownership manifest")
		}
	} else if m.Harness != "codex" && m.Harness != "claude" {
		return fmt.Errorf("invalid skill ownership manifest")
	}
	seen := map[string]bool{}
	for name, hash := range m.Files {
		key := strings.ToLower(name)
		if !managedPath(name) || !validDigest(hash) || seen[key] {
			return fmt.Errorf("unsafe ownership entry: %s", name)
		}
		seen[key] = true
	}
	for key := range seen {
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return fmt.Errorf("overlapping ownership entries")
			}
		}
	}
	hashes, _ := json.Marshal(m.Files)
	if !validDigest(m.Digest) || m.Digest != digest(hashes) {
		return fmt.Errorf("ownership content digest mismatch")
	}
	return nil
}
func decodeManifest(data []byte, dest, kind string) (*Manifest, error) {
	if len(data) > maxManifestSize {
		return nil, fmt.Errorf("ownership manifest exceeds size limit")
	}
	var m Manifest
	if err := decodeMetadata(data, &m); err != nil {
		return nil, err
	}
	if err := validateManifest(&m, dest, kind); err != nil {
		return nil, err
	}
	return &m, nil
}
