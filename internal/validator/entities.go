package validator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/project"

	"github.com/BrianTillman/Denmother/internal/hayaml"
	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

var entityPaths = []string{
	"configuration.yaml",
	"automations",
	"automations.yaml",
	"scenes",
	"scenes.yaml",
	"homekit",
	"helpers",
	"dashboards",
	"customize.yaml",
	"scripts.yaml",
	"scripts",
	"packages",
}

type entityRef struct {
	entity  string
	file    string
	line    int
	context string
}

// validateEntities checks entity references against entity-list.txt
func validateEntities(configPath string, opts Options) error {
	return checkEntities(configPath, opts).Err()
}

func checkEntities(configPath string, opts Options) CheckResult {
	result := CheckResult{Name: "entities", Status: CheckPassed, Summary: "static entity references passed"}
	if err := validateEntitiesInternal(configPath, opts, &result); err != nil {
		fallback := ResultFromError("entities", err)
		result.Status = fallback.Status
		result.cause = fallback.cause
		result.Summary = fallback.Summary
		if len(result.Findings) == 0 {
			result.Findings = fallback.Findings
		}
		if result.Details == nil {
			result.Details = fallback.Details
		}
	}
	return result
}

func validateEntitiesInternal(configPath string, opts Options, result *CheckResult) error {
	if err := opts.context().Err(); err != nil {
		return err
	}
	color.New(color.FgBlue).Println("Validating entity references...")
	fmt.Println()

	settings, err := project.ForConfig(configPath)
	if err != nil {
		return err
	}
	entityListPath := filepath.Join(settings.ReferencesDir, "entity-list.txt")
	validEntities, err := loadEntityList(entityListPath)
	if err != nil {
		summary := "entity validation incomplete: entity-list.txt not found"
		color.Yellow("  %s entity-list.txt not found, entity validation incomplete", warning)
		color.Yellow("    Run: dm sync --entities-only")
		fmt.Println()
		return incompleteResult("entities", summary, []string{summary}, map[string]any{
			"entity_list": entityListPath,
		}).Err()
	}

	color.Cyan("  i Loaded %d valid entities", len(validEntities))
	for entity := range loadLocalConfigEntities(configPath) {
		validEntities[entity] = true
	}

	var refs []entityRef
	var unresolved int
	for _, p := range entityPaths {
		if err := opts.context().Err(); err != nil {
			return err
		}
		fullPath := filepath.Join(configPath, p)
		if !util.FileExists(fullPath) && !util.DirExists(fullPath) {
			continue
		}

		files := getYAMLFiles(fullPath)
		for _, file := range files {
			if err := opts.context().Err(); err != nil {
				return err
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read entity references: %w", err)
			}
			extracted, err := hayaml.ExtractReferences(data)
			if err != nil {
				return fmt.Errorf("%s: extract entity references: %w", file, err)
			}
			unresolved += len(extracted.Dynamic)
			for _, ref := range extracted.Static {
				refs = append(refs, entityRef{entity: ref.Entity, file: util.RelPath(configPath, file), line: ref.Line})
			}
		}
	}

	if unresolved > 0 {
		color.Yellow("  %d dynamic entity reference(s) require runtime evaluation; static checks do not resolve templates or !input", unresolved)
	}

	result.Details = map[string]any{"static_references": len(refs), "dynamic_references": unresolved, "entity_list": entityListPath, "scope": "static references only; dynamic values require runtime evaluation"}

	var invalid []entityRef
	for _, ref := range refs {
		if err := opts.context().Err(); err != nil {
			return err
		}
		if _, ok := validEntities[ref.entity]; !ok {
			invalid = append(invalid, ref)
			result.Findings = append(result.Findings, fmt.Sprintf("%s:%d: unknown entity %s", ref.file, ref.line, ref.entity))
		}
	}

	if len(invalid) > 0 {
		fmt.Println()
		color.Red("  %s Found %d invalid entity references:", crossMark, len(invalid))
		fmt.Println()

		byFile := make(map[string][]entityRef)
		for _, ref := range invalid {
			byFile[ref.file] = append(byFile[ref.file], ref)
		}

		for file, fileRefs := range byFile {
			color.Yellow("  %s", file)
			for _, ref := range fileRefs {
				fmt.Printf("    Line %d: %s\n", ref.line, ref.entity)
				if opts.Fix {
					suggestions := findSimilar(ref.entity, validEntities)
					if len(suggestions) > 0 {
						color.Cyan("      Did you mean: %s?", suggestions[0])
					}
				}
			}
			fmt.Println()
		}

		fmt.Printf("  Checked %d references, %d valid, %d invalid\n", len(refs), len(refs)-len(invalid), len(invalid))
		fmt.Println()
		return fmt.Errorf("entity validation failed: %d unknown references", len(invalid))
	}

	color.Green("  %s All %d entity references valid", checkMark, len(refs))
	fmt.Println()
	return nil
}

func loadEntityList(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entities := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entities[line] = true
	}

	return entities, scanner.Err()
}

