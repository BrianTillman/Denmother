package install

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BrianTillman/Denmother/skills"
	"gopkg.in/yaml.v3"
)

const manifestName = ".denmother-install.json"

type Manifest struct {
	Format       int               `json:"format"`
	ID           string            `json:"installation_id"`
	Version      string            `json:"denmother_version"`
	SkillVersion string            `json:"skill_version,omitempty"`
	Digest       string            `json:"content_digest"`
	Contract     string            `json:"result_contract"`
	MinimumCLI   string            `json:"minimum_cli,omitempty"`
	Origin       string            `json:"origin"`
	Revision     string            `json:"revision"`
	Owner        string            `json:"owner"`
	Harness      string            `json:"harness,omitempty"`
	Scope        string            `json:"scope"`
	Destination  string            `json:"destination"`
	Files        map[string]string `json:"files"`
	Previous     *Previous         `json:"previous,omitempty"`
}
type Previous struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type RecoveryReport struct {
	State          string   `json:"state"`
	RestoredFiles  []string `json:"restored_files"`
	SkippedFiles   []string `json:"skipped_files"`
	RemainingFiles []string `json:"remaining_files"`
}
type Operation struct {
	Recovery       *RecoveryReport `json:"recovery,omitempty"`
	oldBytes       []byte
	AppliedFiles   []string  `json:"applied_files"`
	RemainingFiles []string  `json:"remaining_files"`
	Kind           string    `json:"kind"`
	Destination    string    `json:"destination"`
	Action         string    `json:"action"`
	State          string    `json:"state"`
	Manifest       *Manifest `json:"manifest,omitempty"`
	Missing        []string  `json:"missing"`
	Modified       []string  `json:"modified"`
	Conflicts      []string  `json:"conflicts"`
	Leftovers      []string  `json:"leftovers"`
	Verified       bool      `json:"registration_verified"`
	Compatible     bool      `json:"compatible"`
	files          map[string][]byte
	old            *Manifest
	manifestPath   string
}
type Report struct {
	Discovery      []Harness    `json:"discovery"`
	PlanID         string       `json:"plan_id"`
	Version        string       `json:"resolved_version"`
	Origin         string       `json:"origin"`
	Revision       string       `json:"revision"`
	Scope          string       `json:"scope"`
	Project        string       `json:"project,omitempty"`
	DryRun         bool         `json:"dry_run"`
	Harnesses      []Harness    `json:"harnesses"`
	Operations     []*Operation `json:"operations"`
	Findings       []string     `json:"findings"`
	Binary         string       `json:"binary"`
	PATHBinary     string       `json:"path_binary"`
	BinaryOwner    string       `json:"binary_owner"`
	Reachable      bool         `json:"binary_reachable"`
	Shadowed       bool         `json:"path_shadowed"`
	HarnessInvoked bool         `json:"harness_invoked"`
	ErrorCode      string       `json:"error_code,omitempty"`
	Exit           int          `json:"-"`
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func identifier() string        { b := make([]byte, 16); _, _ = rand.Read(b); return fmt.Sprintf("%x", b) }
func payload() (map[string][]byte, map[string]string, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(skills.Files, "denmother", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := skills.Files.ReadFile(path)
		if e == nil {
			files[strings.TrimPrefix(path, "denmother/")] = b
		}
		return e
	})
	if err != nil {
		return nil, nil, err
	}
	var header struct {
		Metadata map[string]string `yaml:"metadata"`
	}
	parts := strings.SplitN(string(files["SKILL.md"]), "---", 3)
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("invalid embedded skill")
	}
	err = yaml.Unmarshal([]byte(parts[1]), &header)
	return files, header.Metadata, err
}
func newManifest(o Options, h, dest string, files map[string][]byte, meta map[string]string) *Manifest {
	hashes := map[string]string{}
	for p, b := range files {
		hashes[p] = digest(b)
	}
	data, _ := json.Marshal(hashes)
	return &Manifest{Format: 1, ID: identifier(), Version: o.Version, SkillVersion: meta["version"], Contract: "dm.operator.v1", MinimumCLI: meta["denmother-min-version"], Digest: digest(data), Origin: o.Origin, Revision: o.Revision, Owner: "denmother", Harness: h, Scope: o.Scope, Destination: dest, Files: hashes}
}
func safeFile(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func readRegular(path string) ([]byte, error) { return readRegularLimit(path, maxManagedFileSize) }
func readRegularLimit(path string, limit int64) ([]byte, error) {
	if err := safeFile(path); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !current.Mode().IsRegular() || !os.SameFile(before, current) {
		return nil, fmt.Errorf("file changed while opening: %s", path)
	}
	if current.Size() > limit {
		return nil, fmt.Errorf("file exceeds size limit: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds size limit: %s", path)
	}
	return data, nil
}
func loadManifest(path, dest string) (*Manifest, []byte, error) {
	data, err := readRegularLimit(path, maxManifestSize)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	kind := "skill"
	if filepath.Base(path) == ".dm.denmother.json" {
		kind = "binary"
	}
	m, err := decodeManifest(data, dest, kind)
	return m, data, err
}
func readManifest(path, dest string) (*Manifest, error) {
	m, _, err := loadManifest(path, dest)
	return m, err
}
func inspect(op *Operation, action string) error {
	op.Missing = []string{}
	op.Modified = []string{}
	op.Conflicts = []string{}
	op.Leftovers = []string{}
	op.State = "planned"
	if e := safeFile(op.Destination); e != nil {
		return e
	}
	if _, e := os.Lstat(op.manifestPath + ".transaction"); e == nil {
		return interrupted("interrupted installation at %s; retry with --recover after checking no installer is running", op.manifestPath+".transaction")
	} else if !os.IsNotExist(e) {
		return e
	}
	old, oldBytes, e := loadManifest(op.manifestPath, op.Destination)
	if e != nil {
		return e
	}
	op.old = old
	op.oldBytes = oldBytes
	if old != nil {
		if op.Kind == "binary" && (len(old.Files) != 1 || old.Files[binaryName()] == "") {
			return fmt.Errorf("invalid binary ownership manifest")
		}
		for _, p := range sortedKeys(old.Files) {
			b, e := readRegular(filepath.Join(op.Destination, filepath.FromSlash(p)))
			if os.IsNotExist(e) {
				op.Missing = append(op.Missing, p)
			} else if e != nil || digest(b) != old.Files[p] {
				op.Modified = append(op.Modified, p)
			}
		}
		if action == "install" {
			op.Manifest.ID = old.ID
			op.Manifest.Previous = &Previous{old.Version, old.Digest}
		}
	}
	if action == "status" {
		if old != nil {
			for p := range op.files {
				if _, ok := old.Files[p]; !ok {
					op.Missing = append(op.Missing, p)
				}
			}
		}
		op.Manifest = old
		op.Action = "inspect"
		op.State = "skipped"
		op.Verified = old != nil && len(op.Missing) == 0 && len(op.Modified) == 0
		return nil
	}
	if action == "uninstall" {
		op.Action = "remove"
		if old == nil {
			op.State = "skipped"
			op.Action = "none"
			if _, err := os.Lstat(op.Destination); err == nil {
				op.Leftovers = append(op.Leftovers, op.Destination+": not owned by Denmother")
			}
		}
		return nil
	}
	if old == nil && op.Kind == "skill" {
		if _, e := os.Lstat(op.Destination); e == nil {
			op.Conflicts = append(op.Conflicts, op.Destination+": foreign registration; move it aside or choose --destination PATH")
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	for _, p := range sortedKeys(op.files) {
		b, e := readRegular(filepath.Join(op.Destination, filepath.FromSlash(p)))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || old == nil || old.Files[p] == "" {
			op.Conflicts = append(op.Conflicts, filepath.Join(op.Destination, p)+": not an unchanged owned file; move it aside or select a different destination")
			continue
		}
		_ = b
	}
	for _, p := range op.Modified {
		op.Conflicts = append(op.Conflicts, filepath.Join(op.Destination, p)+": locally modified; back up and restore the original before upgrading, or use --destination PATH")
	}
	op.Action = "install"
	if old != nil {
		op.Action = "upgrade"
		if old.Digest == op.Manifest.Digest && maps.Equal(old.Files, op.Manifest.Files) && old.Version == op.Manifest.Version && len(op.Missing) == 0 && len(op.Modified) == 0 && len(op.Conflicts) == 0 {
			op.Action = "none"
			op.State = "skipped"
			op.Manifest = old
			op.Verified = true
		}
	}
	return nil
}
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "dm.exe"
	}
	return "dm"
}
func Owner(path string) string {
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		real = path
	}
	p := filepath.ToSlash(real)
	if strings.Contains(p, "/Cellar/") {
		return "homebrew"
	}
	if strings.HasPrefix(p, "/nix/store/") {
		return "nix"
	}
	return "external"
}

