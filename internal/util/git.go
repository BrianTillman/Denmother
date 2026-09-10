package util

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GetStagedYAMLFiles returns YAML files staged for commit
func GetStagedYAMLFiles(rootDir string) ([]string, error) {
	return GetStagedYAMLFilesContext(context.Background(), rootDir)
}

func GetStagedYAMLFilesContext(ctx context.Context, rootDir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-only", "-z", "--relative", "--diff-filter=ACMR")
	cmd.Dir = rootDir
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(string(output), "\x00") {
		if line == "" {
			continue
		}
		if IsYAMLFile(line) && filepath.Base(line) != "secrets.yaml" && filepath.Base(line) != "secrets.yml" {
			files = append(files, filepath.Join(rootDir, line))
		}
	}

	return files, nil
}
