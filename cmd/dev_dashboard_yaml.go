package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resolve only the Lovelace subtree of configuration.yaml. Unrelated integration
// secrets/includes must not be required to inspect a dashboard.
func dashboardLovelaceConfig(configDir string) (*yaml.Node, error) {
	path := filepath.Join(configDir, "configuration.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	node := dashboardMapValue(yamlDocument(&root), "lovelace")
	return expandDashboardNode(node, configDir, map[string]bool{}, map[*yaml.Node]bool{})
}

func dashboardMapValue(node *yaml.Node, key string) *yaml.Node {
	if node != nil && node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return node.Content[i+1]
			}
		}
	}
	return nil
}

func loadDashboardYAML(path string) (*yaml.Node, error) {
	return readDashboardYAML(path, map[string]bool{})
}

func readDashboardYAML(path string, active map[string]bool) (*yaml.Node, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if active[path] {
		return nil, fmt.Errorf("cyclic YAML include: %s", path)
	}
	active[path] = true
	defer delete(active, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return expandDashboardNode(yamlDocument(&root), filepath.Dir(path), active, map[*yaml.Node]bool{})
}

func expandDashboardNode(node *yaml.Node, dir string, files map[string]bool, nodes map[*yaml.Node]bool) (*yaml.Node, error) {
	if node == nil {
		return nil, nil
	}
	if nodes[node] {
		return nil, fmt.Errorf("cyclic YAML alias at line %d", node.Line)
	}
	nodes[node] = true
	defer delete(nodes, node)
	if node.Kind == yaml.AliasNode {
		return expandDashboardNode(node.Alias, dir, files, nodes)
	}
	if node.Tag == "!include" {
		return readDashboardYAML(filepath.Join(dir, node.Value), files)
	}
	if strings.HasPrefix(node.Tag, "!include_dir_") {
		path := filepath.Join(dir, node.Value)
		var entries []string
		err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() && name != path {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() && filepath.Ext(name) == ".yaml" {
				entries = append(entries, name)
			}
			return nil
		})
		sort.Strings(entries)
		if err != nil {
			return nil, fmt.Errorf("read include directory %s: %w", path, err)
		}
		result := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if node.Tag == "!include_dir_named" || node.Tag == "!include_dir_merge_named" {
			result.Kind, result.Tag = yaml.MappingNode, "!!map"
		}
		if node.Tag != "!include_dir_named" && node.Tag != "!include_dir_merge_named" && node.Tag != "!include_dir_list" && node.Tag != "!include_dir_merge_list" {
			return nil, fmt.Errorf("unsupported YAML tag %s", node.Tag)
		}
		for _, entry := range entries {
			child, err := readDashboardYAML(entry, files)
			if err != nil {
				return nil, err
			}
			switch node.Tag {
			case "!include_dir_named":
				result.Content = append(result.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.TrimSuffix(filepath.Base(entry), filepath.Ext(entry))}, child)
			case "!include_dir_list":
				result.Content = append(result.Content, child)
			default:
				if child == nil || child.Kind != result.Kind {
					return nil, fmt.Errorf("%s has incompatible YAML for %s", entry, node.Tag)
				}
				result.Content = append(result.Content, child.Content...)
			}
		}
		return result, nil
	}
	copy := *node
	copy.Content = nil
	for _, child := range node.Content {
		expanded, err := expandDashboardNode(child, dir, files, nodes)
		if err != nil {
			return nil, err
		}
		copy.Content = append(copy.Content, expanded)
	}
	if copy.Kind == yaml.MappingNode {
		return mergeDashboardMapping(&copy)
	}
	return &copy, nil
}

// YAML merge sequences give earlier maps priority; explicit keys always win.
func mergeDashboardMapping(node *yaml.Node) (*yaml.Node, error) {
	explicit := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Tag != "!!merge" {
			explicit[node.Content[i].Value] = true
		}
	}
	merged := map[string]bool{}
	result := *node
	result.Content = nil
	var add func(*yaml.Node) error
	add = func(source *yaml.Node) error {
		if source == nil {
			return fmt.Errorf("empty YAML merge")
		}
		if source.Kind == yaml.SequenceNode {
			for _, item := range source.Content {
				if err := add(item); err != nil {
					return err
				}
			}
			return nil
		}
		if source.Kind != yaml.MappingNode {
			return fmt.Errorf("YAML merge must contain a mapping")
		}
		for i := 0; i+1 < len(source.Content); i += 2 {
			key := source.Content[i].Value
			if !explicit[key] && !merged[key] {
				result.Content = append(result.Content, source.Content[i], source.Content[i+1])
				merged[key] = true
			}
		}
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Tag == "!!merge" {
			if err := add(node.Content[i+1]); err != nil {
				return nil, err
			}
		} else {
			result.Content = append(result.Content, node.Content[i], node.Content[i+1])
		}
	}
	return &result, nil
}

