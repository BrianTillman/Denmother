package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func environment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex home"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude home"))
	t.Setenv("PATH", filepath.Join(home, "bin"))
	return home
}
func opts() Options {
	return Options{Harnesses: []string{"codex", "claude"}, Scope: "user", Version: "dev", Origin: "source"}
}
func requireOK(t *testing.T, r *Report) {
	t.Helper()
	if r.Exit != 0 && r.Exit != 2 {
		b, _ := json.Marshal(r)
		t.Fatalf("exit %d: %s", r.Exit, b)
	}
}
func TestInstallLifecycle(t *testing.T) {
	home := environment(t)
	o := opts()
	first := Run(o, "install")
	requireOK(t, first)
	if len(first.Operations) != 2 {
		t.Fatal(first)
	}
	for _, op := range first.Operations {
		if !op.Verified || op.State != "applied" {
			t.Fatal(op)
		}
		for p := range op.Manifest.Files {
			if _, e := os.Stat(filepath.Join(op.Destination, p)); e != nil {
				t.Fatal(e)
			}
		}
	}
	path := filepath.Join(home, ".agents", "skills", "denmother", manifestName)
	before, _ := os.ReadFile(path)
	again := Run(o, "install")
	requireOK(t, again)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("identical install changed manifest")
	}
	for _, op := range again.Operations {
		if op.State != "skipped" {
			t.Fatal(op)
		}
	}
	extra := filepath.Join(first.Operations[0].Destination, "notes.txt")
	if e := os.WriteFile(extra, []byte("mine"), 0600); e != nil {
		t.Fatal(e)
	}
	o.Version = "0.2.0"
	upgrade := Run(o, "install")
	requireOK(t, upgrade)
	if upgrade.Operations[0].Manifest.Previous.Version != "dev" {
		t.Fatal("missing previous provenance")
	}
	if _, e := os.Stat(extra); e != nil {
		t.Fatal(e)
	}
	modified := filepath.Join(first.Operations[0].Destination, "SKILL.md")
	os.WriteFile(modified, []byte("my changes"), 0644)
	o.Version = "0.3.0"
	blocked := Run(o, "install")
	if blocked.Exit != 1 || blocked.ErrorCode != "destination_conflict" {
		t.Fatal(blocked)
	}
	// Planning all destinations precedes any writes.
	unchanged, _ := readManifest(filepath.Join(first.Operations[1].Destination, manifestName), first.Operations[1].Destination)
	if unchanged.Version != "0.2.0" {
		t.Fatal("partially applied blocked plan")
	}
	status := Run(o, "status")
	if status.Exit != 3 || len(status.Operations[0].Modified) != 1 {
		t.Fatal(status)
	}
	removed := Run(o, "uninstall")
	requireOK(t, removed)
	b, _ := os.ReadFile(modified)
	if string(b) != "my changes" {
		t.Fatal("removed edit")
	}
	if _, e := os.Stat(extra); e != nil {
		t.Fatal("removed unrelated content")
	}
	if _, e := os.Stat(first.Operations[1].Destination); !os.IsNotExist(e) {
		t.Fatal("unchanged registration remains")
	}
}
func TestScopeDiscoveryAndDryRun(t *testing.T) {
	home := environment(t)
	o := opts()
	o.DryRun = true
	requireOK(t, Run(o, "install"))
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("dry-run wrote files")
	}
	o.Harnesses = nil
	hs, e := Discover(o)
	if e != nil || len(hs) != 0 {
		t.Fatal(hs, e)
	}
	os.MkdirAll(os.Getenv("CODEX_HOME"), 0755)
	hs, _ = Discover(o)
	if len(hs) != 0 {
		t.Fatal("empty directory detected")
	}
	os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"), []byte("model='test'"), 0600)
	hs, _ = Discover(o)
	if len(hs) != 1 || hs[0].Name != "codex" {
		t.Fatal(hs)
	}
	o.Scope = "project"
	if Run(o, "install").Exit != 1 {
		t.Fatal("project fallback")
	}
	o.Project = t.TempDir()
	o.Harnesses = []string{"codex,claude", "codex"}
	o.DryRun = false
	r := Run(o, "install")
	requireOK(t, r)
	if len(r.Operations) != 2 {
		t.Fatal("duplicate registration")
	}
	for _, op := range r.Operations {
		if !strings.HasPrefix(op.Destination, o.Project) {
			t.Fatal(op)
		}
	}
	o.Destination = filepath.Join(t.TempDir(), "skill with spaces")
	if Run(o, "install").Exit != 1 {
		t.Fatal("ambiguous destination")
	}
	o.Harnesses = []string{"codex"}
	requireOK(t, Run(o, "install"))
	o.Harnesses = []string{"unknown"}
	if Run(o, "install").Exit != 1 {
		t.Fatal("unknown harness")
	}
}
func TestForeignAndSymlinkDestinations(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	o.Destination = filepath.Join(t.TempDir(), "foreign")
	os.Mkdir(o.Destination, 0755)
	r := Run(o, "install")
	if r.Exit != 1 {
		t.Fatal("foreign directory accepted")
	}
	target := t.TempDir()
	o.Destination = filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(target, o.Destination); e != nil {
		t.Skip(e)
	}
	if Run(o, "install").Exit != 1 {
		t.Fatal("symlink accepted")
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Fatal("symlink modified")
	}
}
func TestConcurrentInstallAndRecovery(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	hs, _ := Discover(o)
	unlock, e := lock(hs[0].Destination)
	if e != nil {
		t.Fatal(e)
	}
	blocked := Run(o, "install")
	if blocked.Exit != 1 {
		t.Fatal("concurrent installer accepted")
	}
	unlock()
	first := Run(o, "install")
	requireOK(t, first)
	op := first.Operations[0]
	path := filepath.Join(op.Destination, "SKILL.md")
	before, _ := os.ReadFile(path)
	tx := upgradeJournal(t, op, "SKILL.md", []byte("interrupted upgrade"))
	data, _ := json.Marshal(tx)
	os.WriteFile(op.manifestPath+".transaction", data, 0600)
	os.WriteFile(path, tx.Changes[0].After, 0644)
	if Run(o, "install").Exit != 3 {
		t.Fatal("interrupted upgrade not detected")
	}
	os.WriteFile(path, []byte("post-crash user edits"), 0644)
	o.Recover = true
	if Run(o, "install").Exit != 1 {
		t.Fatal("recovery overwrote user edit")
	}
	os.WriteFile(path, tx.Changes[0].After, 0644)
	requireOK(t, Run(o, "install"))
	got, _ := os.ReadFile(path)
	if string(got) != string(before) {
		t.Fatal("recovery failed")
	}
}
func TestBinaryOwnershipAndPATH(t *testing.T) {
	home := environment(t)
	o := opts()
	o.Binary = true
	o.BinaryOnly = true
	o.Harnesses = nil
	o.BinDir = filepath.Join(home, "bin")
	requireOK(t, Run(o, "install"))
	r := Run(o, "install")
	requireOK(t, r)
	if !r.Reachable || r.Shadowed || r.Operations[0].State != "skipped" {
		t.Fatal(r)
	}
	os.WriteFile(filepath.Join(o.BinDir, binaryName()), []byte("unrelated"), 0755)
	if Run(o, "install").Exit != 1 {
		t.Fatal("modified binary overwritten")
	}
	foreign := filepath.Join(home, "foreign")
	os.Mkdir(foreign, 0755)
	os.WriteFile(filepath.Join(foreign, binaryName()), []byte("unrelated"), 0755)
	o.BinDir = foreign
	if Run(o, "install").Exit != 1 {
		t.Fatal("foreign binary overwritten")
	}
	brew := filepath.Join(home, "Cellar", "denmother", "1.0", "bin")
	os.MkdirAll(brew, 0755)
	os.WriteFile(filepath.Join(brew, binaryName()), []byte("package-owned"), 0755)
	t.Setenv("PATH", brew)
	o.Binary = false
	o.BinaryOnly = false
	o.Harnesses = []string{"codex"}
	r = Run(o, "install")
	requireOK(t, r)
	b, _ := os.ReadFile(filepath.Join(brew, binaryName()))
	if string(b) != "package-owned" || !r.Shadowed {
		t.Fatal("package changed or shadowing missed")
	}
}

