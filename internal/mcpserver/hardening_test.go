package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkerResultContract(t *testing.T) {
	valid, _ := json.Marshal(failure("validate", "rule", "failure"))
	if _, err := decodeWorkerResult(valid); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	tests := map[string]func(map[string]any){
		"missing run ID":       func(v map[string]any) { delete(v, "run_id") },
		"empty command":        func(v map[string]any) { v["command"] = "" },
		"unknown field":        func(v map[string]any) { v["secret"] = "unexpected" },
		"status exit mismatch": func(v map[string]any) { v["exit_code"] = 0 },
		"invalid step status":  func(v map[string]any) { v["steps"].([]any)[0].(map[string]any)["status"] = "passed" },
		"invalid time":         func(v map[string]any) { v["started_at"] = "yesterday" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(valid, &doc); err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			data, _ := json.Marshal(doc)
			if _, err := decodeWorkerResult(data); err == nil {
				t.Fatal("accepted malformed operator result")
			}
		})
	}
	for _, data := range [][]byte{[]byte(`null`), append(append([]byte{}, valid...), valid...)} {
		if _, err := decodeWorkerResult(data); err == nil {
			t.Fatal("accepted invalid framing")
		}
	}
}

func writeProjectInput(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectPreflightFollowsReferences(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		files        map[string]string
		reject       bool
	}{
		{"nested include", "", map[string]string{"ha-config/configuration.yaml": "automation: !include ../shared/first.yaml\n", "shared/first.yaml": "nested: !include ../../outside.yaml\n"}, true},
		{"directory include in cache", "", map[string]string{"ha-config/configuration.yaml": "automation: !include_dir_merge_list ../.cache/shared\n", ".cache/shared/first.yaml": "nested: !include ../../../outside.yaml\n"}, true},
		{"second document", "", map[string]string{"ha-config/configuration.yaml": "first: true\n---\nnested: !include ../../outside.yaml\n"}, true},
		{"config under skipped directory", "dist/config", map[string]string{"dist/config/configuration.yaml": "nested: !include ../../../outside.yaml\n"}, true},
		{"blueprint alias", "", map[string]string{"ha-config/configuration.yaml": "path: &path ../../../../outside.yaml\nuse_blueprint:\n  path: *path\n"}, true},
		{"blueprint merge", "", map[string]string{"ha-config/configuration.yaml": "defaults: &defaults\n  path: ../../../../outside.yaml\nuse_blueprint:\n  <<: *defaults\n"}, true},
		{"nested local include", "", map[string]string{"ha-config/configuration.yaml": "nested: !include ../shared/first.yaml\n", "shared/first.yaml": "nested: !include second.yaml\n", "shared/second.yaml": "value: true\n"}, false},
		{"include cycle", "", map[string]string{"ha-config/configuration.yaml": "nested: !include ../shared/first.yaml\n", "shared/first.yaml": "nested: !include ../ha-config/configuration.yaml\n"}, false},
		{"alias cycle", "", map[string]string{"ha-config/configuration.yaml": "value: &value [*value]\n"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range tc.files {
				writeProjectInput(t, root, path, content)
			}
			a, err := New(Config{ProjectRoot: root, ConfigDir: tc.config})
			if a != nil {
				a.Close()
			}
			if (err != nil) != tc.reject {
				t.Fatalf("reject=%v: %v", tc.reject, err)
			}
		})
	}
}

func TestPolicySizeBoundAtStartup(t *testing.T) {
	for _, config := range []string{"", "ha-config"} {
		t.Run("config="+config, func(t *testing.T) {
			root := t.TempDir()
			writeProjectInput(t, root, ".denmother.yaml", strings.Repeat("x", MaxResultBytes+1))
			a, err := New(Config{ProjectRoot: root, ConfigDir: config})
			if a != nil {
				a.Close()
			}
			if err == nil {
				t.Fatal("accepted oversized project policy")
			}
		})
	}
}

