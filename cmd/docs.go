package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BrianTillman/Denmother/internal/hayaml"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	docsGenerateCheck bool
	docsGenerateJSON  bool
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate and validate repository knowledge maps",
}

var docsGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate agent-readable architecture maps",
	RunE:  runDocsGenerate,
}

type generatedDoc struct {
	Path    string `json:"path"`
	Content string `json:"-"`
	Changed bool   `json:"changed"`
}

type docsGenerateSummary struct {
	Docs    []generatedDoc `json:"docs"`
	Changed []string       `json:"changed,omitempty"`
}

type automationDocRow struct {
	Path      string
	Category  string
	ID        string
	Alias     string
	Mode      string
	Blueprint string
	Triggers  int
	Test      string
}

type automationBlueprintDocInfo struct {
	Mode     string
	Triggers int
}

func init() {
	docsGenerateCmd.Flags().BoolVar(&docsGenerateCheck, "check", false, "Fail if generated docs are missing or stale")
	docsGenerateCmd.Flags().BoolVar(&docsGenerateJSON, "json", false, "Emit a machine-readable JSON summary")

	docsCmd.AddCommand(docsGenerateCmd)
	rootCmd.AddCommand(docsCmd)
}

func runDocsGenerate(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("docs", docsGenerateJSON, cmd.OutOrStdout())
	rt.SetProfile("generate")

	summary, err := buildGeneratedDocs(!docsGenerateCheck)
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "generate-docs",
			Title:   "Generate architecture maps",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "generated docs failed")
	}

	if docsGenerateCheck {
		for _, doc := range summary.Docs {
			if doc.Changed {
				summary.Changed = append(summary.Changed, doc.Path)
			}
		}
		sort.Strings(summary.Changed)
	} else if err := writeGeneratedDocs(summary.Docs); err != nil {
		rt.AddStep(operator.Step{
			ID:      "generate-docs",
			Title:   "Generate architecture maps",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "generated docs failed")
	}

	status := operator.StatusSuccess
	if docsGenerateCheck && len(summary.Changed) > 0 {
		status = operator.StatusFailure
	}

	details := map[string]any{
		"docs": generatedDocPaths(summary.Docs),
		"next_commands": []string{
			"./dm docs generate",
			"./dm docs generate --check --json",
		},
	}
	if len(summary.Changed) > 0 {
		details["failures"] = staleGeneratedDocIssues(summary.Changed)
		details["artifacts"] = summary.Changed
	}

	step := operator.Step{
		ID:      "generate-docs",
		Title:   "Generate architecture maps",
		Status:  status,
		Summary: fmt.Sprintf("%d generated doc(s) are current", len(summary.Docs)),
		Details: details,
	}
	if status == operator.StatusFailure {
		step.Summary = fmt.Sprintf("%d generated doc(s) are stale", len(summary.Changed))
		step.Hints = append(step.Hints, "Run `./dm docs generate` and review the generated map changes.")
	}
	rt.AddStep(step)

	if !docsGenerateJSON {
		for _, doc := range summary.Docs {
			state := "updated"
			if docsGenerateCheck {
				state = "current"
				if doc.Changed {
					state = "stale"
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", state, doc.Path)
		}
	}

	return rt.Complete(status, docsGenerateSummaryText(status, summary))
}

func buildGeneratedDocs(assumeBaseDocsCurrent bool) (*docsGenerateSummary, error) {
	docs, automations, err := buildBaseGeneratedDocs()
	if err != nil {
		return nil, err
	}
	qualityInputs := docs
	if assumeBaseDocsCurrent {
		qualityInputs = make([]generatedDoc, len(docs))
		copy(qualityInputs, docs)
		for i := range qualityInputs {
			qualityInputs[i].Changed = false
		}
	}
	report := buildAgentQualityReportFromInputs(automations, qualityInputs)
	qualityDoc := generatedDoc{
		Path:    projectArtifactPath("docs/generated/quality-score.md"),
		Content: generateQualityScoreDoc(report),
	}
	current, err := os.ReadFile(projectArtifactPath(qualityDoc.Path))
	qualityDoc.Changed = err != nil || string(current) != qualityDoc.Content
	docs = append(docs, qualityDoc)
	return &docsGenerateSummary{Docs: docs}, nil
}

func buildBaseGeneratedDocs() ([]generatedDoc, []automationDocRow, error) {
	if _, err := selectedProject(); err != nil {
		return nil, nil, err
	}
	automations, err := collectAutomationDocs()
	if err != nil {
		return nil, nil, err
	}
	homekit, err := generateHomeKitExposureDoc()
	if err != nil {
		return nil, nil, err
	}
	docs := []generatedDoc{
		{Path: "docs/generated/automation-index.md", Content: generateAutomationIndexDoc(automations)},
		{Path: "docs/generated/blueprint-usage.md", Content: generateBlueprintUsageDoc(automations)},
		{Path: "docs/generated/entity-graph.md", Content: generateEntityGraphDoc()},
		{Path: "docs/generated/homekit-exposure.md", Content: homekit},
		{Path: "docs/generated/dm-json-schema.json", Content: operator.ResultJSONSchema()},
	}
	for i := range docs {
		docs[i].Path = projectArtifactPath(docs[i].Path)
		current, err := os.ReadFile(projectArtifactPath(docs[i].Path))
		docs[i].Changed = err != nil || string(current) != docs[i].Content
	}
	return docs, automations, nil
}

func writeGeneratedDocs(docs []generatedDoc) error {
	if err := os.MkdirAll(projectArtifactPath("docs/generated"), 0755); err != nil {
		return err
	}
	for _, doc := range docs {
		if err := os.WriteFile(projectArtifactPath(doc.Path), []byte(doc.Content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", doc.Path, err)
		}
	}
	return nil
}

func collectAutomationDocs() ([]automationDocRow, error) {
	blueprints, err := collectAutomationBlueprintDocs()
	if err != nil {
		return nil, err
	}
	matches, err := globUnderConfig("automations/**/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	rows := make([]automationDocRow, 0, len(matches))
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, automation := range automationMappings(&doc) {
			rows = append(rows, automationRowFromNodeWithBlueprints(path, automation, blueprints))
		}
	}
	return rows, nil
}

func collectAutomationBlueprintDocs() (map[string]automationBlueprintDocInfo, error) {
	matches, err := globUnderConfig("blueprints/automation/**/*.yaml")
	if err != nil {
		return nil, err
	}
	blueprints := make(map[string]automationBlueprintDocInfo, len(matches))
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		root := documentRoot(&doc)
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(configFile("blueprints/automation"), absolutePath)
		if err != nil {
			return nil, err
		}
		blueprints[filepath.ToSlash(rel)] = automationBlueprintDocInfo{
			Mode:     automationBlueprintMode(root),
			Triggers: automationTriggerCount(root),
		}
	}
	return blueprints, nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	return root
}

func automationMappings(doc *yaml.Node) []*yaml.Node {
	root := documentRoot(doc)
	if root == nil {
		return nil
	}
	switch root.Kind {
	case yaml.SequenceNode:
		var nodes []*yaml.Node
		for _, item := range root.Content {
			if item.Kind == yaml.MappingNode {
				nodes = append(nodes, item)
			}
		}
		return nodes
	case yaml.MappingNode:
		return []*yaml.Node{root}
	default:
		return nil
	}
}

func automationRowFromNodeWithBlueprints(path string, node *yaml.Node, blueprints map[string]automationBlueprintDocInfo) automationDocRow {
	row := automationDocRow{
		Path:     path,
		Category: automationCategory(path),
		ID:       yamlScalarValue(mapValue(node, "id")),
		Alias:    yamlScalarValue(mapValue(node, "alias")),
		Mode:     yamlScalarValue(mapValue(node, "mode")),
		Test:     expectedAutomationTest(path),
	}
	if row.Mode == "" {
		row.Mode = "single"
	}
	if !fileExists(row.Test) {
		row.Test = ""
	}
	row.Triggers = automationTriggerCount(node)
	if bp := mapValue(node, "use_blueprint"); bp != nil && bp.Kind == yaml.MappingNode {
		row.Blueprint = yamlScalarValue(mapValue(bp, "path"))
		if info, ok := blueprints[normalizeAutomationBlueprintPath(row.Blueprint)]; ok {
			row.Mode = info.Mode
			row.Triggers = info.Triggers
		}
	}
	return row
}

func automationCategory(path string) string {
	rel := strings.TrimPrefix(relativeConfigPath(path), "automations/")
	if i := strings.Index(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func sequenceLen(node *yaml.Node) int {
	if node == nil || node.Kind != yaml.SequenceNode {
		return 0
	}
	return len(node.Content)
}

func automationTriggerCount(node *yaml.Node) int {
	count := sequenceLen(mapValue(node, "triggers")) + sequenceLen(mapValue(node, "trigger"))
	if triggerNode := mapValue(node, "trigger"); triggerNode != nil && triggerNode.Kind == yaml.MappingNode {
		count++
	}
	if triggerNode := mapValue(node, "triggers"); triggerNode != nil && triggerNode.Kind == yaml.MappingNode {
		count++
	}
	return count
}

func automationBlueprintMode(root *yaml.Node) string {
	modeNode := mapValue(root, "mode")
	if modeNode != nil && modeNode.Kind == yaml.ScalarNode && modeNode.Tag == "!input" {
		if mode := blueprintInputDefault(root, modeNode.Value); mode != "" {
			return mode
		}
	}
	if mode := yamlScalarValue(modeNode); mode != "" && modeNode.Tag != "!input" {
		return mode
	}
	return "single"
}

func blueprintInputDefault(root *yaml.Node, inputName string) string {
	blueprint := mapValue(root, "blueprint")
	if blueprint == nil || blueprint.Kind != yaml.MappingNode {
		return ""
	}
	inputs := mapValue(blueprint, "input")
	if inputs == nil || inputs.Kind != yaml.MappingNode {
		return ""
	}
	input := mapValue(inputs, inputName)
	if input == nil || input.Kind != yaml.MappingNode {
		return ""
	}
	return yamlScalarValue(mapValue(input, "default"))
}

func generateAutomationIndexDoc(rows []automationDocRow) string {
	var b strings.Builder
	writeGeneratedHeader(&b, "Automation Index")
	b.WriteString("This map helps agents find automation ownership, blueprint usage, and conventional test coverage.\n\n")
	b.WriteString("| Category | Automation | Alias | Mode | Triggers | Blueprint | Test |\n")
	b.WriteString("| --- | --- | --- | --- | ---: | --- | --- |\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | `%s` | %s | `%s` | %d | %s | %s |\n",
			escapeTable(row.Category),
			projectDisplayPath(row.Path),
			escapeTable(defaultString(row.Alias, row.ID)),
			escapeTable(row.Mode),
			row.Triggers,
			markdownCodeOrDash(row.Blueprint),
			markdownCodeOrDash(projectDisplayPath(row.Test)))
	}
	return b.String()
}

func generateBlueprintUsageDoc(rows []automationDocRow) string {
	byBlueprint := map[string][]automationDocRow{}
	for _, row := range rows {
		if row.Blueprint == "" {
			continue
		}
		byBlueprint[row.Blueprint] = append(byBlueprint[row.Blueprint], row)
	}
	var blueprints []string
	for bp := range byBlueprint {
		blueprints = append(blueprints, bp)
	}
	sort.Strings(blueprints)

	var b strings.Builder
	writeGeneratedHeader(&b, "Blueprint Usage")
	b.WriteString("This map shows which automation instances depend on each automation blueprint.\n\n")
	b.WriteString("| Blueprint | Uses | Automations |\n")
	b.WriteString("| --- | ---: | --- |\n")
	for _, bp := range blueprints {
		var autos []string
		for _, row := range byBlueprint[bp] {
			autos = append(autos, fmt.Sprintf("`%s`", projectDisplayPath(row.Path)))
		}
		sort.Strings(autos)
		fmt.Fprintf(&b, "| `%s` | %d | %s |\n", escapeTable(bp), len(autos), strings.Join(autos, "<br>"))
	}
	if len(blueprints) == 0 {
		b.WriteString("| _None_ | 0 | - |\n")
	}
	return b.String()
}

func generateEntityGraphDoc() string {
	entities := readReferenceEntities()
	domainCounts := map[string]int{}
	for _, entity := range entities {
		if domain := entityDomain(entity); domain != "" {
			domainCounts[domain]++
		}
	}
	references := countEntityReferences(entities)

	var domains []string
	for domain := range domainCounts {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	var b strings.Builder
	writeGeneratedHeader(&b, "Entity Graph")
	b.WriteString("This map summarizes the synced entity universe and the most referenced entities in YAML configuration.\n\n")
	b.WriteString("## Domain Counts\n\n")
	b.WriteString("| Domain | Entities |\n")
	b.WriteString("| --- | ---: |\n")
	for _, domain := range domains {
		fmt.Fprintf(&b, "| `%s` | %d |\n", domain, domainCounts[domain])
	}

	b.WriteString("\n## Most Referenced Entities\n\n")
	b.WriteString("| Entity | References |\n")
	b.WriteString("| --- | ---: |\n")
	for _, ref := range topEntityReferences(references, 40) {
		fmt.Fprintf(&b, "| `%s` | %d |\n", ref.Entity, ref.Count)
	}
	return b.String()
}

func generateHomeKitExposureDoc() (string, error) {
	matches, err := globUnderConfig("homekit/*.yaml")
	if err != nil {
		return "", err
	}
	sort.Strings(matches)

	type exposure struct {
		Bridge string
		Entity string
		Name   string
	}
	var rows []exposure
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
		for _, root := range homeKitBridgeMappings(&doc) {
			names := homeKitEntityNames(root)
			filter := mapValue(root, "filter")
			includeEntities := mapValue(root, "include_entities")
			if filter != nil && filter.Kind == yaml.MappingNode {
				includeEntities = mapValue(filter, "include_entities")
			}
			for _, entity := range yamlStringSequence(includeEntities) {
				rows = append(rows, exposure{
					Bridge: filepath.Base(path),
					Entity: entity,
					Name:   names[entity],
				})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Bridge == rows[j].Bridge {
			return rows[i].Entity < rows[j].Entity
		}
		return rows[i].Bridge < rows[j].Bridge
	})

	var b strings.Builder
	writeGeneratedHeader(&b, "HomeKit Exposure")
	b.WriteString("This map lists entities explicitly exposed through YAML-mode HomeKit bridges.\n\n")
	b.WriteString("| Bridge | Entity | HomeKit Name |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", row.Bridge, row.Entity, escapeTable(row.Name))
	}
	return b.String(), nil
}

func homeKitBridgeMappings(doc *yaml.Node) []*yaml.Node {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	switch root.Kind {
	case yaml.SequenceNode:
		var mappings []*yaml.Node
		for _, item := range root.Content {
			if item.Kind == yaml.MappingNode {
				mappings = append(mappings, item)
			}
		}
		return mappings
	case yaml.MappingNode:
		return []*yaml.Node{root}
	default:
		return nil
	}
}

func homeKitEntityNames(root *yaml.Node) map[string]string {
	names := map[string]string{}
	entityConfig := mapValue(root, "entity_config")
	if entityConfig == nil || entityConfig.Kind != yaml.MappingNode {
		return names
	}
	for i := 0; i+1 < len(entityConfig.Content); i += 2 {
		entity := entityConfig.Content[i].Value
		config := entityConfig.Content[i+1]
		if config.Kind != yaml.MappingNode {
			continue
		}
		names[entity] = yamlScalarValue(mapValue(config, "name"))
	}
	return names
}

func yamlStringSequence(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var values []string
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode {
			values = append(values, item.Value)
		}
	}
	return values
}

func readReferenceEntities() []string {
	settings, err := selectedProject()
	if err != nil {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(settings.ReferencesDir, "entity-list.txt"))
	if err != nil {
		return nil
	}
	var entities []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entities = append(entities, line)
	}
	sort.Strings(entities)
	return entities
}

type entityRefCount struct {
	Entity string
	Count  int
}

func countEntityReferences(entities []string) map[string]int {
	counts := map[string]int{}
	if len(entities) == 0 {
		return counts
	}
	entitySet := map[string]struct{}{}
	for _, entity := range entities {
		entitySet[entity] = struct{}{}
	}
	matches, _ := globUnderConfig("**/*.yaml")
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		refs, err := hayaml.ExtractReferences(content)
		if err != nil {
			continue
		}
		for _, ref := range refs.Static {
			if _, ok := entitySet[ref.Entity]; ok {
				counts[ref.Entity]++
			}
		}
	}
	return counts
}

