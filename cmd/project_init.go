package cmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/examples"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/BrianTillman/Denmother/internal/project"
	"github.com/BrianTillman/Denmother/skills"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type initFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func init() {
	var directory, example string
	var agent, skill, dryRun, jsonOutput bool
	command := &cobra.Command{Use: "init", Short: "Set up a project and synthetic tests while preserving existing files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var source fs.FS = examples.Quickstart
			if example != "quickstart" {
				if example != "dashboard" && example != "automations" {
					return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("--example must be quickstart, dashboard, or automations")}
				}
				source = examples.Starters
			}
			root, err := filepath.Abs(directory)
			if err != nil {
				return err
			}
			config := configPath
			if !cmd.Flag("config").Changed {
				if _, err := os.Stat(filepath.Join(root, project.Filename)); err == nil {
					config, err = project.DiscoverConfig(root)
					if err != nil {
						return err
					}
				}
			}
			if !filepath.IsAbs(config) {
				config = filepath.Join(root, config)
			}
			config, err = filepath.Abs(config)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, config)
			if err != nil || !isLocalRelPath(rel) {
				return &cliInputError{Code: "invalid_arguments", Err: fmt.Errorf("init --config must be a directory inside --directory")}
			}
			// Never insert synthetic helpers into an existing configuration.
			starterRoot, starterConfig := root, config
			entries, err := os.ReadDir(config)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			original, _ := fs.ReadFile(source, example+"/ha-config/configuration.yaml")
			existing, _ := os.ReadFile(filepath.Join(config, "configuration.yaml"))
			if len(entries) > 0 && !bytes.Equal(original, existing) {
				starterName := "denmother"
				if example != "quickstart" {
					starterName += "-" + example
				}
				starterRoot = filepath.Join(root, "examples", starterName)
				starterConfig = filepath.Join(starterRoot, "ha-config")
			}
			files := map[string][]byte{}
			// Protect generated credentials even when the host project has no
			// root ignore rules. Existing ignore files are preserved like all inputs.
			for _, generatedRoot := range []string{root, starterRoot} {
				files[filepath.Join(generatedRoot, ".devcontainer", "worktrees", ".gitignore")] = []byte("*\n!.gitignore\n")
				files[filepath.Join(generatedRoot, "artifacts", "dm", ".gitignore")] = []byte("*\n!.gitignore\n")
			}
			settingsMap := map[string]any{"version": 1, "config_dir": filepath.ToSlash(rel), "references_dir": "docs/reference"}
			if example == "dashboard" && starterRoot == root {
				settingsMap["dev_fixtures"] = "fixtures.json"
				settingsMap["dev_scenarios"] = "scenarios.json"
			}
			settings, err := yaml.Marshal(settingsMap)
			if err != nil {
				return err
			}
			files[filepath.Join(root, project.Filename)] = settings
			err = fs.WalkDir(source, example, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				data, err := fs.ReadFile(source, path)
				if err != nil {
					return err
				}
				name := strings.TrimPrefix(path, example+"/")
				if name == project.Filename {
					return nil
				}
				destination := filepath.Join(starterRoot, filepath.FromSlash(name))
				if strings.HasPrefix(name, "ha-config/") {
					destination = filepath.Join(starterConfig, strings.TrimPrefix(name, "ha-config/"))
				}
				files[destination] = data
				return nil
			})
			if err != nil {
				return err
			}
			if starterRoot != root {
				starterSettings := "version: 1\nconfig_dir: ha-config\n"
				if example == "dashboard" {
					starterSettings += "dev_fixtures: fixtures.json\ndev_scenarios: scenarios.json\n"
				}
				files[filepath.Join(starterRoot, project.Filename)] = []byte(starterSettings)
			}
			if agent {
				name := filepath.Join(root, "AGENTS.md")
				if _, err := os.Lstat(name); err == nil {
					name = filepath.Join(root, "DENMOTHER-AGENT.md")
				}
				files[name] = []byte("# Denmother\n\nUse Denmother for Home Assistant YAML validation and automation tests.\n\n- Run `dm capabilities --json` and `dm agent context --json` to discover this project.\n- Run `dm --no-schema --json` for offline validation; this does not prove runtime behavior.\n- Use `dm test plan --json` to select relevant tests, and inspect fresh traces on failures.\n- Runtime tests change HA state. Use a dedicated development configuration.\n- Preserve the selected configuration and target in subsequent commands.\n- Report incomplete verification as incomplete, including when context collection succeeds.\n- The optional skill is in `.agents/skills/denmother/SKILL.md`.\n")
			}
			if skill {
				err = fs.WalkDir(skills.Files, "denmother", func(path string, entry fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if entry.IsDir() {
						return nil
					}
					data, err := skills.Files.ReadFile(path)
					if err != nil {
						return err
					}
					files[filepath.Join(root, ".agents", "skills", filepath.FromSlash(path))] = data
					return nil
				})
				if err != nil {
					return err
				}
			}
			rt := newCommandRuntime("init", jsonOutput, cmd.OutOrStdout())
			paths := make([]string, 0, len(files))
			for path := range files {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			// Validate all destinations before the first write. Symlinks must not
			// redirect installation outside the requested project.
			for _, path := range paths {
				if err := validateInitPath(root, path); err != nil {
					return err
				}
				if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
					return fmt.Errorf("setup destination is not a regular file: %s", path)
				}
			}
			var reports []initFile
			for _, path := range paths {
				status := "planned"
				if _, err := os.Lstat(path); err == nil {
					status = "preserved"
				} else if !os.IsNotExist(err) {
					return err
				}
				if status == "planned" && !dryRun {
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						return err
					}
					f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
					if os.IsExist(err) {
						status = "preserved"
					} else {
						if err != nil {
							return err
						}
						_, err = f.Write(files[path])
						closeErr := f.Close()
						if err != nil {
							return err
						}
						if closeErr != nil {
							return closeErr
						}
						status = "created"
					}
				}
				reports = append(reports, initFile{path, status})
			}
			next := []string{"dm --config " + shellQuote(starterConfig) + " --no-schema --json", "dm dev up --config " + shellQuote(starterConfig) + " --json", "dm test --config " + shellQuote(starterConfig) + " --json", "dm dev down --config " + shellQuote(starterConfig) + " --json"}
			if example == "dashboard" {
				next = []string{"dm dev dashboard denmother-demo --config " + shellQuote(starterConfig) + " --ensure-dev --render --require-running --json", "dm dev scenario warm --config " + shellQuote(starterConfig) + " --json", "dm dev down --config " + shellQuote(starterConfig) + " --json"}
			}
			rt.AddStep(operator.Step{ID: "project-init", Title: "Initialize project", Status: operator.StatusSuccess, Summary: "project setup prepared; existing files preserved", Mutates: !dryRun,
				Details:      map[string]any{"files": reports, "config_root": config, "example_config": starterConfig, "dry_run": dryRun, "example": example},
				NextCommands: next})
			return rt.Complete(operator.StatusSuccess, "Denmother project setup complete")
		}}
	command.Flags().StringVar(&example, "example", "quickstart", "Starter: quickstart, dashboard, or automations (timer, event, blueprint)")
	command.Flags().StringVar(&directory, "directory", ".", "Project directory to initialize")
	command.Flags().BoolVar(&agent, "agent", false, "Create an agent instruction file without replacing existing instructions")
	command.Flags().BoolVar(&skill, "skill", false, "Copy the bundled skill to .agents/skills/denmother")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Describe files that would be created without writing them")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit a structured setup report")
	rootCmd.AddCommand(command)
}

func validateInitPath(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || !isLocalRelPath(rel) {
		return fmt.Errorf("setup path escapes project")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("setup refuses symlink: %s", current)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}
