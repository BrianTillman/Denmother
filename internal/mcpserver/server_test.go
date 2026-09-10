package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "denmother-mcp-tests-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "dm")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", testBinary, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build MCP fixture: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func fixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if err := os.CopyFS(root, os.DirFS("../../examples/quickstart")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "fixture"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	return root
}
func connect(t *testing.T, cfg Config) (*Adapter, *mcp.ClientSession) {
	t.Helper()
	cfg.Executable = testBinary
	cfg.Version = "test"
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := a.Server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "Denmother acceptance client", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close(); a.Close() })
	return a, cs
}
func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *operator.Result {
	t.Helper()
	got, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(got.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result operator.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	if result.SchemaVersion != operator.SchemaVersion || result.RunID == "" {
		t.Fatalf("invalid envelope: %s", data)
	}
	if got.IsError != (result.Status == operator.StatusFailure) {
		t.Fatalf("transport/domain mismatch: %s", data)
	}
	return &result
}
func cli(t *testing.T, root string, args ...string) *operator.Result {
	t.Helper()
	args = append(args, "--config="+filepath.Join(root, "ha-config"), "--json")
	c := exec.Command(testBinary, args...)
	c.Dir = root
	out, err := c.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatal(err)
		}
	}
	var r operator.Result
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("CLI JSON: %v %s", err, out)
	}
	return &r
}

func TestDiscoveryAndStrictArguments(t *testing.T) {
	root := fixture(t)
	_, cs := connect(t, Config{ProjectRoot: root, Token: "synthetic-secret"})
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 6 {
		t.Fatalf("tools: %v", listed.Tools)
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == (tool.Name == "run_tests") || tool.OutputSchema == nil {
			t.Fatalf("tool contract: %+v", tool)
		}
	}
	discovery, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "denmother://discovery"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(discovery)
	if bytes.Contains(data, []byte("synthetic-secret")) || !bytes.Contains(data, []byte("credential_present")) {
		t.Fatalf("discovery: %s", data)
	}
	for _, tc := range []struct{ name, raw string }{
		{"project_context", `{"config":"/etc"}`}, {"project_context", `null`}, {"validate_config", `{"native_schema":null}`},
		{"validate_config", `{"native_schema":"false"}`}, {"inspect_automation", `{"automation":"--prod-url=bad"}`},
		{"inspect_automation", `{"automation":"automation.a","limit":-1}`}, {"run_tests", `{"files":[]}`},
		{"run_tests", `{"files":["ha-config/tests/presence/motion_lamp_test.yaml"],"allow_prod":true}`},
		{"review_change", `{"base":"--output=/tmp/file"}`}, {"review_change", `{"changed_files":["../escape.yaml"]}`},
	} {
		t.Run(tc.name+tc.raw, func(t *testing.T) {
			r := call(t, cs, tc.name, json.RawMessage(tc.raw))
			if r.ErrorCode != "invalid_arguments" {
				t.Fatalf("%+v", r)
			}
		})
	}
	r := call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/presence/motion_lamp_test.yaml"}})
	if r.ErrorCode != "guardrail_blocked" {
		t.Fatalf("guardrail: %+v", r)
	}
}

