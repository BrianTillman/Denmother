package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BrianTillman/Denmother/internal/operator"
)

func TestSafeJSONCommandsEmitContractEnvelope(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dm")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dm test binary: %v\n%s", err, string(output))
	}

	commands := [][]string{
		{"agent", "lint", "--json"},
		{"agent", "quality", "--json"},
		{"agent", "tasks", "--json"},
		{"agent", "next", "--json"},
		{"docs", "generate", "--check", "--json"},
		{"plan", "status", "--json"},
	}

	repoRoot := filepath.Join(t.TempDir(), "project")
	if err := os.CopyFS(repoRoot, os.DirFS("../examples/quickstart")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "fixture"}} {
		git := exec.Command("git", args...)
		git.Dir = repoRoot
		if output, err := git.CombinedOutput(); err != nil {
			t.Fatalf("prepare synthetic repo: %v: %s", err, output)
		}
	}
	generate := exec.Command(bin, "docs", "generate", "--json")
	generate.Dir = repoRoot
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture maps: %v: %s", err, output)
	}
	for _, args := range commands {
		args := args
		t.Run(args[0]+"-"+args[1], func(t *testing.T) {
			cmd := exec.Command(bin, args...)
			cmd.Dir = repoRoot
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dm %v failed: %v\n%s", args, err, string(output))
			}

			var result operator.Result
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode JSON: %v\n%s", err, string(output))
			}
			if result.SchemaVersion != operator.SchemaVersion {
				t.Fatalf("SchemaVersion = %q, want %q", result.SchemaVersion, operator.SchemaVersion)
			}
			if result.RunID == "" || result.Command == "" || result.Summary == "" {
				t.Fatalf("missing required envelope fields: %+v", result)
			}
			if result.ExitCode != 0 || result.Status != operator.StatusSuccess {
				t.Fatalf("unexpected command status: status=%s exit=%d summary=%s", result.Status, result.ExitCode, result.Summary)
			}
		})
	}
}
