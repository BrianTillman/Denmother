package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// projectArtifactPath anchors repository assistance to the selected project,
// including when the CLI is run from another checkout or a nested directory.
func projectArtifactPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(selectedProjectRoot(), filepath.FromSlash(path))
}

func projectDisplayPath(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(selectedProjectRoot(), absolute)
	if err == nil && isLocalRelPath(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

type projectGitProcess struct {
	command *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
}

func projectGitCommand(args ...string) *projectGitProcess {
	ctx, cancel := context.WithTimeout(commandContext(), 10*time.Second)
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = selectedProjectRoot()
	command.WaitDelay = time.Second
	return &projectGitProcess{command: command, ctx: ctx, cancel: cancel}
}

func (p *projectGitProcess) Output() ([]byte, error) {
	defer p.cancel()
	output, err := p.command.Output()
	if p.ctx.Err() != nil {
		return output, p.ctx.Err()
	}
	return output, err
}

func (p *projectGitProcess) CombinedOutput() ([]byte, error) {
	defer p.cancel()
	output, err := p.command.CombinedOutput()
	if p.ctx.Err() != nil {
		return output, p.ctx.Err()
	}
	return output, err
}

func (p *projectGitProcess) Run() error {
	defer p.cancel()
	err := p.command.Run()
	if p.ctx.Err() != nil {
		return p.ctx.Err()
	}
	return err
}

// gitReviewPaths converts Git's root-relative names into usable caller paths.
func gitReviewPaths(root string, paths []string) []string {
	cwd, _ := os.Getwd()
	var confined []string
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if workerRoot := os.Getenv("DM_MCP_WORKER_ROOT"); workerRoot != "" {
			rel, err := filepath.Rel(workerRoot, full)
			if err != nil || !isLocalRelPath(rel) {
				continue
			}
		}
		if rel, err := filepath.Rel(cwd, full); err == nil && isLocalRelPath(rel) {
			confined = append(confined, filepath.ToSlash(rel))
		} else {
			confined = append(confined, filepath.ToSlash(full))
		}
	}
	return confined
}

// goTestCommandForChange finds the Go module containing a changed file.
func goTestCommandForChange(path string) string {
	base := filepath.Base(path)
	if !strings.HasSuffix(path, ".go") && base != "go.mod" && base != "go.sum" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if boundary := os.Getenv("DM_MCP_WORKER_ROOT"); boundary != "" {
			rel, err := filepath.Rel(boundary, dir)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ""
			}
		}
		if fileExists(filepath.Join(dir, "go.mod")) {
			cwd, _ := os.Getwd()
			if cwd == dir {
				return "go test ./..."
			}
			if rel, err := filepath.Rel(cwd, dir); err == nil && isLocalRelPath(rel) {
				dir = rel
			}
			return "cd " + shellQuote(filepath.ToSlash(dir)) + " && go test ./..."
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func commandImplementationChanged(paths []string) bool {
	for _, path := range paths {
		if goTestCommandForChange(path) == "" {
			continue
		}
		normalized := "/" + filepath.ToSlash(path)
		for _, part := range []string{"/cmd/", "/internal/operator/", "/internal/haconfig/"} {
			if strings.Contains(normalized, part) {
				return true
			}
		}
	}
	return false
}

// Policy artifact names preserve the managed repository contract while its
// configuration directory can be selected under a different name.
func policyArtifactPath(path string) string {
	if strings.HasPrefix(path, "ha-config/") {
		return configFile(strings.TrimPrefix(path, "ha-config/"))
	}
	return projectArtifactPath(path)
}