type installationError struct {
	Code string
	Exit int
	Err  error
}

func (e *installationError) Error() string { return e.Err.Error() }
func (e *installationError) Unwrap() error { return e.Err }
func interrupted(format string, args ...any) error {
	return &installationError{Code: "incomplete_verification", Exit: 3, Err: fmt.Errorf(format, args...)}
}

func fail(r *Report, e error) *Report {
	r.Exit = 1
	r.ErrorCode = "installation_failed"
	if errors.Is(e, os.ErrPermission) {
		r.ErrorCode = "permission_failure"
	}
	var classified *installationError
	if errors.As(e, &classified) {
		r.ErrorCode = classified.Code
		r.Exit = classified.Exit
	}
	r.Findings = append(r.Findings, e.Error())
	return r
}

func Run(o Options, action string) *Report { return RunContext(context.Background(), o, action) }

func RunContext(ctx context.Context, o Options, action string) *Report {
	r := &Report{PlanID: identifier(), Version: o.Version, Origin: o.Origin, Revision: o.Revision, Scope: o.Scope, Project: o.Project, DryRun: o.DryRun, Operations: []*Operation{}, Findings: []string{}}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fail(r, interrupted("installation canceled: %w", err))
	}
	if action != "install" && action != "status" && action != "uninstall" {
		r = fail(r, fmt.Errorf("invalid installer action"))
		r.ErrorCode = "invalid_arguments"
		return r
	}
	if o.BinaryOnly && (!o.Binary || o.Destination != "" || len(o.Harnesses) > 0 || o.Project != "" || o.Scope != "user") {
		r = fail(r, fmt.Errorf("--binary-only cannot select a skill harness, scope, project or destination"))
		r.ErrorCode = "invalid_arguments"
		return r
	}
	var hs []Harness
	if !o.BinaryOnly {
		var err error
		hs, err = Discover(o)
		if err != nil {
			r = fail(r, err)
			r.ErrorCode = "invalid_arguments"
			return r
		}
		r.Harnesses = hs
		discoveryOptions := o
		discoveryOptions.Harnesses = []string{"codex", "claude"}
		discoveryOptions.Destination = ""
		if evidence, err := Discover(discoveryOptions); err == nil {
			r.Discovery = evidence
		}
	}
	exe, e := os.Executable()
	if e != nil {
		return fail(r, e)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	r.Binary = exe
	r.BinaryOwner = Owner(exe)
	if p, e := exec.LookPath(binaryName()); e == nil {
		r.PATHBinary, _ = filepath.Abs(p)
	}
	if o.Binary {
		if r.BinaryOwner == "homebrew" || r.BinaryOwner == "nix" {
			r.Findings = append(r.Findings, "Package-managed binary preserved; use dm skills install and upgrade with "+r.BinaryOwner+" (Homebrew: brew upgrade denmother)")
			o.Binary = false
		} else if r.PATHBinary != "" && (Owner(r.PATHBinary) == "homebrew" || Owner(r.PATHBinary) == "nix") {
			r.ErrorCode = "destination_conflict"
			r.Exit = 1
			r.Findings = append(r.Findings, "Package-managed dm on PATH: "+r.PATHBinary+"; run that binary's skills install and use its package manager to upgrade")
			return r
		}
	}
	files, meta, e := payload()
	if e != nil {
		return fail(r, e)
	}
	if o.Binary {
		dir := o.BinDir
		if dir == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return fail(r, e)
			}
			dir = filepath.Join(home, ".local", "bin")
		}
		dir, e = Resolve(dir)
		if e != nil {
			return fail(r, e)
		}
		b, e := readRegular(exe)
		if e != nil {
			return fail(r, e)
		}
		f := map[string][]byte{binaryName(): b}
		op := &Operation{Kind: "binary", Destination: dir, files: f, Manifest: newManifest(o, "", dir, f, nil), manifestPath: filepath.Join(dir, ".dm.denmother.json")}
		r.Operations = append(r.Operations, op)
		r.Binary = filepath.Join(dir, binaryName())
		r.BinaryOwner = "denmother"
	}
	seen := map[string]bool{}
	for _, h := range hs {
		if seen[h.Destination] {
			continue
		}
		seen[h.Destination] = true
		op := &Operation{Kind: "skill", Destination: h.Destination, files: files, Manifest: newManifest(o, h.Name, h.Destination, files, meta), manifestPath: filepath.Join(h.Destination, manifestName)}
		r.Operations = append(r.Operations, op)
	}
	for i, a := range r.Operations {
		for _, b := range r.Operations[i+1:] {
			for _, pair := range [][2]string{{a.Destination, b.Destination}, {b.Destination, a.Destination}} {
				rel, err := filepath.Rel(pair[0], pair[1])
				if err == nil && (rel == "." || filepath.IsLocal(rel)) {
					r = fail(r, fmt.Errorf("installation destinations overlap: %s and %s; choose separate directories", a.Destination, b.Destination))
					r.ErrorCode = "destination_conflict"
					return r
				}
			}
		}
	}
	// Lock every destination in sorted order, then re-inspect while holding locks.
	// Read-only operations never create locks or directories.
	var unlocks []func()
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}()
	if action != "status" && !o.DryRun {
		locks := map[string]bool{}
		for _, op := range r.Operations {
			locks[op.Destination] = true
		}
		for _, dest := range sortedKeys(locks) {
			unlock, e := lock(dest)
			if e != nil {
				return fail(r, e)
			}
			unlocks = append(unlocks, unlock)
		}
	}
	for _, op := range r.Operations {
		if err := ctx.Err(); err != nil {
			return fail(r, interrupted("installation canceled: %w", err))
		}
		if o.Recover && !o.DryRun && action != "status" {
			if e := recoverTransactionContext(ctx, op); e != nil {
				return fail(r, e)
			}
		}
		if e := inspect(op, action); e != nil {
			return fail(r, e)
		}
		expected := newManifest(o, "", op.Destination, op.files, meta)
		op.Compatible = op.Manifest != nil && op.Manifest.Digest == expected.Digest && maps.Equal(op.Manifest.Files, expected.Files) && op.Manifest.Contract == "dm.operator.v1"
		if len(op.Conflicts) > 0 {
			r.Exit = 1
			r.ErrorCode = "destination_conflict"
		}
	}
	if r.Exit != 0 {
		return r
	}
	for _, op := range r.Operations {
		if err := ctx.Err(); err != nil {
			return fail(r, interrupted("installation canceled: %w", err))
		}
		if action == "status" {
			if !op.Verified || !op.Compatible {
				r.Exit = 3
				r.ErrorCode = "incomplete_verification"
			}
			continue
		}
		if o.DryRun || op.State == "skipped" {
			if action == "uninstall" && len(op.Leftovers) > 0 && r.Exit == 0 {
				r.Exit = 2
			}
			continue
		}
		if e := applyContext(ctx, op, action); e != nil {
			return fail(r, e)
		}
		if action == "uninstall" {
			if len(op.Leftovers) > 0 {
				r.Exit = 2
			}
			continue
		}
		verified := &Operation{Kind: op.Kind, Destination: op.Destination, manifestPath: op.manifestPath, Manifest: op.Manifest, files: op.files}
		if e := inspect(verified, "status"); e != nil {
			return fail(r, e)
		}
		op.Verified = verified.Verified
		op.Compatible = op.Verified
		if !op.Verified {
			r.Exit = 3
			r.ErrorCode = "incomplete_verification"
		}
	}
	if p, e := exec.LookPath(binaryName()); e == nil {
		r.PATHBinary, _ = filepath.Abs(p)
	}
	r.Reachable = false
	if r.PATHBinary != "" {
		a, _ := filepath.EvalSymlinks(r.PATHBinary)
		b, _ := filepath.EvalSymlinks(r.Binary)
		r.Reachable = a != "" && a == b
		r.Shadowed = !r.Reachable
	}
	// A planned binary can be reachable after creation even though LookPath failed.
	if o.DryRun && o.Binary {
		for _, p := range filepath.SplitList(os.Getenv("PATH")) {
			a, _ := filepath.Abs(p)
			if a == filepath.Dir(r.Binary) && r.PATHBinary == "" {
				r.Findings = append(r.Findings, "Binary destination is on PATH; executable verification deferred until installation")
			}
		}
	}
	if len(hs) == 0 && !o.BinaryOnly {
		r.Findings = append(r.Findings, "No supported harness detected; register explicitly with dm skills install --harness codex or --harness claude")
		if r.Exit == 0 {
			r.Exit = 2
		}
	}
	if !r.Reachable {
		r.Findings = append(r.Findings, "PATH does not resolve to "+r.Binary+"; currently resolves to "+r.PATHBinary+". Add the binary directory to PATH explicitly; shell configuration was not edited.")
		if r.Exit == 0 {
			r.Exit = 2
		}
	}
	if o.Recover && (action == "status" || o.DryRun) {
		r.Findings = append(r.Findings, "Recovery is deferred in read-only mode")
	}
	return r
}
