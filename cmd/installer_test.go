package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BrianTillman/Denmother/internal/install"
	"github.com/BrianTillman/Denmother/internal/operator"
)

func TestInstallerReleaseOutsideCheckout(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native bootstrap hosts")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "dm")
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X github.com/BrianTillman/Denmother/cmd.version=0.1.0-rc.1", "-o", binary, ".")
	build.Dir = ".."
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	name := fmt.Sprintf("denmother-0.1.0-rc.1-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(dir, name)
	if e := writeReleaseArchive(archive, "..", binary); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(archive)
	os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)), 0600)
	home := filepath.Join(t.TempDir(), "home with spaces")
	os.MkdirAll(home, 0755)
	binDir := filepath.Join(home, "bin")
	env := append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "CODEX_HOME="+filepath.Join(home, "codex"), "CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	run := func(executable string, args ...string) operator.Result {
		t.Helper()
		c := exec.Command(executable, args...)
		c.Env = env
		c.Dir = home
		b, e := c.Output()
		var result operator.Result
		if json.Unmarshal(b, &result) != nil {
			t.Fatalf("invalid JSON %v: %v %s", args, e, b)
		}
		exit := 0
		if e != nil {
			var process *exec.ExitError
			if !errors.As(e, &process) {
				t.Fatalf("process failed without an exit code: %v", e)
			}
			exit = process.ExitCode()
		}
		if exit != result.ExitCode || result.SchemaVersion != operator.SchemaVersion || result.StartedAt.IsZero() || result.EndedAt.IsZero() {
			t.Fatalf("process/envelope mismatch: process=%d result=%s", exit, b)
		}
		return result
	}
	// Keep the already installed toolchain and module cache available while HOME
	// is isolated; offline source mode must never acquire a Go toolchain.
	goEnv := exec.Command("go", "env", "GOPATH", "GOCACHE", "GOROOT")
	cacheOutput, err := goEnv.Output()
	if err != nil {
		t.Fatal(err)
	}
	cachePaths := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(cachePaths) != 3 {
		t.Fatalf("unexpected go env output: %q", cacheOutput)
	}
	env = append(env, "GOPATH="+cachePaths[0], "GOCACHE="+cachePaths[1], "PATH="+binDir+string(os.PathListSeparator)+filepath.Join(cachePaths[2], "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	setup, _ := filepath.Abs("../setup")
	args := []string{setup, "--archive", archive, "--offline", "--bin-dir", binDir, "--harness", "codex,claude", "--json"}
	dry := run("bash", append(args, "--dry-run")...)
	if dry.ExitCode != 0 && dry.ExitCode != 2 && dry.ExitCode != 3 {
		t.Fatal(dry)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("dry-run modified home")
	}
	first := run("bash", args...)
	if first.ExitCode != 0 {
		t.Fatalf("fresh installation: %+v", first)
	}
	again := run("bash", args...)
	if again.ExitCode != 0 {
		t.Fatal(again)
	}
	var details struct {
		Installation install.Report `json:"installation"`
	}
	encoded, _ := json.Marshal(again.Steps[0].Details)
	json.Unmarshal(encoded, &details)
	for _, op := range details.Installation.Operations {
		if op.State != "skipped" {
			t.Fatal("repeat installation changed files", op)
		}
	}
	// Corruption never replaces the active binary and errors remain parseable.
	os.WriteFile(archive, []byte("corrupt"), 0600)
	bad := run("bash", args...)
	if bad.ExitCode != 1 || bad.Steps[0].ErrorCode != "checksum_failure" {
		t.Fatal(bad)
	}
	os.RemoveAll(dir)
	installed := filepath.Join(binDir, "dm")
	status := run(installed, "skills", "status", "--harness", "codex,claude", "--json")
	if status.ExitCode != 0 {
		t.Fatal(status)
	}
	for _, path := range []string{filepath.Join(home, ".agents", "skills", "denmother", "references", "testing.md"), filepath.Join(home, "claude", "skills", "denmother", "references", "diagnosis.md")} {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("archive removal broke registration", e)
		}
	}
	missing := run(installed, "skills", "install", "--harness", "unknown", "--json")
	if missing.ExitCode != 1 {
		t.Fatal("invalid input accepted")
	}
	// Rejected read-only flags must not write artifacts through error rendering.
	for _, command := range [][]string{
		{"skills", "status", "--json", "--evidence-dir", filepath.Join(home, "must-not-exist")},
		{"skills", "install", "--json", "--dry-run", "--compact"},
		{"setup", "--json", "--dry-run", "--evidence-dir", filepath.Join(home, "must-not-exist"), "--unknown"},
		{"skills", "install", "--json", "--destination", ""},
	} {
		rejected := run(installed, command...)
		if rejected.ExitCode != 1 {
			t.Fatal("invalid flags accepted", rejected)
		}
		for _, path := range []string{filepath.Join(home, "must-not-exist"), filepath.Join(home, "artifacts")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rejected read-only command created %s", path)
			}
		}
	}
	// Explicit source mode remains dev and works with networking disabled.
	sourceBin := filepath.Join(home, "source bin")
	source := run("bash", setup, "--from-source", "--offline", "--binary-only", "--bin-dir", sourceBin, "--json")
	if source.ExitCode != 2 {
		t.Fatalf("source install should report PATH shadowing: %+v", source)
	}
	sourceVersion := run(filepath.Join(sourceBin, "dm"), "version", "--json")
	if sourceVersion.Steps[0].Details["version"] != "dev" {
		t.Fatal("source build presented as release")
	}
	// Installation commands must not parse project settings or touch HA.
	os.WriteFile(filepath.Join(home, ".denmother.yaml"), []byte("invalid: ["), 0600)
	status = run(installed, "skills", "status", "--harness", "codex", "--json")
	if status.ExitCode != 0 {
		t.Fatal("skills depended on HA project")
	}
}
func TestBootstrapSecuritySuite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Python bootstrap is Unix only")
	}
	command := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "bootstrap_test.py", "-v")
	command.Dir = ".."
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("bootstrap security tests: %v\n%s", e, output)
	}
	if !strings.Contains(string(output), "OK") {
		t.Fatal(string(output))
	}
}
