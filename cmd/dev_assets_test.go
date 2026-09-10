package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func portableAssetsPython(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is required to exercise the HA preparation script")
	}
	if output, err := exec.Command(python, "-c", "import yaml").CombinedOutput(); err != nil {
		t.Skipf("PyYAML (provided by the HA runtime) is required to exercise the preparation script: %v: %s", err, output)
	}
	return python
}

func TestPortableAssetsRefreshAndPreserveStorage(t *testing.T) {
	python := portableAssetsPython(t)
	root := t.TempDir()
	source, runtime := filepath.Join(root, "source"), filepath.Join(root, "runtime")
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(source, "configuration.yaml"), "input_boolean:\n  lamp:\n")
	write(filepath.Join(source, "www", "card.js"), "customElements.define('demo-card', class extends HTMLElement {});")
	write(filepath.Join(source, "www", "private.pem"), "not a web asset")
	write(filepath.Join(source, "custom_components", "private", "config.yaml"), "excluded")
	write(filepath.Join(runtime, ".storage", "auth"), "preserve-me")
	script, err := filepath.Abs("devassets/prepare.py")
	if err != nil {
		t.Fatal(err)
	}
	run := func() ([]byte, error) {
		c := exec.Command(python, "-c", `import runpy,sys; runpy.run_path(sys.argv[1])["prepare"](sys.argv[2], sys.argv[3])`, script, source, runtime)
		return c.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("prepare: %v %s", err, output)
	}
	if data, err := os.ReadFile(filepath.Join(runtime, "www", "card.js")); err != nil || !strings.Contains(string(data), "demo-card") {
		t.Fatalf("asset missing: %s %v", data, err)
	}
	for _, path := range []string{"www/private.pem", "custom_components/private/config.yaml"} {
		if _, err := os.Stat(filepath.Join(runtime, path)); !os.IsNotExist(err) {
			t.Fatalf("copied excluded %s", path)
		}
	}
	var hashes map[string]string
	data, _ := os.ReadFile(filepath.Join(runtime, ".denmother-source.json"))
	json.Unmarshal(data, &hashes)
	if len(hashes) != 1 || hashes["configuration.yaml"] == "" {
		t.Fatalf("schema manifest includes static assets: %v", hashes)
	}
	if err := os.Remove(filepath.Join(source, "www", "card.js")); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err != nil {
		t.Fatalf("refresh: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(runtime, "www", "card.js")); !os.IsNotExist(err) {
		t.Fatal("stale web asset retained")
	}
	if data, _ := os.ReadFile(filepath.Join(runtime, ".storage", "auth")); string(data) != "preserve-me" {
		t.Fatal("storage overwritten")
	}
	outside := filepath.Join(root, "outside.js")
	write(outside, "private")
	if err := os.Symlink(outside, filepath.Join(source, "www", "escape.js")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "must stay within") {
		t.Fatalf("external symlink accepted: %s %v", output, err)
	}
}

func TestPortableAssetsParseRootKeysWithoutLosingHATags(t *testing.T) {
	python := portableAssetsPython(t)
	script, err := filepath.Abs("devassets/prepare.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, config string
	}{
		{"quoted", "\"frontend\": !include frontend.yaml\nhttp: {use_x_forwarded_for: true}\n"},
		{"indented", "  frontend: !include frontend.yaml\n  http: {use_x_forwarded_for: true}\n"},
		{"flow", "{frontend: !include frontend.yaml, http: {use_x_forwarded_for: true}}\n"},
		{"merged", "<<: &defaults {frontend: !include frontend.yaml}\nhttp: {use_x_forwarded_for: true}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source, runtime := filepath.Join(root, "source"), filepath.Join(root, "runtime")
			writeTestFile(t, filepath.Join(source, "configuration.yaml"), tc.config)
			writeTestFile(t, filepath.Join(source, "frontend.yaml"), "themes: {}\n")
			command := exec.Command(python, "-c", `import runpy,sys; runpy.run_path(sys.argv[1])["prepare"](sys.argv[2], sys.argv[3])`, script, source, runtime)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare: %v: %s", err, output)
			}
			data, err := os.ReadFile(filepath.Join(runtime, "configuration.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var rootMap map[string]any
			if err := yaml.Unmarshal(data, &rootMap); err != nil {
				t.Fatalf("invalid or duplicate runtime YAML: %v: %s", err, data)
			}
			for _, key := range []string{"http", "api", "websocket_api", "frontend"} {
				if _, ok := rootMap[key]; !ok {
					t.Errorf("runtime lacks %s: %s", key, data)
				}
			}
			if rootMap["frontend"] != "frontend.yaml" || !strings.Contains(string(data), "!include") {
				t.Fatalf("existing HA include tag was lost: %s", data)
			}
			httpSettings, ok := rootMap["http"].(map[string]any)
			if !ok || httpSettings["use_x_forwarded_for"] != true {
				t.Fatalf("existing integration settings were lost: %s", data)
			}
			original, err := os.ReadFile(filepath.Join(source, "configuration.yaml"))
			if err != nil || string(original) != tc.config {
				t.Fatal("source configuration was changed")
			}
			manifest, err := os.ReadFile(filepath.Join(runtime, ".denmother-source.json"))
			if err != nil {
				t.Fatal(err)
			}
			var hashes map[string]string
			if err := json.Unmarshal(manifest, &hashes); err != nil {
				t.Fatal(err)
			}
			if hashes["configuration.yaml"] != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.config))) {
				t.Fatalf("manifest must retain source identity: %s", manifest)
			}
		})
	}
}

func TestPortableAssetsRejectMalformedOrNonMappingRoot(t *testing.T) {
	python := portableAssetsPython(t)
	script, err := filepath.Abs("devassets/prepare.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{"frontend: [", "- frontend\n", "null\n", "", "frontend: {}\n---\nhttp: {}\n", "frontend: {}\nfrontend: {}\n"} {
		root := t.TempDir()
		source, runtime := filepath.Join(root, "source"), filepath.Join(root, "runtime")
		writeTestFile(t, filepath.Join(source, "configuration.yaml"), config)
		command := exec.Command(python, "-c", `import runpy,sys; runpy.run_path(sys.argv[1])["prepare"](sys.argv[2], sys.argv[3])`, script, source, runtime)
		if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "configuration.yaml") {
			t.Errorf("invalid configuration %q accepted or unexplained: %v: %s", config, err, output)
		}
		if fileExists(filepath.Join(runtime, ".denmother-source.json")) {
			t.Error("invalid runtime configuration must not record a successful source manifest")
		}
	}
}
