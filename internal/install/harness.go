// Package install manages binary and skill installations with ownership tracking.
package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Options struct {
	Harnesses                                                      []string
	Scope, Project, Destination, BinDir, Version, Origin, Revision string
	Binary, BinaryOnly, DryRun, Offline, Recover                   bool
}
type Harness struct {
	Name        string   `json:"name"`
	Detected    bool     `json:"detected"`
	Evidence    []string `json:"evidence"`
	Destination string   `json:"destination"`
	Session     string   `json:"session"`
}

// Resolve canonicalizes existing ancestors, including a symlinked home.
// It leaves the final path component unresolved for ownership inspection.
func Resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return absolute, nil
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if os.IsNotExist(err) {
		resolved, err = Resolve(parent)
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(absolute)), nil
}
func Discover(o Options) ([]Harness, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if o.Scope != "user" && o.Scope != "project" {
		return nil, fmt.Errorf("scope must be user or project")
	}
	if o.Scope == "project" {
		if o.Project == "" {
			return nil, fmt.Errorf("project scope requires --project PATH (no user-scope fallback)")
		}
		info, err := os.Stat(o.Project)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("project is not a directory")
		}
	} else if o.Project != "" {
		return nil, fmt.Errorf("--project requires --scope project")
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	all := []Harness{{Name: "codex", Destination: filepath.Join(home, ".agents", "skills", "denmother"), Session: "Automatic discovery; restart Codex if changes do not appear."}, {Name: "claude", Destination: filepath.Join(claude, "skills", "denmother"), Session: "Restart Claude Code when creating a new top-level skills directory or changing supporting resources."}}
	for i := range all {
		h := &all[i]
		h.Evidence = []string{}
		if p, e := exec.LookPath(h.Name); e == nil {
			h.Detected = true
			h.Evidence = append(h.Evidence, "executable: "+p)
		}
		config := filepath.Join(codex, "config.toml")
		dir := codex
		if h.Name == "claude" {
			config = filepath.Join(claude, "settings.json")
			dir = claude
		}
		if info, e := os.Stat(config); e == nil && info.Mode().IsRegular() && info.Size() > 0 {
			h.Detected = true
			h.Evidence = append(h.Evidence, "configured: "+config)
		} else if info, e := os.Stat(dir); e == nil && info.IsDir() {
			h.Evidence = append(h.Evidence, "ambiguous configuration directory: "+dir)
		}
		if o.Scope == "project" {
			d := ".agents"
			if h.Name == "claude" {
				d = ".claude"
			}
			h.Destination = filepath.Join(o.Project, d, "skills", "denmother")
		}
		if info, e := os.Stat(filepath.Join(h.Destination, manifestName)); e == nil && info.Mode().IsRegular() {
			h.Detected = true
			h.Evidence = append(h.Evidence, "registration manifest: "+h.Destination)
		}
	}
	selected := map[string]bool{}
	names := o.Harnesses
	if len(names) == 0 {
		names = []string{"detected"}
	}
	for _, value := range names {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			switch name {
			case "detected":
				for _, h := range all {
					if h.Detected {
						selected[h.Name] = true
					}
				}
			case "codex", "claude":
				selected[name] = true
			default:
				return nil, fmt.Errorf("unknown harness %q; supported: detected, codex, claude", name)
			}
		}
	}
	if o.Destination != "" && len(selected) != 1 {
		return nil, fmt.Errorf("--destination requires exactly one harness; use --harness codex --destination PATH")
	}
	var result []Harness
	for _, h := range all {
		if selected[h.Name] {
			if o.Destination != "" {
				h.Destination = o.Destination
			}
			h.Destination, err = Resolve(h.Destination)
			if err != nil {
				return nil, err
			}
			result = append(result, h)
		}
	}
	return result, nil
}
func sortedKeys[T any](m map[string]T) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
