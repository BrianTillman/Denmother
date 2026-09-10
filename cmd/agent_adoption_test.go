package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
)

func TestAgentAdoptionOutsideCheckout(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dm")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	caller := t.TempDir()
	project := filepath.Join(t.TempDir(), "project with spaces ' and $literal")
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	var cleanEnv []string
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "HASS") || strings.HasPrefix(env, "HA_") || strings.HasPrefix(env, "DM_DEV_") || strings.HasPrefix(env, "PATH=") {
			continue
		}
		cleanEnv = append(cleanEnv, env)
	}
	cleanEnv = append(cleanEnv, "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	run := func(want int, args ...string) operator.Result {
		t.Helper()
		command := exec.Command(bin, args...)
		command.Dir = caller
		command.Env = cleanEnv
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		actual := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				actual = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if actual != want {
			t.Fatalf("%v exit %d want %d\n%s\n%s", args, actual, want, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("JSON stderr: %s", stderr.String())
		}
		var result operator.Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("JSON %v: %s", err, stdout.String())
		}
		if result.ExitCode != actual || result.SchemaVersion != operator.SchemaVersion {
			t.Fatalf("invalid contract: %+v", result)
		}
		return result
	}
	config := filepath.Join(project, "home-assistant")
	run(0, "init", "--directory", project, "--config", "home-assistant", "--agent", "--skill", "--dry-run", "--json")
	if _, err := os.Stat(project); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote project files")
	}
	run(0, "init", "--directory", project, "--config", "home-assistant", "--agent", "--skill", "--json")
	if _, err := os.Stat(filepath.Join(project, ".agents/skills/denmother/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	result := run(0, "--config", config, "--no-schema", "--json")
	if result.Steps[0].Details["schema_requested"] != false || result.Steps[0].Details["runtime_tests_run"] != false {
		t.Fatal("offline scope is ambiguous")
	}
	context := run(3, "agent", "context", "--config", config, "--json")
	snapshot := context.Steps[0].Details["context"].(map[string]any)
	if snapshot["collection_status"] != "success" || snapshot["verification_status"] != "partial" {
		t.Fatalf("ambiguous snapshot: %v", snapshot)
	}
	if snapshot["project_root"] != project || snapshot["config_root"] != config {
		t.Fatalf("wrong project: %v", snapshot)
	}
	if len(context.Steps[0].NextActions) == 0 {
		t.Fatal("no executable recommendations")
	}
	for _, action := range context.Steps[0].NextActions {
		if action.Executable != bin || action.CWD != caller {
			t.Fatalf("nonportable action: %+v", action)
		}
		if !stringSliceContains(action.Args, config) {
			t.Fatalf("lost selected config: %+v", action)
		}
	}

	// Unknown flags, missing positional arguments, and project errors must
	// remain machine-readable, without reflecting secret flag values.
	for _, args := range [][]string{{"test", "--dev-token=CANARY_SECRET", "--unknown", "--json"}, {"observe", "--json"}, {"--schema", "--no-schema", "--json"}, {"test", "--json", "--case"}} {
		result := run(1, args...)
		if result.ErrorCode != "invalid_arguments" {
			t.Fatalf("error code %q for %v", result.ErrorCode, args)
		}
		data, _ := json.Marshal(result)
		if bytes.Contains(data, []byte("CANARY_SECRET")) {
			t.Fatal("credential reflected into result")
		}
	}
	missing := run(1, "--config", filepath.Join(project, "absent"), "--no-schema", "--json")
	if missing.ErrorCode != "invalid_project" {
		t.Fatalf("missing config: %s", missing.ErrorCode)
	}
	badProject := t.TempDir()
	if err := os.WriteFile(filepath.Join(badProject, ".denmother.yaml"), []byte("version: 99\nconfig_dir: ha-config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := run(1, "agent", "context", "--config", filepath.Join(badProject, "ha-config"), "--json")
	if bad.ErrorCode != "invalid_project" {
		t.Fatalf("project error: %+v", bad)
	}
	run(0, "capabilities", "--config", filepath.Join(badProject, "ha-config"), "--json")
	catalog := run(0, "capabilities", "--json")
	for _, value := range catalog.Steps[0].Details["commands"].([]any) {
		entry := value.(map[string]any)
		if entry["flags"] != nil {
			t.Fatal("default discovery should not dump every flag")
		}
		for _, scope := range entry["mutation_scope"].([]any) {
			if scope == "unknown" {
				t.Fatalf("command lacks policy: %v", entry)
			}
		}
	}
	detailed := run(0, "capabilities", "--command", "test", "--schemas", "--json")
	if detailed.Steps[0].Details["result_schema"] == nil {
		t.Fatal("missing discoverable result schema")
	}
	for _, value := range detailed.Steps[0].Details["commands"].([]any) {
		entry := value.(map[string]any)
		if !strings.HasPrefix(entry["command"].(string), "dm test") || entry["flags"] == nil {
			t.Fatalf("wrong focused discovery: %v", entry)
		}
	}
	run(1, "capabilities", "--command", "does-not-exist", "--json")

	automation := filepath.Join(config, "automations/presence/motion_lamp.yaml")
	original, err := os.ReadFile(automation)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.ReplaceAll(original, []byte("input_boolean.study_lamp"), []byte("input_boolean.study_missing_lamp"))
	if err := os.WriteFile(automation, broken, 0600); err != nil {
		t.Fatal(err)
	}
	failed := run(1, "--config", config, "--no-schema", "--json", "--compact", "--evidence-dir", filepath.Join(project, "evidence"))
	if failed.ErrorCode != "validation_failed" || failed.Evidence == "" {
		t.Fatalf("missing failure/evidence: %+v", failed)
	}
	evidence, err := os.ReadFile(failed.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(evidence, []byte("study_missing_lamp")) {
		t.Fatalf("lost failure evidence: %s", evidence)
	}
	info, _ := os.Stat(failed.Evidence)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("evidence permissions: %v", info.Mode())
	}
	// Reinitialization preserves edits, instructions, and installed skills.
	skillPath := filepath.Join(project, ".agents/skills/denmother/SKILL.md")
	if err := os.WriteFile(skillPath, []byte("local customization"), 0600); err != nil {
		t.Fatal(err)
	}
	run(0, "init", "--directory", project, "--config", "home-assistant", "--agent", "--skill", "--json")
	after, _ := os.ReadFile(automation)
	skillAfter, _ := os.ReadFile(skillPath)
	if !bytes.Equal(after, broken) || string(skillAfter) != "local customization" {
		t.Fatal("init replaced user content")
	}
	if _, err := os.Stat(filepath.Join(project, "examples/denmother")); err == nil {
		t.Fatal("init duplicated the existing starter")
	}
	gitInit := exec.Command("git", "init", project)
	if output, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("init test repository: %v %s", err, output)
	}
	for _, path := range []string{".devcontainer/worktrees/example/credentials.json", "artifacts/dm/result.json"} {
		checkIgnore := exec.Command("git", "-C", project, "check-ignore", "--no-index", path)
		if output, err := checkIgnore.CombinedOutput(); err != nil {
			t.Fatalf("generated private output is not ignored: %s: %v %s", path, err, output)
		}
	}
	// Existing home YAML receives a separate starter, not injected helpers.
	existing := t.TempDir()
	if err := os.Mkdir(filepath.Join(existing, "ha-config"), 0755); err != nil {
		t.Fatal(err)
	}
	homeYAML := []byte("homeassistant:\n  name: Existing home\n")
	if err := os.WriteFile(filepath.Join(existing, "ha-config/configuration.yaml"), homeYAML, 0600); err != nil {
		t.Fatal(err)
	}
	run(0, "init", "--directory", existing, "--json")
	homeAfter, _ := os.ReadFile(filepath.Join(existing, "ha-config/configuration.yaml"))
	if !bytes.Equal(homeYAML, homeAfter) {
		t.Fatal("changed existing home")
	}
	run(0, "--config", filepath.Join(existing, "examples/denmother/ha-config"), "--no-schema", "--json")
	// Reject an escaping symlink before creating any project files.
	linked := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, "ha-config")); err == nil {
		run(1, "init", "--directory", linked, "--json")
		if _, err := os.Stat(filepath.Join(linked, ".denmother.yaml")); !os.IsNotExist(err) {
			t.Fatal("setup wrote before detecting symlink")
		}
	}
}