func listDashboardTargets(configDir string) ([]dashboardTarget, error) {
	lovelace, err := dashboardLovelaceConfig(configDir)
	if err != nil {
		return nil, err
	}
	registrations := dashboardMapValue(lovelace, "dashboards")
	targets := map[string]string{}
	add := func(mapping *yaml.Node) error {
		if mapping == nil {
			return nil
		}
		if mapping.Kind != yaml.MappingNode {
			return fmt.Errorf("lovelace dashboards must be a YAML mapping")
		}
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			slug, spec := mapping.Content[i].Value, mapping.Content[i+1]
			if mode := yamlMapScalar(spec, "mode"); mode != "" && mode != "yaml" {
				continue
			}
			filename := yamlMapScalar(spec, "filename")
			if filename == "" {
				return fmt.Errorf("dashboard %q has no filename", slug)
			}
			if strings.ContainsAny(slug, "/\\") || slug == "." || slug == ".." {
				return fmt.Errorf("invalid dashboard slug %q", slug)
			}
			if !filepath.IsAbs(filename) {
				filename = filepath.Join(configDir, filename)
			}
			if info, err := os.Stat(filename); err != nil || info.IsDir() {
				return fmt.Errorf("dashboard %q references missing or invalid file %q", slug, filename)
			}
			if prior, exists := targets[slug]; exists && prior != filename {
				return fmt.Errorf("conflicting registration for dashboard %q", slug)
			}
			targets[slug] = filename
		}
		return nil
	}
	if err := add(registrations); err != nil {
		return nil, err
	}
	// Read standalone definitions only when configuration.yaml has no registrations.
	if registrations == nil {
		files, err := filepath.Glob(filepath.Join(configDir, "dashboards", "definitions", "*.yaml"))
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			mapping, err := loadDashboardYAML(file)
			if err != nil {
				return nil, err
			}
			if err := add(mapping); err != nil {
				return nil, err
			}
		}
	}
	if _, exists := targets["lovelace"]; !exists && yamlMapScalar(lovelace, "mode") == "yaml" {
		path := filepath.Join(configDir, "ui-lovelace.yaml")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("default YAML dashboard: %w", err)
		}
		targets["lovelace"] = path
	}
	result := make([]dashboardTarget, 0, len(targets))
	for slug, file := range targets {
		result = append(result, dashboardTarget{Slug: slug, File: file})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Slug < result[j].Slug })
	return result, nil
}

func configuredDashboardResources(configDir string) ([]string, error) {
	config, err := dashboardLovelaceConfig(configDir)
	if err != nil {
		return nil, err
	}
	resources := dashboardMapValue(config, "resources")
	if resources == nil {
		return nil, nil
	}
	if resources.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("lovelace resources must be a list")
	}
	var urls []string
	for _, resource := range resources.Content {
		value := yamlMapScalar(resource, "url")
		if value == "" {
			return nil, fmt.Errorf("lovelace resource has no URL")
		}
		urls = append(urls, value)
	}
	sort.Strings(urls)
	return urls, nil
}

func dashboardViewPaths(path string, selected []string) ([]string, error) {
	root, err := loadDashboardYAML(path)
	if err != nil {
		return nil, err
	}
	return dashboardNodeViewPaths(root, selected)
}

func dashboardNodeViewPaths(root *yaml.Node, selected []string) ([]string, error) {
	views := dashboardMapValue(root, "views")
	if views == nil || views.Kind != yaml.SequenceNode || len(views.Content) == 0 {
		return nil, fmt.Errorf("dashboard must define at least one view")
	}
	var paths []string
	for index, view := range views.Content {
		path := yamlMapScalar(view, "path")
		if path == "" {
			path = strconv.Itoa(index)
		}
		paths = append(paths, path)
	}
	if len(selected) == 0 {
		return paths, nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, selection := range selected {
		found := ""
		for index, path := range paths {
			if selection == path || selection == strconv.Itoa(index) {
				found = path
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("dashboard view %q was not found", selection)
		}
		if !seen[found] {
			result = append(result, found)
			seen[found] = true
		}
	}
	return result, nil
}