func TestUpgradePayloadAndRepairMissingResource(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	r := Run(o, "install")
	requireOK(t, r)
	op := r.Operations[0]
	// Represent a previous release whose unchanged owned content differs.
	old := []byte("previous skill release")
	os.WriteFile(filepath.Join(op.Destination, "SKILL.md"), old, 0644)
	m := op.Manifest
	m.Version = "0.0.1"
	m.Files["SKILL.md"] = digest(old)
	hashes, _ := json.Marshal(m.Files)
	m.Digest = digest(hashes)
	data, _ := json.Marshal(m)
	os.WriteFile(op.manifestPath, data, 0644)
	os.Remove(filepath.Join(op.Destination, "references", "testing.md"))
	status := Run(o, "status")
	if status.Exit != 3 {
		t.Fatal("missing required resource accepted")
	}
	upgraded := Run(o, "install")
	requireOK(t, upgraded)
	got, _ := os.ReadFile(filepath.Join(op.Destination, "SKILL.md"))
	if string(got) == string(old) || !upgraded.Operations[0].Verified {
		t.Fatal("old payload not upgraded")
	}
}

func TestPermissionFailureIsReadOnly(t *testing.T) {
	environment(t)
	parent := t.TempDir()
	o := opts()
	o.Harnesses = []string{"codex"}
	o.Destination = filepath.Join(parent, "denmother")
	if e := os.Chmod(parent, 0500); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Chmod(parent, 0700) })
	r := Run(o, "install")
	if r.Exit != 1 {
		t.Skip("host user can override directory permissions")
	}
	if r.ErrorCode != "permission_failure" {
		t.Fatal(r)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("permission failure left writes")
	}
}

