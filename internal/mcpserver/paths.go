package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrianTillman/Denmother/internal/project"
	"gopkg.in/yaml.v3"
)

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// relative resolves existing ancestors as well as the final symlink. Missing
// changed files are supported (Git deletions), but their parents cannot escape.
func (a *Adapter) relative(path string, exists bool) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("a nonempty project-relative path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.cfg.ProjectRoot, path)
	}
	path = filepath.Clean(path)
	if !inside(a.cfg.ProjectRoot, path) {
		return "", fmt.Errorf("path is outside the configured project root")
	}
	candidate := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			if !inside(a.cfg.ProjectRoot, resolved) {
				return "", fmt.Errorf("symlink resolves outside the configured project root")
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			if exists {
				info, err := os.Stat(resolved)
				if err != nil {
					return "", err
				}
				if !info.Mode().IsRegular() {
					return "", fmt.Errorf("expected a regular evidence or test file")
				}
			}
			return filepath.Rel(a.cfg.ProjectRoot, resolved)
		}
		// Dangling symlinks must never be mistaken for deleted regular files.
		if info, e := os.Lstat(candidate); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dangling symlink is not allowed")
		}
		if exists || !os.IsNotExist(err) || candidate == a.cfg.ProjectRoot {
			return "", fmt.Errorf("cannot resolve project path")
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = filepath.Dir(candidate)
	}
}

func (a *Adapter) checkProject(ctx context.Context) error {
	if _, err := a.relative(a.cfg.ConfigDir, false); err != nil {
		return err
	}
	// Policy may refer to inventories or fixtures outside config_dir. These must
	// also belong to the configured project, even when those files are missing.
	for _, p := range []string{filepath.Join(a.cfg.ConfigDir, project.Filename), filepath.Join(filepath.Dir(a.cfg.ConfigDir), project.Filename)} {
		if inside(a.cfg.ProjectRoot, p) {
			rel, err := a.relative(p, false)
			if err != nil {
				return err
			}
			if _, err := readProjectFile(a.root, rel); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("project policy: %w", err)
			}
		}
	}
	settings, err := project.ForConfigWithin(a.cfg.ConfigDir, a.cfg.ProjectRoot)
	if err != nil {
		settings = project.Settings{}
	} // The CLI reports malformed policy; still scan symlinks.
	for _, p := range []string{settings.Root, settings.ReferencesDir, settings.DevFixtures, settings.DevScenarios, settings.DevComposeTemplate} {
		if p != "" {
			if _, err := a.relative(p, false); err != nil {
				return fmt.Errorf("project policy: %w", err)
			}
		}
	}
	visited := make(map[string]bool)
	protected := []string{a.cfg.ConfigDir, settings.ReferencesDir, settings.DevFixtures, settings.DevScenarios, settings.DevComposeTemplate}
	return filepath.WalkDir(a.cfg.ProjectRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".cache", "node_modules", "dist", ".dev-build-tmp":
				if path != a.cfg.ProjectRoot {
					needed := false
					for _, selected := range protected {
						if selected != "" && (inside(path, selected) || inside(selected, path)) {
							needed = true
						}
					}
					if !needed {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := a.relative(path, true); err != nil {
				return fmt.Errorf("project symlink: %w", err)
			}
		} else if !entry.Type().IsRegular() {
			return fmt.Errorf("project contains a non-regular file")
		}
		if !inside(a.cfg.ConfigDir, path) || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		return a.checkYAML(ctx, path, visited)
	})
}

// Bound both the allocation and the read itself, and reject pipes/devices before
// reading. os.Root also enforces confinement when the file is opened.
func readProjectFile(root *os.Root, rel string) ([]byte, error) {
	f, err := openEvidence(root, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("project input must be a regular file")
	}
	if info.Size() > MaxResultBytes {
		return nil, fmt.Errorf("project input exceeds %d bytes", MaxResultBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxResultBytes+1))
	if len(data) > MaxResultBytes {
		return nil, fmt.Errorf("project input exceeds %d bytes", MaxResultBytes)
	}
	return data, err
}

// Follow includes even outside config_dir and inside otherwise skipped cache
// directories. Track lexical paths: relative includes use the source's directory.
func (a *Adapter) checkYAML(ctx context.Context, path string, visited map[string]bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	rel, err := a.relative(path, false)
	if err != nil {
		return err
	}
	if visited[path] {
		return nil
	}
	visited[path] = true
	// Cap recursion/work for cyclic aliases and adversarial include graphs.
	if len(visited) > 4096 {
		return fmt.Errorf("YAML reference graph exceeds 4096 paths")
	}
	info, err := a.root.Stat(rel)
	if os.IsNotExist(err) {
		return nil // The CLI supplies missing-input diagnostics.
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if entry, err := os.Lstat(path); err != nil || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory symlinks are not allowed")
		}
		return filepath.WalkDir(path, func(child string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if _, err := a.relative(child, true); err != nil {
				return err
			}
			if filepath.Ext(child) == ".yaml" || filepath.Ext(child) == ".yml" {
				return a.checkYAML(ctx, child, visited)
			}
			return nil
		})
	}
	data, err := readProjectFile(a.root, rel)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			return nil // Syntax diagnostics belong to the CLI.
		}
		seen := make(map[*yaml.Node]bool)
		var check func(*yaml.Node) error
		check = func(n *yaml.Node) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if n == nil || seen[n] {
				return nil
			}
			seen[n] = true
			if strings.HasPrefix(n.Tag, "!include") && n.Value != "" {
				include := n.Value
				if !filepath.IsAbs(include) {
					include = filepath.Join(filepath.Dir(path), include)
				}
				if err := a.checkYAML(ctx, include, visited); err != nil {
					return fmt.Errorf("YAML include: %w", err)
				}
			}
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					if n.Content[i].Value != "use_blueprint" {
						continue
					}
					var use struct {
						Path string `yaml:"path"`
					}
					if n.Content[i+1].Decode(&use) == nil && use.Path != "" {
						blueprint := use.Path
						if !filepath.IsAbs(blueprint) {
							blueprint = filepath.Join(a.cfg.ConfigDir, "blueprints", "automation", blueprint)
						}
						if err := a.checkYAML(ctx, blueprint, visited); err != nil {
							return fmt.Errorf("blueprint: %w", err)
						}
					}
				}
			}
			if err := check(n.Alias); err != nil {
				return err
			}
			for _, child := range n.Content {
				if err := check(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := check(&doc); err != nil {
			return err
		}
	}
}
