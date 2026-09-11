package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/operator"
	releaseassets "github.com/BrianTillman/Denmother/scripts"
	"github.com/spf13/cobra"
)

var releaseJSON bool
var releaseVersion string
var releaseOutput string
var releaseSource string
var releaseHomebrewRepository string

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`)

func init() {
	command := &cobra.Command{Use: "release", Short: "Check and package a standalone source tree"}
	check := &cobra.Command{Use: "check", Short: "Run Go/Python tests, race checks, vet, and source hygiene", Args: cobra.NoArgs, RunE: runReleaseCheck}
	build := &cobra.Command{Use: "build", Short: "Create stamped binaries and checksum archives locally", Args: cobra.NoArgs, RunE: runReleaseBuild}
	command.PersistentFlags().StringVar(&releaseSource, "source", ".", "Denmother module source directory")
	command.PersistentFlags().BoolVar(&releaseJSON, "json", false, "Emit a JSON result")
	build.Flags().StringVar(&releaseVersion, "version", "", "Release version (for example 0.1.0-rc.1)")
	build.Flags().StringVar(&releaseOutput, "output", "dist", "Output directory (relative to source)")
	build.Flags().StringVar(&releaseHomebrewRepository, "homebrew-repository", "", "Also generate a Homebrew formula for GitHub OWNER/REPO releases tagged v<VERSION>")
	command.AddCommand(check, build)
	rootCmd.AddCommand(command)
}

func releaseRoot() (string, error) {
	root, err := filepath.Abs(releaseSource)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("--source must point to the Denmother Go module: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "module github.com/BrianTillman/Denmother\n") {
		return "", fmt.Errorf("--source is not the Denmother module")
	}
	return root, nil
}

func runReleaseCheck(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("release", releaseJSON, cmd.OutOrStdout())
	rt.SetProfile("check")
	root, err := releaseRoot()
	if err != nil {
		return err
	}
	if err := checkReleaseSource(root); err != nil {
		rt.AddStep(operator.Step{ID: "source", Title: "Inspect public source", Status: operator.StatusFailure, Summary: err.Error()})
		return rt.Complete(operator.StatusFailure, "release source check failed")
	}
	rt.AddStep(operator.Step{ID: "source", Title: "Inspect public source", Status: operator.StatusSuccess, Summary: "required release files present; known private inventory and environment files absent"})
	python := "python3"
	if _, err := exec.LookPath(python); err != nil {
		python = "python"
	}
	for _, check := range []struct {
		id, program string
		args        []string
	}{
		{"test", "go", []string{"test", "./..."}},
		{"race", "go", []string{"test", "-race", "./..."}},
		{"vet", "go", []string{"vet", "./..."}},
		{"acceptance-runner", python, []string{"-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "acceptance_test.py"}},
		{"bootstrap", python, []string{"-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "bootstrap_test.py"}},
		{"public-source", python, []string{"-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "public_source_test.py"}},
		{"precommit", python, []string{"-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "precommit_test.py"}},
		{"evaluation-recorder", python, []string{"-B", "-m", "unittest", "discover", "-s", "evals/agent-adoption", "-p", "record_test.py"}},
	} {
		command := exec.CommandContext(cmd.Context(), check.program, check.args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		status := operator.StatusSuccess
		summary := check.program + " " + strings.Join(check.args, " ") + " passed"
		if err != nil {
			status = operator.StatusFailure
			summary = fmt.Sprintf("%s %s failed: %v", check.program, strings.Join(check.args, " "), err)
		}
		rt.AddStep(operator.Step{ID: check.id, Title: "Run " + check.id, Status: status, Summary: summary, Details: map[string]any{"output": strings.TrimSpace(string(output))}})
		if err != nil {
			return rt.Complete(status, "release checks failed")
		}
	}
	return rt.Complete(operator.StatusSuccess, "portable source checks passed; runtime acceptance is a separate gate")
}

// Release assets are enumerated once for packaging and bootstrap validation.
func releaseAssetFiles() []string { return releaseassets.Files() }

func releaseAssetInfo(root, name string) (os.FileInfo, error) {
	path := root
	var info os.FileInfo
	for _, part := range strings.Split(name, "/") {
		path = filepath.Join(path, part)
		var err error
		info, err = os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("release asset %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("release asset %s contains a symlink", name)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("release asset %s is not a regular file", name)
	}
	return info, nil
}

func checkReleaseSource(root string) error {
	for _, path := range releaseAssetFiles() {
		if _, err := releaseAssetInfo(root, path); err != nil {
			return err
		}
	}
	for _, path := range []string{"docs/reference/entity-list.txt", "docs/reference/device-list.txt", "docs/reference/area-list.txt", ".env", ".env.local"} {
		if fileExists(filepath.Join(root, path)) {
			return fmt.Errorf("private/runtime file present in source: %s", path)
		}
	}
	return nil
}

func runReleaseBuild(cmd *cobra.Command, args []string) error {
	if !releaseVersionPattern.MatchString(releaseVersion) {
		return fmt.Errorf("--version must be an explicit semantic release version")
	}
	if releaseHomebrewRepository != "" {
		if err := validateHomebrewRepository(releaseHomebrewRepository); err != nil {
			return err
		}
	}
	root, err := releaseRoot()
	if err != nil {
		return err
	}
	if err := checkReleaseSource(root); err != nil {
		return err
	}
	out := releaseOutput
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	rt := newCommandRuntime("release", releaseJSON, cmd.OutOrStdout())
	rt.SetProfile("build")
	revisionValue := "unknown"
	git := exec.CommandContext(cmd.Context(), "git", "rev-parse", "HEAD")
	git.Dir = root
	if data, err := git.Output(); err == nil {
		revisionValue = strings.TrimSpace(string(data))
	}
	dirty := exec.CommandContext(cmd.Context(), "git", "status", "--porcelain")
	dirty.Dir = root
	if data, err := dirty.Output(); err == nil && len(data) > 0 {
		revisionValue += "-dirty"
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	var checksums strings.Builder
	archiveChecksums := make(map[string]string)
	for _, target := range []struct{ os, arch string }{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"windows", "amd64"}} {
		temp, err := os.MkdirTemp("", "denmother-package-")
		if err != nil {
			return err
		}
		binary := "dm"
		if target.os == "windows" {
			binary += ".exe"
		}
		pkg := "github.com/BrianTillman/Denmother/cmd"
		flags := fmt.Sprintf("-s -w -X %s.version=%s -X %s.revision=%s -X %s.buildDate=%s", pkg, releaseVersion, pkg, revisionValue, pkg, stamp)
		command := exec.CommandContext(cmd.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", filepath.Join(temp, binary), ".")
		command.Dir = root
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.os, "GOARCH="+target.arch)
		output, buildErr := command.CombinedOutput()
		if buildErr != nil {
			os.RemoveAll(temp)
			return fmt.Errorf("build %s/%s: %w: %s", target.os, target.arch, buildErr, strings.TrimSpace(string(output)))
		}
		name := fmt.Sprintf("denmother-%s-%s-%s.tar.gz", releaseVersion, target.os, target.arch)
		path := filepath.Join(out, name)
		err = writeReleaseArchive(path, root, filepath.Join(temp, binary))
		os.RemoveAll(temp)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		archiveChecksums[name] = digest
		fmt.Fprintf(&checksums, "%s  %s\n", digest, name)
		rt.AddStep(operator.Step{ID: target.os + "-" + target.arch, Title: "Package " + target.os + "/" + target.arch, Status: operator.StatusSuccess, Summary: name, Artifacts: []string{path}})
	}
	checksumPath := filepath.Join(out, "SHA256SUMS")
	if err := os.WriteFile(checksumPath, []byte(checksums.String()), 0644); err != nil {
		return err
	}
	if releaseHomebrewRepository != "" {
		formula, err := renderHomebrewFormula(releaseVersion, releaseHomebrewRepository, archiveChecksums)
		if err != nil {
			return err
		}
		formulaPath := filepath.Join(out, "homebrew", "Formula", "denmother.rb")
		if err := os.MkdirAll(filepath.Dir(formulaPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(formulaPath, formula, 0644); err != nil {
			return err
		}
		rt.AddStep(operator.Step{ID: "homebrew", Title: "Generate Homebrew formula", Status: operator.StatusSuccess, Summary: "formula uses release archive checksums and v" + releaseVersion + " download URLs", Artifacts: []string{formulaPath}})
		return rt.Complete(operator.StatusSuccess, "release archives, SHA256SUMS, and Homebrew formula created locally")
	}
	return rt.Complete(operator.StatusSuccess, "release archives and SHA256SUMS created locally")
}

func writeReleaseArchive(path, root, binary string) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	gz := gzip.NewWriter(file)
	defer func() {
		if closeErr := gz.Close(); err == nil {
			err = closeErr
		}
	}()
	archive := tar.NewWriter(gz)
	defer func() {
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
	}()
	add := func(source, name string) error {
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(archive, input)
		return err
	}
	if err := add(binary, filepath.Base(binary)); err != nil {
		return err
	}
	for _, name := range releaseAssetFiles() {
		if _, err := releaseAssetInfo(root, name); err != nil {
			return err
		}
		if err := add(filepath.Join(root, name), name); err != nil {
			return err
		}
	}
	return nil
}