func TestSharedDiscoveryPathsAreDeduplicated(t *testing.T) {
	home := environment(t)
	o := opts()
	shared := filepath.Join(home, "shared", "skills")
	if e := os.MkdirAll(shared, 0755); e != nil {
		t.Fatal(e)
	}
	for _, parent := range []string{filepath.Join(home, ".agents"), os.Getenv("CLAUDE_CONFIG_DIR")} {
		if e := os.MkdirAll(parent, 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(shared, filepath.Join(parent, "skills")); e != nil {
			t.Skip(e)
		}
	}
	r := Run(o, "install")
	requireOK(t, r)
	if len(r.Operations) != 1 || len(r.Harnesses) != 2 {
		t.Fatal("shared path registered twice", r)
	}
	requireOK(t, Run(o, "status"))
}

func TestReinstallAfterPreservedUninstallRestoresResources(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	first := Run(o, "install")
	requireOK(t, first)
	path := filepath.Join(first.Operations[0].Destination, "SKILL.md")
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, []byte("user edit"), 0644)
	removed := Run(o, "uninstall")
	requireOK(t, removed)
	// The user explicitly restores the preserved file after saving their edits.
	os.WriteFile(path, original, 0644)
	if r := Run(o, "status"); r.Exit != 3 {
		t.Fatal("partial uninstall reported compatible", r)
	}
	repaired := Run(o, "install")
	requireOK(t, repaired)
	if repaired.Operations[0].State != "applied" || !repaired.Operations[0].Verified {
		t.Fatal("missing resources skipped", repaired)
	}
	for p := range first.Operations[0].Manifest.Files {
		if _, e := os.Stat(filepath.Join(first.Operations[0].Destination, p)); e != nil {
			t.Fatal(e)
		}
	}
}

func TestUninstallPreservesReplacementDirectorySymlink(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	r := Run(o, "install")
	requireOK(t, r)
	references := filepath.Join(r.Operations[0].Destination, "references")
	saved := filepath.Join(t.TempDir(), "saved references")
	if e := os.Rename(references, saved); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(saved, references); e != nil {
		t.Skip(e)
	}
	removed := Run(o, "uninstall")
	requireOK(t, removed)
	if info, e := os.Lstat(references); e != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("uninstall deleted user's replacement symlink")
	}
	if _, e := os.Stat(filepath.Join(saved, "testing.md")); e != nil {
		t.Fatal("uninstall touched symlink target", e)
	}
}