func topEntityReferences(counts map[string]int, limit int) []entityRefCount {
	var refs []entityRefCount
	for entity, count := range counts {
		if count > 0 {
			refs = append(refs, entityRefCount{Entity: entity, Count: count})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Count == refs[j].Count {
			return refs[i].Entity < refs[j].Entity
		}
		return refs[i].Count > refs[j].Count
	})
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return refs
}

func entityDomain(entity string) string {
	if i := strings.Index(entity, "."); i >= 0 {
		return entity[:i]
	}
	return ""
}

func writeGeneratedHeader(b *strings.Builder, title string) {
	b.WriteString("# " + title + "\n\n")
	b.WriteString("Generated by `./dm docs generate`. Do not edit by hand.\n\n")
}

func generatedDocPaths(docs []generatedDoc) []string {
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		paths = append(paths, doc.Path)
	}
	sort.Strings(paths)
	return paths
}

func staleGeneratedDocIssues(paths []string) []agentIssue {
	issues := make([]agentIssue, 0, len(paths))
	for _, path := range paths {
		issues = append(issues, agentIssue{
			ID:       "generated-doc-stale",
			Severity: "error",
			File:     path,
			Summary:  "generated architecture map is missing or stale",
			Hint:     "./dm docs generate",
		})
	}
	return issues
}

func docsGenerateSummaryText(status operator.Status, summary *docsGenerateSummary) string {
	if status == operator.StatusSuccess {
		return fmt.Sprintf("generated docs are current: %d artifact(s)", len(summary.Docs))
	}
	return fmt.Sprintf("generated docs are stale: %d artifact(s)", len(summary.Changed))
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	if fallback != "" {
		return fallback
	}
	return "-"
}

func markdownCodeOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return "`" + escapeTable(value) + "`"
}

func escapeTable(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	if value == "" {
		return "-"
	}
	return value
}
