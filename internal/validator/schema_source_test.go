package validator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaRequiresSelectedAndCurrentSource(t *testing.T) {
	config := newSchemaConfigDir(t)
	contents := []byte("homeassistant:\n  name: Example\n")
	os.WriteFile(filepath.Join(config, "configuration.yaml"), contents, 0600)
	old := commandOutput
	defer func() { commandOutput = old }()
	mounted := config
	loaded := map[string]string{"configuration.yaml": fmt.Sprintf("%x", sha256.Sum256(contents))}
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "inspect" {
			return json.Marshal([]map[string]string{{"Source": mounted, "Destination": "/ha-config-source"}})
		}
		return json.Marshal(loaded)
	}
	if err := verifySchemaSource(config, "example"); err != nil {
		t.Fatal(err)
	}
	mounted = filepath.Join(t.TempDir(), "different-config")
	if err := verifySchemaSource(config, "example"); err == nil {
		t.Fatal("accepted another project")
	}
	mounted = config
	loaded["configuration.yaml"] = "stale"
	if err := verifySchemaSource(config, "example"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("accepted stale runtime: %v", err)
	}
}