func TestCLIParity(t *testing.T) {
	for _, kind := range []string{"valid", "broken-entity", "missing-inventory", "malformed-yaml", "malformed-project", "missing-config"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			switch kind {
			case "broken-entity":
				p := filepath.Join(root, "ha-config/automations/presence/motion_lamp.yaml")
				b, _ := os.ReadFile(p)
				os.WriteFile(p, bytes.ReplaceAll(b, []byte("input_boolean.study_lamp"), []byte("input_boolean.missing_lamp")), 0600)
			case "missing-inventory":
				os.Remove(filepath.Join(root, "docs/reference/entity-list.txt"))
			case "malformed-yaml":
				os.WriteFile(filepath.Join(root, "ha-config/automations/broken.yaml"), []byte("bad: ["), 0600)
			case "malformed-project":
				os.WriteFile(filepath.Join(root, "ha-config/.denmother.yaml"), []byte("bad: ["), 0600)
			case "missing-config":
				os.Rename(filepath.Join(root, "ha-config"), filepath.Join(root, "removed-config"))
			}
			_, cs := connect(t, Config{ProjectRoot: root})
			for _, tc := range []struct {
				tool string
				args []string
			}{{"validate_config", []string{"--no-schema"}}, {"project_context", []string{"agent", "context", "--no-schema"}}, {"plan_tests", []string{"test", "plan"}}, {"review_change", []string{"agent", "review"}}} {
				want := cli(t, root, tc.args...)
				got := call(t, cs, tc.tool, map[string]any{})
				if got.Status != want.Status || got.ExitCode != want.ExitCode || got.ErrorCode != want.ErrorCode {
					t.Fatalf("%s: MCP %s/%d/%s CLI %s/%d/%s (%s)", tc.tool, got.Status, got.ExitCode, got.ErrorCode, want.Status, want.ExitCode, want.ErrorCode, got.Summary)
				}
				if len(got.Steps) != len(want.Steps) {
					t.Fatalf("%s steps differ: %+v / %+v", tc.tool, got.Steps, want.Steps)
				}
				for i := range want.Steps {
					g, w := got.Steps[i], want.Steps[i]
					if g.Status != w.Status || g.RuleID != w.RuleID || g.ErrorCode != w.ErrorCode {
						t.Fatalf("step parity: %+v / %+v", g, w)
					}
				}
				if tc.tool == "project_context" && kind == "valid" {
					snapshot := got.Steps[0].Details["context"].(map[string]any)
					if snapshot["collection_status"] != "success" || snapshot["verification_status"] != "partial" {
						t.Fatalf("context: %v", snapshot)
					}
				}
			}
		})
	}
}

func TestEvidenceConfinementAndLimits(t *testing.T) {
	root := fixture(t)
	path := filepath.Join(root, "artifact.json")
	os.WriteFile(path, []byte(`{"trace":"fresh-failure","token":"synthetic-secret"}`+strings.Repeat("x", MaxExcerptBytes)), 0600)
	a, cs := connect(t, Config{ProjectRoot: root, Token: "synthetic-secret", EvidenceFiles: []string{"artifact.json"}})
	resources, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var uri string
	for _, r := range resources.Resources {
		if strings.HasPrefix(r.URI, "denmother://evidence/") {
			uri = r.URI
		}
	}
	if uri == "" || strings.Contains(uri, root) {
		t.Fatalf("opaque resource missing: %v", resources)
	}
	got, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	if bytes.Contains(data, []byte("synthetic-secret")) || !bytes.Contains(data, []byte("fresh-failure")) {
		t.Fatalf("redaction or trace: %s", data)
	}
	var excerpt struct {
		Truncated bool
		Excerpt   string
	}
	json.Unmarshal([]byte(got.Contents[0].Text), &excerpt)
	if !excerpt.Truncated || len(excerpt.Excerpt) > MaxExcerptBytes {
		t.Fatalf("unbounded excerpt: %d", len(excerpt.Excerpt))
	}
	os.Remove(path)
	outside := filepath.Join(t.TempDir(), "private.json")
	os.WriteFile(outside, []byte("PRIVATE"), 0600)
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri}); err == nil {
		t.Fatal("symlink swap escaped evidence root")
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "denmother://evidence/../../etc/passwd"}); err == nil {
		t.Fatal("arbitrary path resource read")
	}
	if err := a.checkProject(context.Background()); err == nil {
		t.Fatal("tool project preflight followed escaping symlink")
	}
}