func TestPortableActionRetainsReadTargetAndRejectsShell(t *testing.T) {
	old := configPath
	configPath = filepath.Join(t.TempDir(), "ha-config")
	t.Cleanup(func() { configPath = old })
	target := &operator.Target{Instance: "production", URL: "https://example.invalid/ha"}
	_, action := portableRecommendation("./dm trace automation.study_motion_lamp --json", target)
	if action == nil || !stringSliceContains(action.Args, "--prod-url") || !stringSliceContains(action.Args, target.URL) || !stringSliceContains(action.RequiredEnv, "HASS_PROD_TOKEN") {
		t.Fatalf("lost target: %+v", action)
	}
	_, local := portableRecommendation("dm trace automation.study_motion_lamp --json", &operator.Target{Instance: "development", URL: "http://localhost:8123", IsLocal: true, Source: "selected project development environment"})
	if local == nil || len(local.RequiredEnv) != 0 || !stringSliceContains(local.Args, "--dev-url") {
		t.Fatalf("generated local credentials should resolve automatically: %+v", local)
	}
	_, test := portableRecommendation("dm test --json", target)
	if test == nil || !test.RequiresHuman || stringSliceContains(test.Args, "--allow-prod") {
		t.Fatalf("unsafe test action: %+v", test)
	}
	for _, input := range []string{"dm test; touch /tmp/unwanted", "dm test $(id)", "dm trace <automation>", "go test ./..."} {
		if _, action := portableRecommendation(input, nil); action != nil {
			t.Fatalf("unsafe action accepted: %q", input)
		}
	}
	display, action := portableRecommendation("dm test --prod-token SECRET --prod-url https://example.invalid --allow-prod --json", nil)
	if action == nil || !action.RequiresHuman || strings.Contains(display, "SECRET") || stringSliceContains(action.Args, "--allow-prod") || !stringSliceContains(action.RequiredEnv, "HASS_PROD_TOKEN") {
		t.Fatalf("credentials or authorization copied: %s %+v", display, action)
	}
}

