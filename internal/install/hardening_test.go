package install

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func encodeFixture(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func upgradeJournal(t *testing.T, op *Operation, path string, after []byte) transaction {
	t.Helper()
	before := readFixture(t, filepath.Join(op.Destination, path))
	oldBytes := readFixture(t, op.manifestPath)
	next := *op.Manifest
	next.Files = maps.Clone(next.Files)
	next.Files[path] = digest(after)
	next.Digest = digest(encodeFixture(t, next.Files))
	return transaction{Format: 1, Destination: op.Destination, Changes: []change{{Path: path, Before: before, After: after, Mode: 0644}, {Path: filepath.Base(op.manifestPath), Before: oldBytes, After: encodeFixture(t, next), Mode: 0644}}}
}
func installedFixture(t *testing.T) (Options, *Operation) {
	t.Helper()
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	r := Run(o, "install")
	requireOK(t, r)
	return o, r.Operations[0]
}
func directorySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, p)
		entry := info.Mode().String()
		if info.Mode().IsRegular() {
			entry += " " + digest(readFixture(t, p))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			entry += " " + target
		}
		result[relative] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestManifestRejectsUnsafeOrAmbiguousMetadata(t *testing.T) {
	cases := map[string]func(*Manifest) []byte{
		"bad digest": func(m *Manifest) []byte { m.Digest = strings.Repeat("f", 64); return encodeFixture(t, m) },
		"bad file digest": func(m *Manifest) []byte {
			m.Files["SKILL.md"] = strings.Repeat("z", 64)
			m.Digest = digest(encodeFixture(t, m.Files))
			return encodeFixture(t, m)
		},
		"duplicate field": func(m *Manifest) []byte { return append([]byte(`{"owner":"foreign",`), encodeFixture(t, m)[1:]...) },
		"case alias":      func(m *Manifest) []byte { return append([]byte(`{"OWNER":"foreign",`), encodeFixture(t, m)[1:]...) },
		"trailing JSON":   func(m *Manifest) []byte { return append(encodeFixture(t, m), []byte(` {}`)...) },
		"unknown field":   func(m *Manifest) []byte { return append([]byte(`{"unreviewed":true,`), encodeFixture(t, m)[1:]...) },
	}
	for _, path := range []string{".", "../outside", "C:/outside", "notes:stream", "CON.txt", "references/NUL", "references/trailing.", "references/ends ", ".denmother-install.json.transaction", "references/.denmother-stage-owned", ".dm.denmother.json", "skill.md", "references"} {
		cases[path] = func(m *Manifest) []byte {
			m.Files[path] = digest([]byte("fixture"))
			m.Digest = digest(encodeFixture(t, m.Files))
			return encodeFixture(t, m)
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o, op := installedFixture(t)
			writeFixture(t, op.manifestPath, mutate(op.Manifest))
			before := directorySnapshot(t, op.Destination)
			r := Run(o, "uninstall")
			if r.Exit != 1 {
				t.Fatalf("unsafe manifest accepted: %+v", r)
			}
			if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
				t.Fatal("invalid manifest changed files")
			}
		})
	}
}
func TestRecoveryRejectsUnownedPathsAndDuplicateChanges(t *testing.T) {
	for _, invalid := range []string{"notes.txt", "../outside", "C:/outside", "SKILL.md:stream", ".", "duplicate", "wrong digest", "no manifest"} {
		t.Run(invalid, func(t *testing.T) {
			o, op := installedFixture(t)
			tx := upgradeJournal(t, op, "SKILL.md", []byte("next"))
			switch invalid {
			case "duplicate":
				tx.Changes = append([]change{tx.Changes[0]}, tx.Changes...)
			case "wrong digest":
				tx.Changes[0].Before = []byte("not owned")
			case "no manifest":
				tx.Changes = tx.Changes[:1]
			default:
				tx.Changes[0].Path = invalid
			}
			writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
			before := directorySnapshot(t, op.Destination)
			o.Recover = true
			r := Run(o, "install")
			if r.Exit != 1 {
				t.Fatalf("invalid recovery accepted: %+v", r)
			}
			if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
				t.Fatal("invalid recovery changed files")
			}
		})
	}
}
func TestRecoveryDoesNotConfuseMissingAndEmptyFiles(t *testing.T) {
	o, op := installedFixture(t)
	path := filepath.Join(op.Destination, "SKILL.md")
	original := readFixture(t, path)
	tx := transaction{Format: 1, Destination: op.Destination, Changes: []change{{Path: "SKILL.md", Before: original, After: nil, Mode: 0644}, {Path: manifestName, Before: readFixture(t, op.manifestPath), After: nil, Mode: 0644}}}
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	writeFixture(t, path, []byte{})
	before := directorySnapshot(t, op.Destination)
	o.Recover = true
	if r := Run(o, "install"); r.Exit != 1 {
		t.Fatal("empty user file treated as absent", r)
	}
	if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
		t.Fatal("recovery overwrote empty user file")
	}
}
func TestInterruptedFirstInstallationCanBeRecovered(t *testing.T) {
	environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	o.DryRun = true
	plan := Run(o, "install")
	requireOK(t, plan)
	op := plan.Operations[0]
	tx := transaction{Format: 1, Destination: op.Destination}
	for _, p := range sortedKeys(op.files) {
		tx.Changes = append(tx.Changes, change{Path: p, After: op.files[p], Mode: 0644})
	}
	tx.Changes = append(tx.Changes, change{Path: manifestName, After: encodeFixture(t, op.Manifest), Mode: 0644})
	for _, c := range tx.Changes[:2] {
		writeFixture(t, filepath.Join(op.Destination, c.Path), c.After)
	}
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	o.DryRun = false
	o.Recover = true
	r := Run(o, "install")
	requireOK(t, r)
	if !r.Operations[0].Verified || r.Operations[0].Recovery == nil || r.Operations[0].Recovery.State != "restored" {
		t.Fatalf("first install not recovered: %+v", r)
	}
}
func TestRecoveryReportsRollbackEvenWhenLaterPlanConflicts(t *testing.T) {
	o, op := installedFixture(t)
	tx := upgradeJournal(t, op, "SKILL.md", []byte("next"))
	writeFixture(t, filepath.Join(op.Destination, "SKILL.md"), tx.Changes[0].After)
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	o.Harnesses = []string{"codex", "claude"}
	hs, err := Discover(o)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(hs[1].Destination, "foreign.txt"), []byte("mine"))
	o.Recover = true
	r := Run(o, "install")
	if r.Exit != 1 {
		t.Fatal("foreign registration should block plan")
	}
	recovery := r.Operations[0].Recovery
	if recovery == nil || recovery.State != "restored" || len(recovery.RestoredFiles)+len(recovery.SkippedFiles) != 2 || len(recovery.RemainingFiles) != 0 {
		t.Fatalf("rollback hidden by conflict: %+v", recovery)
	}
}
func TestApplyRejectsManifestEditedAfterPlanning(t *testing.T) {
	o, op := installedFixture(t)
	o.Version = "next"
	op.Manifest = newManifest(o, "codex", op.Destination, op.files, map[string]string{})
	if err := inspect(op, "install"); err != nil {
		t.Fatal(err)
	}
	edited := append(readFixture(t, op.manifestPath), ' ')
	writeFixture(t, op.manifestPath, edited)
	before := directorySnapshot(t, op.Destination)
	if err := apply(op, "install"); err == nil {
		t.Fatal("edited ownership accepted")
	}
	if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
		t.Fatal("edited manifest overwritten")
	}
}
func TestRecoveryRetainsOriginalFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not represented on Windows")
	}
	_, op := installedFixture(t)
	tx := upgradeJournal(t, op, "SKILL.md", []byte("next"))
	mode := uint32(0600)
	tx.Changes[0].BeforeMode = &mode
	writeFixture(t, filepath.Join(op.Destination, "SKILL.md"), tx.Changes[0].After)
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	if err := recoverTransaction(op); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(op.Destination, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("rollback permissions=%o", info.Mode().Perm())
	}
}
func TestManagedPathAllowsSpacesAndRejectsAliases(t *testing.T) {
	for _, p := range []string{"references/with spaces.md", "SKILL.md"} {
		if !managedPath(p) {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"CON.md", "COM1", "LPT9.log", "nul", "references/file:stream", "."} {
		if managedPath(p) {
			t.Fatalf("unsafe path %q", p)
		}
	}
}

func TestBrokenUnselectedHarnessDoesNotBlockInstallation(t *testing.T) {
	home := environment(t)
	loop := filepath.Join(home, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skip(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", loop)
	o := opts()
	o.Harnesses = []string{"codex"}
	requireOK(t, Run(o, "install"))
	o.Binary = true
	o.BinaryOnly = true
	o.Harnesses = nil
	o.BinDir = filepath.Join(home, "bin")
	requireOK(t, Run(o, "install"))
}
func TestOverlappingBinaryAndSkillDestinationsAreBlocked(t *testing.T) {
	home := environment(t)
	o := opts()
	o.Harnesses = []string{"codex"}
	o.Binary = true
	o.BinDir = filepath.Join(home, "bin")
	for _, dest := range []string{o.BinDir, filepath.Join(o.BinDir, "skill"), home} {
		o.Destination = dest
		before := directorySnapshot(t, home)
		r := Run(o, "install")
		if r.Exit != 1 || r.ErrorCode != "destination_conflict" {
			t.Fatal("overlap accepted", r)
		}
		if !reflect.DeepEqual(before, directorySnapshot(t, home)) {
			t.Fatal("overlap failure wrote files")
		}
	}
}
func TestCancellationBeforeInstallOrRecoveryDoesNotWrite(t *testing.T) {
	home := environment(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := opts()
	before := directorySnapshot(t, home)
	r := RunContext(ctx, o, "install")
	if r.Exit != 3 {
		t.Fatal("cancellation ignored", r)
	}
	if !reflect.DeepEqual(before, directorySnapshot(t, home)) {
		t.Fatal("canceled plan mutated home")
	}
	_, op := installedFixture(t)
	tx := upgradeJournal(t, op, "SKILL.md", []byte("next"))
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	before = directorySnapshot(t, op.Destination)
	if err := recoverTransactionContext(ctx, op); err == nil {
		t.Fatal("canceled recovery accepted")
	}
	if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
		t.Fatal("canceled recovery wrote files")
	}
}

func TestBinaryRecoveryCannotRestoreUnrelatedFile(t *testing.T) {
	home := environment(t)
	o := opts()
	o.Binary = true
	o.BinaryOnly = true
	o.Harnesses = nil
	o.BinDir = filepath.Join(home, "bin")
	r := Run(o, "install")
	requireOK(t, r)
	op := r.Operations[0]
	manifest := readFixture(t, op.manifestPath)
	foreign := filepath.Join(op.Destination, "other-tool")
	writeFixture(t, foreign, []byte("user tool"))
	tx := transaction{Format: 1, Destination: op.Destination, Changes: []change{{Path: "other-tool", Before: []byte("replacement"), After: []byte("user tool"), Mode: 0644}, {Path: filepath.Base(op.manifestPath), Before: manifest, After: manifest, Mode: 0644}}}
	writeFixture(t, op.manifestPath+".transaction", encodeFixture(t, tx))
	before := directorySnapshot(t, op.Destination)
	o.Recover = true
	if result := Run(o, "install"); result.Exit != 1 {
		t.Fatal("binary journal acquired unrelated file", result)
	}
	if !reflect.DeepEqual(before, directorySnapshot(t, op.Destination)) {
		t.Fatal("unrelated binary changed")
	}
}
func TestMetadataSizeLimitsFailBeforeMutation(t *testing.T) {
	for _, journal := range []bool{false, true} {
		t.Run(fmt.Sprint(journal), func(t *testing.T) {
			o, op := installedFixture(t)
			path := op.manifestPath
			limit := int64(maxManifestSize)
			if journal {
				path += ".transaction"
				limit = maxJournalSize
				o.Recover = true
			}
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(limit + 1); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			payload := readFixture(t, filepath.Join(op.Destination, "SKILL.md"))
			r := Run(o, "install")
			if r.Exit != 1 {
				t.Fatal("oversized metadata accepted", r)
			}
			if string(readFixture(t, filepath.Join(op.Destination, "SKILL.md"))) != string(payload) {
				t.Fatal("oversized metadata changed payload")
			}
		})
	}
}
func TestFailureClassificationDoesNotDependOnPathText(t *testing.T) {
	r := fail(&Report{}, fmt.Errorf("invalid file /tmp/interrupted/owned"))
	if r.Exit != 1 {
		t.Fatal("path text changed exit classification")
	}
}
func FuzzMetadataDecode(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"Files":{"SKILL.md":"x","skill.md":"y"}}`, `{"Format":1,"format":2}`, `{"Changes":[{}]}`, strings.Repeat("[", 40) + strings.Repeat("]", 40)} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip()
		}
		var tx transaction
		_ = decodeMetadata(data, &tx)
	})
}
func FuzzManagedPath(f *testing.F) {
	for _, seed := range []string{"SKILL.md", "../outside", "C:/outside", "COM¹.md", "references/a b.md", "."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !managedPath(name) {
			return
		}
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			t.Fatalf("accepted nonlocal path %q", name)
		}
		root := filepath.Join(string(filepath.Separator), "fixture")
		rel, err := filepath.Rel(root, filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			t.Fatalf("path escaped destination: %q", name)
		}
	})
}

func TestHomebrewSourceDirectoryDoesNotImplyPackageOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homebrew", "bin", binaryName())
	if owner := Owner(path); owner != "external" {
		t.Fatalf("source directory mistaken for installed package: %s", owner)
	}
}

func TestPermissionFailureDuringUpgradeLeavesRecoverableProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX directory permissions")
	}
	o, op := installedFixture(t)
	original := directorySnapshot(t, op.Destination)
	files, meta, err := payload()
	if err != nil {
		t.Fatal(err)
	}
	files["SKILL.md"] = append(files["SKILL.md"], []byte("\nnew skill revision\n")...)
	files["references/testing.md"] = append(files["references/testing.md"], []byte("\nnew reference revision\n")...)
	o.Version = "next"
	op.files = files
	op.Manifest = newManifest(o, "codex", op.Destination, files, meta)
	if err := inspect(op, "install"); err != nil {
		t.Fatal(err)
	}
	references := filepath.Join(op.Destination, "references")
	if err := os.Chmod(references, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(references, 0755) })
	probe := filepath.Join(references, "probe")
	if err := os.WriteFile(probe, nil, 0600); err == nil {
		_ = os.Remove(probe)
		t.Skip("host can override directory permissions")
	}
	err = apply(op, "install")
	if err == nil {
		t.Fatal("upgrade ignored unwritable reference directory")
	}
	if fail(&Report{}, err).Exit != 3 || len(op.AppliedFiles) != 1 || len(op.RemainingFiles) != 2 {
		t.Fatalf("partial progress missing: %+v: %v", op, err)
	}
	if filepath.Base(op.AppliedFiles[0]) != "SKILL.md" {
		t.Fatal(op.AppliedFiles)
	}
	if err := recoverTransaction(op); err != nil {
		t.Fatalf("could not recover unchanged read-only references: %v", err)
	}
	if err := os.Chmod(references, 0755); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, directorySnapshot(t, op.Destination)) {
		t.Fatal("rollback failed to preserve previous usable installation")
	}
	if len(op.Recovery.RestoredFiles) != 1 || len(op.Recovery.SkippedFiles) != 2 {
		t.Fatalf("rollback reporting inaccurate: %+v", op.Recovery)
	}
}