func loadLocalConfigEntities(configPath string) map[string]bool {
	entities := make(map[string]bool)
	helperFiles, err := doublestar.FilepathGlob(filepath.Join(configPath, "helpers/**/*.yaml"))
	if err != nil {
		return entities
	}
	for _, path := range helperFiles {
		if strings.Contains(filepath.Base(path), "secrets") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}
		root := yamlRoot(&doc)
		addHelperEntities(entities, helperDomain(path), root)
		addTemplateEntities(entities, root)
	}

	automationFiles, err := doublestar.FilepathGlob(filepath.Join(configPath, "automations/**/*.yaml"))
	if err != nil {
		return entities
	}
	for _, path := range automationFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}
		root := yamlRoot(&doc)
		if root == nil || root.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range root.Content {
			if id := yamlMapScalar(item, "id"); id != "" {
				entities[entityID("automation", id)] = true
			}
		}
	}
	return entities
}

func addHelperEntities(entities map[string]bool, domain string, root *yaml.Node) {
	if domain == "" || root == nil {
		return
	}
	switch root.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i]
			if key.Kind == yaml.ScalarNode && key.Value != "" {
				entities[domain+"."+key.Value] = true
			}
		}
	case yaml.SequenceNode:
		for _, item := range root.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			if uniqueID := yamlMapScalar(item, "unique_id"); uniqueID != "" {
				entities[entityID(domain, uniqueID)] = true
			}
		}
	}
}

func addTemplateEntities(entities map[string]bool, root *yaml.Node) {
	if root == nil || root.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range root.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			domainNode := item.Content[i]
			definitions := item.Content[i+1]
			if domainNode.Kind != yaml.ScalarNode || definitions.Kind != yaml.SequenceNode {
				continue
			}
			domain := domainNode.Value
			if !localEntityDomain(domain) {
				continue
			}
			for _, definition := range definitions.Content {
				if definition.Kind != yaml.MappingNode {
					continue
				}
				if uniqueID := yamlMapScalar(definition, "unique_id"); uniqueID != "" {
					entities[entityID(domain, uniqueID)] = true
				}
			}
		}
	}
}

func helperDomain(path string) string {
	parent := filepath.Base(filepath.Dir(path))
	if strings.HasSuffix(parent, "-groups") {
		return domainFromName(parent)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return domainFromName(base)
}

func domainFromName(name string) string {
	normalized := strings.ReplaceAll(name, "-", "_")
	for _, domain := range localEntityDomains() {
		if normalized == domain || strings.HasPrefix(normalized, domain+"_") {
			return domain
		}
	}
	return ""
}

func localEntityDomain(domain string) bool {
	for _, candidate := range localEntityDomains() {
		if domain == candidate {
			return true
		}
	}
	return false
}

func localEntityDomains() []string {
	return []string{
		"binary_sensor",
		"input_boolean",
		"input_button",
		"input_datetime",
		"input_number",
		"input_select",
		"input_text",
		"button",
		"fan",
		"light",
		"number",
		"sensor",
		"switch",
		"timer",
	}
}

func entityID(domain string, id string) string {
	id = strings.TrimSpace(id)
	if strings.Contains(id, ".") {
		return id
	}
	return domain + "." + id
}

func yamlRoot(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func yamlMapScalar(mapping *yaml.Node, key string) string {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && mapping.Content[i+1].Kind == yaml.ScalarNode {
			return mapping.Content[i+1].Value
		}
	}
	return ""
}

func getYAMLFiles(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	if !info.IsDir() {
		if util.IsYAMLFile(path) && !strings.Contains(filepath.Base(path), "secrets") {
			return []string{path}
		}
		return nil
	}

	var files []string
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if util.IsYAMLFile(p) && !strings.Contains(filepath.Base(p), "secrets") {
			files = append(files, p)
		}
		return nil
	})

	return files
}

func extractEntities(path string, configPath string) []entityRef {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	extracted, err := hayaml.ExtractReferences(data)
	if err != nil {
		return nil
	}
	var refs []entityRef
	for _, ref := range extracted.Static {
		refs = append(refs, entityRef{entity: ref.Entity, file: util.RelPath(configPath, path), line: ref.Line})
	}
	return refs
}

// similarEntity holds an entity and its similarity score for sorting
type similarEntity struct {
	entity string
	score  float64
}

// findSimilar finds entities similar to the target, sorted by similarity score.
// Returns the top 3 most similar entities from the same domain.
func findSimilar(target string, validEntities map[string]bool) []string {
	parts := strings.SplitN(target, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	domain := parts[0]
	targetName := parts[1]

	var candidates []similarEntity

	for entity := range validEntities {
		entityParts := strings.SplitN(entity, ".", 2)
		if len(entityParts) != 2 || entityParts[0] != domain {
			continue
		}

		score := util.Similarity(targetName, entityParts[1])
		if score > 0.5 {
			candidates = append(candidates, similarEntity{
				entity: entity,
				score:  score,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	result := make([]string, 0, 3)
	for i := 0; i < len(candidates) && i < 3; i++ {
		result = append(result, candidates[i].entity)
	}
	return result
}