func TestConfiguredBoundary(t *testing.T) {
	for _, kind := range []string{"production-tests", "outside-config", "outside-inventory", "include", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			cfg := Config{ProjectRoot: root, Executable: testBinary}
			switch kind {
			case "production-tests":
				cfg.TargetInstance = "production"
				cfg.TargetURL = "http://127.0.0.1:8123"
				cfg.AllowTests = true
			case "outside-config":
				cfg.ConfigDir = t.TempDir()
			case "outside-inventory":
				os.WriteFile(filepath.Join(root, ".denmother.yaml"), []byte("version: 1\nreferences_dir: /etc\n"), 0600)
			case "include":
				os.WriteFile(filepath.Join(root, "ha-config/configuration.yaml"), []byte("automation: !include ../../outside.yaml\n"), 0600)
			case "symlink", "dangling-symlink":
				target := t.TempDir()
				if kind == "dangling-symlink" {
					target = filepath.Join(target, "missing")
				}
				if err := os.Symlink(target, filepath.Join(root, "ha-config/escape.yaml")); err != nil {
					t.Skip(err)
				}
			}
			a, err := New(cfg)
			if err == nil {
				a.Close()
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
}

func writeRuntimeTest(t *testing.T, root, trigger string) {
	t.Helper()
	os.WriteFile(filepath.Join(root, "ha-config/tests/mcp_test.yaml"), []byte(`version: 1
name: MCP cleanup
config:
  cleanup: true
  auto_restore: false
tests:
  - name: controlled test
    trigger:
      - service: input_boolean.`+trigger+`
    assertions:
      - entity_id: input_boolean.lamp
        state: "on"
    cleanup:
      - service: input_boolean.turn_off
`), 0600)
}

func TestRuntimeCancellationCleanupAndSerialization(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	var inFlight, maxInFlight, cleanups atomic.Int32
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/services/input_boolean/turn_on" {
			_, _ = io.Copy(io.Discard, r.Body)
			n := inFlight.Add(1)
			for old := maxInFlight.Load(); n > old && !maxInFlight.CompareAndSwap(old, n); old = maxInFlight.Load() {
			}
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			inFlight.Add(-1)
		}
		if r.URL.Path == "/api/services/input_boolean/turn_off" {
			cleanups.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/api/states/") {
			fmt.Fprint(w, `{"entity_id":"input_boolean.lamp","state":"on","attributes":{}}`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	cfg := Config{ProjectRoot: root, TargetURL: server.URL, Token: "synthetic", AllowTests: true, Timeout: 4 * time.Second}
	first := connectStdio(t, cfg)
	second := connectStdio(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "run_tests", Arguments: map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}}})
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first test never started")
	}
	secondDone := make(chan *mcp.CallToolResult, 1)
	go func() {
		r, _ := second.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_tests", Arguments: map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}}})
		secondDone <- r
	}()
	select {
	case <-started:
		t.Fatal("shared runtime tests overlapped")
	case <-time.After(150 * time.Millisecond):
	}
	cancel()
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not propagate")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("next session never acquired released runtime")
	}
	if cleanups.Load() != 1 {
		t.Fatalf("next test started before cleanup: %d", cleanups.Load())
	}
	close(release)
	select {
	case r := <-secondDone:
		if r == nil || r.IsError {
			t.Fatalf("second result: %+v", r)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("second test did not complete")
	}
	if maxInFlight.Load() != 1 || cleanups.Load() != 2 {
		t.Fatalf("max concurrent=%d cleanup=%d", maxInFlight.Load(), cleanups.Load())
	}
}

func TestCleanupErrorAndTraceFailureParity(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	_, cs := connect(t, Config{ProjectRoot: root, TargetURL: server.URL, Token: "synthetic", AllowTests: true})
	got := call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}})
	if got.Status != operator.StatusFailure {
		t.Fatalf("cleanup passed: %+v", got)
	}
	data, _ := json.Marshal(got)
	if !bytes.Contains(data, []byte("cleanup_errors")) || !bytes.Contains(data, []byte(`"phase":"trigger"`)) {
		t.Fatalf("cleanup obscured primary failure: %s", data)
	}
	os.WriteFile(filepath.Join(root, "ha-config/tests/mcp_test.yaml"), []byte("version: 1\nname: trace\nconfig:\n  auto_restore: false\ntests:\n  - name: trace required\n    trigger:\n      - service: input_boolean.turn_on\n    assertions:\n      - entity_id: input_boolean.lamp\n        state: on\n    trace_assertions:\n      automation: automation.study_motion_lamp\n"), 0600)
	got = call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}})
	data, _ = json.Marshal(got)
	if got.Status != operator.StatusFailure || !bytes.Contains(data, []byte(`"phase":"trace"`)) || !bytes.Contains(data, []byte(`"trace_error"`)) {
		t.Fatalf("unavailable trace lost its diagnostics: %s", data)
	}
	observed := call(t, cs, "inspect_automation", map[string]any{"automation": "automation.study_motion_lamp"})
	if observed.Status != operator.StatusPartial {
		t.Fatalf("unavailable runtime evidence was not partial: %+v", observed)
	}
}

func TestIndependentRuntimeLocksAndCanceledWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := lockRuntime(ctx, "http://runtime-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other, err := lockRuntime(ctx, "http://runtime-b")
	if err != nil {
		t.Fatal(err)
	}
	other()
	short, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelShort()
	if unlock, err := lockRuntime(short, "http://runtime-a/"); err == nil {
		unlock()
		t.Fatal("same-runtime lock ignored")
	}
}

func TestConcurrentReadsPreserveIsolation(t *testing.T) {
	root := fixture(t)
	_, cs := connect(t, Config{ProjectRoot: root})
	var wg sync.WaitGroup
	statuses := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "validate_config", Arguments: map[string]any{}})
			if err != nil {
				statuses <- err.Error()
				return
			}
			data, _ := json.Marshal(r.StructuredContent)
			var result operator.Result
			json.Unmarshal(data, &result)
			statuses <- string(result.Status)
		}()
	}
	wg.Wait()
	close(statuses)
	for s := range statuses {
		if s != "success" {
			t.Fatalf("read isolation failed: %s", s)
		}
	}
}

func TestOperatorEvidencePreservesRemediation(t *testing.T) {
	root := fixture(t)
	a, _ := connect(t, Config{ProjectRoot: root})
	result := failure("validate", "bad_rule", "bad YAML")
	result.Steps[0].RuleID = "yaml-rule"
	result.Steps[0].NextActions = []operator.Action{{Executable: "dm", Args: []string{"--no-schema"}, CWD: root, MutationScope: []string{}}}
	want := result.Steps[0]
	got := a.present(result).StructuredContent.(*operator.Result)
	if !reflect.DeepEqual(got.Steps[0], want) {
		t.Fatalf("remediation changed: %+v", got)
	}
}

func TestStdioClientAndCredentialIsolation(t *testing.T) {
	root := fixture(t)
	command := exec.Command(testBinary, "mcp", "--project-root", root)
	command.Env = append(os.Environ(), "HASS_PROD_URL=http://127.0.0.1:1", "HASS_PROD_TOKEN=must-not-be-inherited", "HASS_TOKEN=legacy-must-not-be-inherited")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio acceptance client", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 40 * time.Second}, nil)
	if err != nil {
		t.Fatalf("stdio initialize: %v %s", err, stderr.String())
	}
	defer session.Close()
	result := call(t, session, "project_context", map[string]any{})
	if result.Status != operator.StatusPartial {
		t.Fatalf("stdio context: %+v", result)
	}
	data, _ := json.Marshal(result)
	if bytes.Contains(data, []byte("must-not-be-inherited")) {
		t.Fatalf("credential leak: %s", data)
	}
	result = call(t, session, "inspect_automation", map[string]any{"automation": "automation.study_motion_lamp"})
	if result.Status != operator.StatusFailure || result.Target != nil {
		t.Fatalf("fell back to an inherited target: %+v", result)
	}
}