func TestCredentialRedactionKeysAndExcerptBoundaries(t *testing.T) {
	root := fixture(t)
	token := strings.Repeat("\"", 40) + "credential-tail"
	a, cs := connect(t, Config{ProjectRoot: root, Token: token})
	result := failure("validate", "rule", "failure")
	result.Steps[0].Details = map[string]any{token: map[string]any{"nested": token}, "counter": json.Number("9007199254740993")}
	got := a.present(result).StructuredContent.(*operator.Result)
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "credential-tail") || !strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("redaction: %s", data)
	}
	stored, err := os.ReadFile(filepath.Join(root, a.evidence.entries[got.Evidence]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "credential-tail") {
		t.Fatal("persisted credential in JSON key")
	}
	encoded, _ := json.Marshal(token)
	for _, tc := range []struct{ name, content, want string }{
		{"escaped credential", strings.Repeat("x", MaxExcerptBytes-4) + string(encoded[1:len(encoded)-1]), "[RED"},
		{"raw credential", strings.Repeat("x", MaxExcerptBytes-4) + token, "[RED"},
		{"invalid UTF8", "before\xffafter", "before�after"},
		{"split UTF8", strings.Repeat("x", MaxExcerptBytes-1) + "界", strings.Repeat("x", MaxExcerptBytes-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeProjectInput(t, root, "excerpt.txt", tc.content)
			uri, err := a.evidence.register("excerpt.txt", "test")
			if err != nil {
				t.Fatal(err)
			}
			read, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
			if err != nil {
				t.Fatal(err)
			}
			var wrapper struct {
				Excerpt   string `json:"excerpt"`
				Truncated bool   `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(read.Contents[0].Text), &wrapper); err != nil {
				t.Fatal(err)
			}
			if len(wrapper.Excerpt) > MaxExcerptBytes || !utf8.ValidString(wrapper.Excerpt) || !strings.HasSuffix(wrapper.Excerpt, tc.want) {
				t.Fatalf("invalid excerpt: len=%d suffix=%q", len(wrapper.Excerpt), wrapper.Excerpt[max(0, len(wrapper.Excerpt)-20):])
			}
			if len(tc.content) > MaxExcerptBytes && !wrapper.Truncated {
				t.Fatal("missing truncation marker")
			}
		})
	}
}

func TestRuntimeKeyCanonicalization(t *testing.T) {
	for _, pair := range [][2]string{
		{"http://HA.example:80/", "http://ha.example"},
		{"https://HA.example.:443/prefix/", "https://ha.example/prefix"},
		{"http://[::1]:80/", "http://[::1]"},
	} {
		if runtimeKey(pair[0]) != runtimeKey(pair[1]) {
			t.Errorf("equivalent targets differ: %v", pair)
		}
	}
	if runtimeKey("http://ha.example/Case") == runtimeKey("http://ha.example/case") {
		t.Fatal("case-sensitive paths collapsed")
	}
	if runtimeKey("http://ha.example:8123") == runtimeKey("http://ha.example:8124") {
		t.Fatal("different ports collapsed")
	}
}

func TestDuplicateTestFilesAreRejectedBeforeExecution(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	_, cs := connect(t, Config{ProjectRoot: root, AllowTests: true, TargetURL: "http://127.0.0.1:1"})
	result := call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml", "ha-config/tests/../tests/mcp_test.yaml"}})
	if result.ErrorCode != "invalid_arguments" {
		t.Fatalf("duplicate files: %+v", result)
	}
}

func TestCanceledRuntimeWaitNeverExecutes(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	target := "http://127.0.0.1:1"
	unlock, err := lockRuntime(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, cs := connect(t, Config{ProjectRoot: root, TargetURL: target, AllowTests: true, Timeout: 100 * time.Millisecond})
	result := call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}})
	if result.ErrorCode != "operation_canceled" || !strings.Contains(result.Summary, "no tests started") {
		t.Fatalf("canceled lock waiter: %+v", result)
	}
}
