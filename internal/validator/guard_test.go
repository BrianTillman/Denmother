package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectIntegrationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, yaml            string
		restricted, wantError bool
	}{
		{"unrestricted demo", "demo: {}\n", false, false},
		{"restricted flow", "light: [{platform: demo}]\n", true, true},
		{"comment and description", "# demo: {}\ninput_boolean: {study_demo: {name: 'platform: demo'}}\n", true, false},
		{"restricted key", "demo: {}\n", true, true},
		{"malformed", "light: [\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "ha-config")
			os.Mkdir(config, 0755)
			os.WriteFile(filepath.Join(config, "configuration.yaml"), []byte(tc.yaml), 0600)
			if tc.restricted {
				os.WriteFile(filepath.Join(root, ".denmother.yaml"), []byte("version: 1\nforbidden_integrations: [demo]\n"), 0600)
			}
			err := validateGuard(config)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