func connectStdio(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	args := []string{"mcp", "--project-root", cfg.ProjectRoot, "--target-url", cfg.TargetURL, "--token-env", "DM_MCP_TEST_TOKEN", "--timeout", cfg.Timeout.String()}
	if cfg.AllowTests {
		args = append(args, "--allow-tests")
	}
	command := exec.Command(testBinary, args...)
	command.Env = append(os.Environ(), "DM_MCP_TEST_TOKEN="+cfg.Token)
	command.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "cross-process acceptance", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command, TerminateDuration: 40 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestCredentialRedactionDoesNotCorruptJSON(t *testing.T) {
	root := fixture(t)
	a, _ := connect(t, Config{ProjectRoot: root, Token: "secret\"with-quote"})
	result := failure("validate", "bad_rule", "secret\"with-quote")
	result.Steps[0].Details = map[string]any{"counter": json.Number("9007199254740993"), "secret": "secret\"with-quote"}
	got := a.present(result).StructuredContent.(*operator.Result)
	data, _ := json.Marshal(got)
	if !bytes.Contains(data, []byte("9007199254740993")) || bytes.Contains(data, []byte("with-quote")) || got.SchemaVersion != operator.SchemaVersion {
		t.Fatalf("redaction corrupted evidence: %s", data)
	}
}

func TestServerDeadlineReportsCleanup(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	var cleanup atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/services/input_boolean/turn_on" {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/api/services/input_boolean/turn_off" {
			cleanup.Add(1)
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	_, cs := connect(t, Config{ProjectRoot: root, TargetURL: server.URL, Token: "synthetic", AllowTests: true, Timeout: 500 * time.Millisecond})
	before := time.Now()
	result := call(t, cs, "run_tests", map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}})
	if time.Since(before) > 3*time.Second || result.Status != operator.StatusFailure || cleanup.Load() != 1 {
		t.Fatalf("deadline/cleanup: elapsed=%s cleanup=%d result=%+v", time.Since(before), cleanup.Load(), result)
	}
}

func TestEvidenceEvictionAndToolOutputLimit(t *testing.T) {
	root := fixture(t)
	a, cs := connect(t, Config{ProjectRoot: root})
	first := a.present(failure("validate", "rule", "failure")).StructuredContent.(*operator.Result).Evidence
	for i := 0; i < MaxResources; i++ {
		a.present(failure("validate", "rule", "failure"))
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: first}); err == nil {
		t.Fatal("expired resource remained readable")
	}
	listed, err := cs.ListResources(context.Background(), &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Resources) > MaxResources+1 {
		t.Fatal("resource catalog exceeded limit")
	}
	result := failure("validate", "rule", "failure")
	result.Steps[0].Details = map[string]any{"large": strings.Repeat("x", MaxToolBytes)}
	got := a.present(result).StructuredContent.(*operator.Result)
	if got.ErrorCode != "output_limit_exceeded" || got.Evidence == "" || !got.Truncated {
		t.Fatalf("output limit: %+v", got)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: got.Evidence}); err != nil {
		t.Fatal(err)
	}
}

func TestClientDisconnectStillCleansUp(t *testing.T) {
	root := fixture(t)
	writeRuntimeTest(t, root, "turn_on")
	started := make(chan struct{})
	var cleanup atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/services/input_boolean/turn_on" {
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/api/services/input_boolean/turn_off" {
			cleanup.Add(1)
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	command := exec.Command(testBinary, "mcp", "--project-root", root, "--target-url", server.URL, "--token-env", "DM_MCP_TEST_TOKEN", "--allow-tests", "--timeout", "10s")
	command.Env = append(os.Environ(), "DM_MCP_TEST_TOKEN=synthetic")
	transport := &capturedTransport{Transport: &mcp.CommandTransport{Command: command, TerminateDuration: 40 * time.Second}}
	client := mcp.NewClient(&mcp.Implementation{Name: "disconnect acceptance", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_tests", Arguments: map[string]any{"files": []string{"ha-config/tests/mcp_test.yaml"}}})
	}()
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("test did not start")
	}
	closed := make(chan struct{})
	go func() { _ = transport.connection.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(4 * time.Second):
		t.Fatal("disconnect did not cancel worker")
	}
	<-callDone
	if cleanup.Load() != 1 {
		t.Fatalf("disconnect lost cleanup: %d", cleanup.Load())
	}
}

type capturedTransport struct {
	mcp.Transport
	connection mcp.Connection
}

func (t *capturedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.Transport.Connect(ctx)
	t.connection = c
	return c, err
}