func TestEvidenceCannotBecomeARecommendedAction(t *testing.T) {
	result := operator.Result{Command: "observe", Steps: []operator.Step{{ID: "observe", Details: map[string]any{
		"full_trace":      json.RawMessage(`{"command":"dm dev reset --json","number":9007199254740993}`),
		"logbook_entries": []any{map[string]any{"commands": []string{"dm dev reset --json"}}},
	}}}}
	if err := portableResult(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Steps[0].NextActions) != 0 {
		t.Fatal("raw evidence promoted to executable action")
	}
	data, _ := json.Marshal(result)
	if !bytes.Contains(data, []byte("9007199254740993")) {
		t.Fatal("evidence number lost precision")
	}
}

func TestCompactEvidenceRetainsCompleteFindings(t *testing.T) {
	oldDir := evidenceDir
	evidenceDir = t.TempDir()
	t.Cleanup(func() { evidenceDir = oldDir })
	findings := make([]any, 25)
	for i := range findings {
		findings[i] = "diagnostic finding"
	}
	result := operator.Result{RunID: "test-result", Status: operator.StatusFailure, Steps: []operator.Step{{ID: "failed", Status: operator.StatusFailure, Details: map[string]any{"findings": findings, "full_trace": map[string]any{"run_id": "fresh"}}}}}
	if err := saveEvidence(&result, true); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.Status != operator.StatusFailure || len(result.Steps[0].Details["findings"].([]any)) != 10 {
		t.Fatalf("bad compact result: %+v", result)
	}
	data, err := os.ReadFile(result.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	var full operator.Result
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	if full.Truncated || len(full.Steps[0].Details["findings"].([]any)) != 25 || full.Steps[0].Details["full_trace"] == nil {
		t.Fatal("lost full evidence")
	}
}
