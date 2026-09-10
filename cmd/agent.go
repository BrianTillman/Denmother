package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/haverify"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	agentLintJSON       bool
	agentContextJSON    bool
	agentContextOffline bool
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent harness utilities",
	Long: `Agent harness utilities expose repository context and mechanical checks
for unattended coding agents. They are intentionally repo-local and avoid
production mutation.`,
}

var agentLintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Validate agent-facing docs, safety rules, and common automation defects",
	RunE:  runAgentLint,
}

var agentContextCmd = &cobra.Command{
	Use:   "context",
	Short: "Print a machine-readable agent starting snapshot",
	RunE:  runAgentContext,
}

func init() {
	agentLintCmd.Flags().BoolVar(&agentLintJSON, "json", false, "Emit a machine-readable JSON summary")
	agentContextCmd.Flags().BoolVar(&agentContextJSON, "json", false, "Emit a machine-readable JSON summary")

	agentContextCmd.Flags().BoolVar(&agentContextOffline, "no-schema", false, "Collect an offline baseline without Docker or HA discovery")

	agentCmd.AddCommand(agentLintCmd)
	agentCmd.AddCommand(agentContextCmd)
	rootCmd.AddCommand(agentCmd)
}

type agentIssue struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Summary  string `json:"summary"`
	Details  string `json:"details,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

type agentDocRef struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Exists bool   `json:"exists"`
}

type agentCommandRef struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Mutates     bool   `json:"mutates"`
}

type agentContext struct {
	CollectionStatus     string            `json:"collection_status"`
	VerificationStatus   string            `json:"verification_status"`
	ProjectRoot          string            `json:"project_root"`
	ConfigRoot           string            `json:"config_root"`
	Version              string            `json:"version"`
	Git                  agentGitContext   `json:"git"`
	Docs                 []agentDocRef     `json:"docs"`
	Profiles             []string          `json:"profiles"`
	Credentials          map[string]bool   `json:"credentials"`
	LocalDevAvailable    bool              `json:"local_dev_available"`
	LocalDevSource       string            `json:"local_dev_source,omitempty"`
	Baseline             map[string]any    `json:"baseline"`
	RecommendedCommands  []agentCommandRef `json:"recommended_commands"`
	ProductionGuardrails []string          `json:"production_guardrails"`
}

type agentGitContext struct {
	Available bool     `json:"available"`
	Dirty     bool     `json:"dirty"`
	Changes   []string `json:"changes,omitempty"`
}

type lintSummary struct {
	DocsChecked int
	Issues      []agentIssue
}

var requiredAgentDocs = []agentDocRef{
	{Path: "AGENTS.md", Title: "Agent map"},
	{Path: "docs/agents/README.md", Title: "Agent documentation index"},
	{Path: "docs/agents/workflows.md", Title: "Workflows"},
	{Path: "docs/agents/testing.md", Title: "Testing"},
	{Path: "docs/agents/home-assistant-domain.md", Title: "Home Assistant domain rules"},
	{Path: "docs/agents/production-safety.md", Title: "Production safety"},
	{Path: "docs/agents/framework-limitations.md", Title: "Framework limitations"},
	{Path: "docs/agents/escalation.md", Title: "Escalation boundaries"},
	{Path: "docs/agents/quality-score.md", Title: "Quality score guidance"},
	{Path: "docs/generated/automation-index.md", Title: "Generated automation index"},
	{Path: "docs/generated/blueprint-usage.md", Title: "Generated blueprint usage map"},
	{Path: "docs/generated/entity-graph.md", Title: "Generated entity graph"},
	{Path: "docs/generated/homekit-exposure.md", Title: "Generated HomeKit exposure map"},
	{Path: "docs/generated/dm-json-schema.json", Title: "Generated dm JSON schema"},
	{Path: "docs/generated/quality-score.md", Title: "Generated quality score"},
	{Path: "docs/exec-plans/active/.gitkeep", Title: "Active execution plan namespace"},
	{Path: "docs/exec-plans/completed/.gitkeep", Title: "Completed execution plan namespace"},
	{Path: "docs/exec-plans/tech-debt.md", Title: "Agent tech debt tracker"},
}

var mapLineLimits = map[string]int{
	"AGENTS.md":                 150,
	"cmd/AGENTS.md":             140,
	"ha-config/AGENTS.md":       140,
	"ha-config/tests/AGENTS.md": 140,
}

func runAgentLint(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentLintJSON, cmd.OutOrStdout())
	rt.SetProfile("lint")

	summary := runAgentLintChecks()
	step := agentLintStep(summary)
	rt.AddStep(step)

	if !agentLintJSON && len(summary.Issues) > 0 {
		for _, issue := range summary.Issues {
			location := issue.File
			if issue.Line > 0 {
				location = fmt.Sprintf("%s:%d", issue.File, issue.Line)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %s\n", location, issue.ID, issue.Summary)
			if issue.Hint != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Hint: %s\n", issue.Hint)
			}
		}
	}

	return rt.Complete(step.Status, agentLintSummaryText(step.Status, summary))
}

func runAgentContext(cmd *cobra.Command, args []string) error {
	rt := newCommandRuntime("agent", agentContextJSON, cmd.OutOrStdout())
	rt.SetProfile("context")

	context := buildAgentContext()
	status := operator.StatusSuccess
	if validation, ok := context.Baseline["validation"].(map[string]any); ok {
		if value, ok := validation["status"].(string); ok {
			status = operator.Status(value)
		}
	}

	rt.AddStep(operator.Step{
		ID:      "agent-context",
		Title:   "Collect agent context",
		Status:  status,
		Summary: "collected repository map, safety posture, credentials presence, and baseline commands",
		Details: map[string]any{
			"context": context,
		},
		Hints: []string{
			"Use `./dm check --json` for the current mutating fast-test baseline against local development Home Assistant.",
		},
	})

	return rt.Complete(status, "agent context collected")
}

func runAgentLintChecks() lintSummary {
	var summary lintSummary

	summary.Issues = append(summary.Issues, checkTrackedSecrets()...)
	summary.Issues = append(summary.Issues, checkAutomationHarnessDefects()...)
	if managedRepository() {
		summary.Issues = append(summary.Issues, checkRequiredAgentDocs()...)
		summary.Issues = append(summary.Issues, checkAgentMapSizes()...)
		summary.Issues = append(summary.Issues, checkMarkdownLinks()...)
		summary.Issues = append(summary.Issues, checkCommandDocsDrift()...)
		summary.Issues = append(summary.Issues, checkConfigReferenceDefects()...)
		summary.Issues = append(summary.Issues, checkHomeKitHarnessDefects()...)
		summary.Issues = append(summary.Issues, checkZigbee2MQTTConfigDefects()...)
		summary.Issues = append(summary.Issues, checkFanLightPairDeviceContracts()...)
		summary.Issues = append(summary.Issues, checkInovelliHousePolicySchedule()...)

	}

	if managedRepository() {
		summary.DocsChecked = len(requiredAgentDocs)
	}
	return summary
}

func checkInovelliHousePolicySchedule() []agentIssue {
	schedulerPath := configFile("automations/config/inovelli_z2m_house_policy_schedule.yaml")
	content, err := os.ReadFile(schedulerPath)
	if err != nil {
		return []agentIssue{{
			ID:       "inovelli-house-policy-scheduler-missing",
			Severity: "error",
			File:     schedulerPath,
			Summary:  "Inovelli house-policy scheduler is missing",
			Hint:     "Restore the central scheduler so device policy runs remain serialized.",
		}}
	}

	var scheduler interface{}
	if err := yaml.Unmarshal(content, &scheduler); err != nil {
		return []agentIssue{{
			ID:       "inovelli-house-policy-scheduler-invalid",
			Severity: "error",
			File:     schedulerPath,
			Summary:  fmt.Sprintf("cannot parse Inovelli house-policy scheduler: %v", err),
		}}
	}
	scheduled, ok := findStringListByKey(scheduler, "policy_automations")
	if !ok {
		return []agentIssue{{
			ID:       "inovelli-house-policy-scheduler-list-missing",
			Severity: "error",
			File:     schedulerPath,
			Summary:  "scheduler has no policy_automations list",
			Hint:     "Keep the serialized automation list in the scheduler variables block.",
		}}
	}

	expected := make(map[string]string)
	pattern := filepath.Join(configPath, "automations", "config", "**", "*.yaml")
	paths, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return []agentIssue{{ID: "inovelli-house-policy-config-scan-failed", Severity: "error", Summary: err.Error()}}
	}
	for _, path := range paths {
		automations, err := loadAgentAutomationItems(path)
		if err != nil {
			continue
		}
		for _, automation := range automations {
			blueprintPath := strings.TrimSpace(automation.UseBlueprint.Path)
			if !strings.Contains(blueprintPath, "z2m_inovelli_blue_dimmer_settings") &&
				!strings.Contains(blueprintPath, "z2m_inovelli_fan_canopy_settings") {
				continue
			}
			entityID := "automation." + strings.TrimSpace(automation.ID)
			expected[entityID] = path
		}
	}

	counts := make(map[string]int)
	for _, entityID := range scheduled {
		counts[entityID]++
	}

	var issues []agentIssue
	expectedIDs := make([]string, 0, len(expected))
	for entityID := range expected {
		expectedIDs = append(expectedIDs, entityID)
	}
	sort.Strings(expectedIDs)
	for _, entityID := range expectedIDs {
		switch counts[entityID] {
		case 0:
			issues = append(issues, agentIssue{
				ID:       "inovelli-house-policy-automation-unscheduled",
				Severity: "error",
				File:     expected[entityID],
				Summary:  fmt.Sprintf("%s is missing from the central house-policy scheduler", entityID),
				Hint:     "Add the automation once to policy_automations; do not add a per-device time trigger.",
			})
		case 1:
		default:
			issues = append(issues, agentIssue{
				ID:       "inovelli-house-policy-automation-duplicated",
				Severity: "error",
				File:     schedulerPath,
				Summary:  fmt.Sprintf("%s appears %d times in the central house-policy scheduler", entityID, counts[entityID]),
				Hint:     "Keep exactly one scheduler entry per config automation.",
			})
		}
	}
	for entityID := range counts {
		if _, exists := expected[entityID]; exists {
			continue
		}
		issues = append(issues, agentIssue{
			ID:       "inovelli-house-policy-automation-unknown",
			Severity: "error",
			File:     schedulerPath,
			Summary:  fmt.Sprintf("%s is scheduled but is not a current Blue dimmer or fan-canopy config automation", entityID),
			Hint:     "Remove stale entries or restore the matching YAML config automation.",
		})
	}
	return issues
}

func findStringListByKey(value interface{}, key string) ([]string, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if raw, exists := typed[key]; exists {
			items, ok := raw.([]interface{})
			if !ok {
				return nil, false
			}
			result := make([]string, 0, len(items))
			for _, item := range items {
				result = append(result, strings.TrimSpace(fmt.Sprintf("%v", item)))
			}
			return result, true
		}
		for _, child := range typed {
			if result, found := findStringListByKey(child, key); found {
				return result, true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if result, found := findStringListByKey(child, key); found {
				return result, true
			}
		}
	}
	return nil, false
}

func agentLintStep(summary lintSummary) operator.Step {
	status := operator.StatusSuccess
	for _, issue := range summary.Issues {
		if issue.Severity == "error" {
			status = operator.StatusFailure
			break
		}
		if issue.Severity == "warning" && status == operator.StatusSuccess {
			status = operator.StatusWarning
		}
	}

	details := map[string]any{
		"docs_checked": summary.DocsChecked,
		"issue_count":  len(summary.Issues),
	}
	if len(summary.Issues) > 0 {
		details["failures"] = summary.Issues
		details["artifacts"] = issueArtifacts(summary.Issues)
		details["next_commands"] = []string{"./dm agent lint --json"}
	}

	step := operator.Step{
		ID:      "agent-lint",
		Title:   "Lint agent harness",
		Status:  status,
		Summary: fmt.Sprintf("%d issue(s) found", len(summary.Issues)),
		Details: details,
	}
	if status == operator.StatusSuccess {
		step.Summary = "agent harness checks passed"
	}
	return step
}

func agentLintSummaryText(status operator.Status, summary lintSummary) string {
	if status == operator.StatusSuccess {
		return fmt.Sprintf("agent harness checks passed across %d required doc artifacts", summary.DocsChecked)
	}
	return fmt.Sprintf("agent harness checks found %d issue(s)", len(summary.Issues))
}

func checkRequiredAgentDocs() []agentIssue {
	var issues []agentIssue
	for _, doc := range requiredAgentDocs {
		if _, err := os.Stat(projectArtifactPath(doc.Path)); err != nil {
			issues = append(issues, agentIssue{
				ID:       "missing-agent-doc",
				Severity: "error",
				File:     doc.Path,
				Summary:  fmt.Sprintf("missing required agent artifact: %s", doc.Title),
				Hint:     "Create the artifact or update requiredAgentDocs if the harness contract changed.",
			})
		}
	}
	return issues
}

func checkAgentMapSizes() []agentIssue {
	var issues []agentIssue
	for path, limit := range mapLineLimits {
		lineCount, err := countLines(policyArtifactPath(path))
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "missing-agent-map",
				Severity: "error",
				File:     path,
				Summary:  "missing scoped agent map",
			})
			continue
		}
		if lineCount > limit {
			issues = append(issues, agentIssue{
				ID:       "agent-map-too-large",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("agent map has %d lines, limit is %d", lineCount, limit),
				Hint:     "Keep this file as a map and move durable guidance into docs/agents/.",
			})
		}
	}
	return issues
}

func checkMarkdownLinks() []agentIssue {
	paths := []string{
		"AGENTS.md",
		"cmd/AGENTS.md",
		"ha-config/AGENTS.md",
		"ha-config/tests/AGENTS.md",
	}
	for _, doc := range requiredAgentDocs {
		if strings.HasSuffix(doc.Path, ".md") {
			paths = append(paths, doc.Path)
		}
	}

	var issues []agentIssue
	for _, path := range uniqueStrings(paths) {
		content, err := os.ReadFile(policyArtifactPath(path))
		if err != nil {
			continue
		}
		issues = append(issues, brokenLinksInFile(policyArtifactPath(path), string(content))...)
	}
	return issues
}

func checkCommandDocsDrift() []agentIssue {
	var issues []agentIssue

	readme, err := os.ReadFile(projectArtifactPath("README.md"))
	if err == nil {
		for _, command := range missingRootCommandMentions(string(readme), documentedRootCommands()) {
			issues = append(issues, agentIssue{
				ID:       "command-docs-stale",
				Severity: "error",
				File:     "README.md",
				Summary:  fmt.Sprintf("README does not document registered dm command %q", command),
				Hint:     "Update the CLI reference so feature docs match `./dm --help`.",
			})
		}
	}

	agentCommands := registeredSubcommandNames(agentCmd)
	for _, path := range []string{"AGENTS.md", "cmd/AGENTS.md"} {
		content, err := os.ReadFile(projectArtifactPath(path))
		if err != nil {
			continue
		}
		for _, command := range missingAgentCommandMentions(string(content), agentCommands) {
			issues = append(issues, agentIssue{
				ID:       "agent-command-docs-stale",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("%s does not document registered agent subcommand %q", path, command),
				Hint:     "Update the agent front door whenever `dm agent --help` changes.",
			})
		}
	}

	return issues
}

func documentedRootCommands() []string {
	candidates := []string{
		"agent",
		"check",
		"dev",
		"docs",
		"observe",
		"plan",
		"trace",
		"logs",
		"sync",
		"test",
		"audit",
		"verify",
		"discover",
	}
	var commands []string
	for _, command := range candidates {
		if commandRegistered(rootCmd, command) {
			commands = append(commands, command)
		}
	}
	return commands
}

func commandRegistered(parent *cobra.Command, name string) bool {
	for _, command := range parent.Commands() {
		if command.Hidden {
			continue
		}
		if command.Name() == name {
			return true
		}
	}
	return false
}

func registeredSubcommandNames(parent *cobra.Command) []string {
	var names []string
	for _, command := range parent.Commands() {
		if command.Hidden {
			continue
		}
		names = append(names, command.Name())
	}
	sort.Strings(names)
	return names
}

func missingRootCommandMentions(content string, commands []string) []string {
	var missing []string
	for _, command := range commands {
		if containsDMCommandMention(content, command) {
			continue
		}
		missing = append(missing, command)
	}
	return missing
}

func containsDMCommandMention(content string, command string) bool {
	for _, prefix := range []string{"./dm ", "dm "} {
		if strings.Contains(content, prefix+command) {
			return true
		}
	}
	return false
}

func missingAgentCommandMentions(content string, commands []string) []string {
	var missing []string
	for _, command := range commands {
		if strings.Contains(content, "./dm agent "+command) || strings.Contains(content, "dm agent "+command) {
			continue
		}
		missing = append(missing, command)
	}
	return missing
}

var markdownLinkRe = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func brokenLinksInFile(path string, content string) []agentIssue {
	var issues []agentIssue
	baseDir := filepath.Dir(path)

	lines := strings.Split(content, "\n")
	for lineNo, line := range lines {
		matches := markdownLinkRe.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			target := strings.TrimSpace(match[1])
			if skipLinkTarget(target) {
				continue
			}

			target = strings.Trim(target, "<>")
			target = strings.Split(target, "#")[0]
			if target == "" {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Clean(filepath.Join(baseDir, target))
			}
			if _, err := os.Stat(target); err != nil {
				issues = append(issues, agentIssue{
					ID:       "broken-agent-link",
					Severity: "error",
					File:     path,
					Line:     lineNo + 1,
					Summary:  fmt.Sprintf("broken markdown link target %q", match[1]),
					Hint:     "Update the link or restore the referenced doc.",
				})
			}
		}
	}
	return issues
}

func skipLinkTarget(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "#") ||
		strings.HasPrefix(lower, "app://")
}

func checkTrackedSecrets() []agentIssue {
	files, err := gitTrackedFiles()
	if err != nil {
		return []agentIssue{{
			ID:       "git-ls-files-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not list tracked files: %v", err),
		}}
	}

	jwtRe := regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	assignmentRe := regexp.MustCompile(`(?i)(token|secret|api[_-]?key|password)["']?\s*[:=]\s*["']?([A-Za-z0-9_./+=-]{48,})`)

	var issues []agentIssue
	for _, file := range files {
		if skipSecretScan(projectDisplayPath(file)) {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil || bytes.IndexByte(content, 0) >= 0 {
			continue
		}
		lines := strings.Split(string(content), "\n")
		for idx, line := range lines {
			if jwtRe.MatchString(line) || assignmentRe.MatchString(line) {
				issues = append(issues, agentIssue{
					ID:       "tracked-secret-like-value",
					Severity: "error",
					File:     file,
					Line:     idx + 1,
					Summary:  "tracked file contains a token-like literal",
					Hint:     "Move real credentials to .env/.env.local or local-only settings, and keep examples as placeholders.",
				})
			}
		}
	}
	return issues
}

func skipSecretScan(path string) bool {
	switch path {
	case ".devcontainer/storage-template/auth",
		".devcontainer/storage-template/auth_provider.homeassistant":
		return true
	default:
		return strings.HasPrefix(path, "docs/reference/entity-list.txt")
	}
}

func checkAutomationHarnessDefects() []agentIssue {
	var matches []string
	for _, pattern := range []string{"automations/**/*.yaml", "blueprints/**/*.yaml"} {
		found, err := globUnderConfig(pattern)
		if err != nil {
			return []agentIssue{{
				ID:       "automation-scan-failed",
				Severity: "error",
				Summary:  fmt.Sprintf("could not discover automation YAML files: %v", err),
			}}
		}
		matches = append(matches, found...)
	}

	var issues []agentIssue
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "automation-read-failed",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("could not read automation file: %v", err),
			})
			continue
		}

		var node yaml.Node
		if err := yaml.Unmarshal(content, &node); err != nil {
			continue
		}

		issues = append(issues, lintNestedZHAEventFilters(path, &node)...)
		issues = append(issues, lintBareConditionInThen(path, &node)...)
		issues = append(issues, lintSingleModeExclusivity(path, &node)...)
		issues = append(issues, lintTemplateBooleanLiterals(path, string(content))...)
		issues = append(issues, lintRawStatesEntityState(path, string(content))...)
		issues = append(issues, lintOvernightTimeWindows(path, &node)...)
		if strings.Contains(filepath.ToSlash(path), "/blueprints/") {
			issues = append(issues, lintBlueprintMetadata(path, &node)...)
		}
	}
	return issues
}

func checkHomeKitHarnessDefects() []agentIssue {
	matches, err := globUnderConfig("homekit/**/*.yaml")
	if err != nil {
		return []agentIssue{{
			ID:       "homekit-scan-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not discover HomeKit YAML files: %v", err),
		}}
	}

	var issues []agentIssue
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "homekit-read-failed",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("could not read HomeKit bridge: %v", err),
			})
			continue
		}
		var node yaml.Node
		if err := yaml.Unmarshal(content, &node); err != nil {
			continue
		}
		walkYAMLMaps(&node, func(m *yaml.Node) {
			if includeDomains := mapValue(m, "include_domains"); includeDomains != nil {
				issues = append(issues, agentIssue{
					ID:       "homekit-include-domains",
					Severity: "error",
					File:     path,
					Line:     includeDomains.Line,
					Summary:  "HomeKit bridge uses include_domains instead of explicit include_entities",
					Hint:     "Expose entities explicitly to avoid accidental HomeKit exposure.",
				})
			}
		})
	}
	return issues
}

func checkZigbee2MQTTConfigDefects() []agentIssue {
	path := configFile("zigbee2mqtt/configuration.yaml")
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []agentIssue{{
			ID:       "z2m-config-read-failed",
			Severity: "error",
			File:     path,
			Summary:  fmt.Sprintf("could not read Zigbee2MQTT config: %v", err),
		}}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var issues []agentIssue
	issues = append(issues, lintZigbee2MQTTFilteredUpdateAttribute(path, &doc)...)
	issues = append(issues, lintZigbee2MQTTInovelliNoisyAttributeFilters(path, &doc)...)
	issues = append(issues, lintZigbee2MQTTInovelliNoisyDiscoveryOverrides(path, &doc)...)
	return issues
}

type fanLightPairConsumer struct {
	Path    string
	Alias   string
	Z2MName string
}

func checkFanLightPairDeviceContracts() []agentIssue {
	consumers, err := discoverFanLightPairConsumers()
	if err != nil {
		return []agentIssue{{
			ID:       "fan-light-pair-contract-scan-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not inspect fan/light pair consumers: %v", err),
		}}
	}
	if len(consumers) == 0 {
		return nil
	}

	configFiles, err := globUnderConfig("automations/config/**/*.yaml")
	if err != nil {
		return []agentIssue{{
			ID:       "fan-light-pair-contract-scan-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not discover config automations: %v", err),
		}}
	}

	devices, err := resolveVerifyDevices(configFiles)
	if err != nil {
		return []agentIssue{{
			ID:       "fan-light-pair-contract-resolve-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not resolve config automation expectations: %v", err),
			Hint:     "Run `./dm verify --dry-run --json` for the exact config automation parse failure.",
		}}
	}

	byZ2MName := make(map[string][]resolvedVerifyDevice)
	for _, device := range devices {
		z2mName := strings.TrimSpace(device.z2mName)
		if device.skipped || z2mName == "" {
			continue
		}
		byZ2MName[z2mName] = append(byZ2MName[z2mName], device)
	}

	var issues []agentIssue
	for _, consumer := range consumers {
		if consumer.Z2MName == "" {
			issues = append(issues, fanLightPairContractIssue(consumer, "fan-light-pair-missing-z2m-name", "fan/light pair automation does not declare z2m_friendly_name", "Set z2m_friendly_name so the matching config automation and MQTT writes can be verified."))
			continue
		}

		matches := byZ2MName[consumer.Z2MName]
		if len(matches) == 0 {
			issues = append(issues, fanLightPairContractIssue(consumer, "fan-light-pair-config-missing", fmt.Sprintf("fan/light pair has no matching config automation for %q", consumer.Z2MName), "Add a ha-config/automations/config/* settings automation with the same z2m_friendly_name."))
			continue
		}
		if len(matches) > 1 {
			issues = append(issues, fanLightPairContractIssue(consumer, "fan-light-pair-config-duplicate", fmt.Sprintf("fan/light pair has %d matching config automations for %q", len(matches), consumer.Z2MName), fmt.Sprintf("Keep exactly one matching device settings automation. Matches: %s", strings.Join(fanLightPairDevicePaths(matches), ", "))))
			continue
		}

		issues = append(issues, lintFanLightPairDeviceContract(consumer, matches[0])...)
	}

	return issues
}

func fanLightPairDevicePaths(devices []resolvedVerifyDevice) []string {
	paths := make([]string, 0, len(devices))
	for _, device := range devices {
		paths = append(paths, device.sourceFile)
	}
	sort.Strings(paths)
	return paths
}

func discoverFanLightPairConsumers() ([]fanLightPairConsumer, error) {
	settings, err := selectedProject()
	if err != nil {
		return nil, err
	}
	blueprintPath := strings.TrimSpace(settings.FanLightPairBlueprint)
	if blueprintPath == "" {
		return nil, nil
	}
	matches, err := globUnderConfig("automations/**/*.yaml")
	if err != nil {
		return nil, err
	}

	var consumers []fanLightPairConsumer
	for _, path := range matches {
		automations, err := loadAgentAutomationItems(path)
		if err != nil {
			return nil, err
		}
		for _, automation := range automations {
			if strings.TrimSpace(automation.UseBlueprint.Path) != blueprintPath {
				continue
			}
			z2mName, _ := automation.UseBlueprint.Input["z2m_friendly_name"].(string)
			consumers = append(consumers, fanLightPairConsumer{
				Path:    path,
				Alias:   automation.Alias,
				Z2MName: strings.TrimSpace(z2mName),
			})
		}
	}
	return consumers, nil
}

func loadAgentAutomationItems(path string) ([]haverify.ConfigAutomation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var automations []haverify.ConfigAutomation
	if err := yaml.Unmarshal(content, &automations); err != nil {
		return nil, err
	}
	return automations, nil
}

func lintFanLightPairDeviceContract(consumer fanLightPairConsumer, device resolvedVerifyDevice) []agentIssue {
	var issues []agentIssue

	issues = append(issues, requireFanLightPairExpectation(consumer, device, "buttonDelay", "", "buttonDelay must be nonzero so Z2M emits multi-tap action events", func(value string) bool {
		value = strings.TrimSpace(strings.ToLower(value))
		return value != "" && value != "0" && value != "0ms"
	})...)
	issues = append(issues, requireFanLightPairExpectationValue(consumer, device, "outputMode", "Dimmer")...)
	issues = append(issues, requireFanLightPairExpectationValue(consumer, device, "localProtection", "Disabled")...)
	issues = append(issues, requireFanLightPairExpectationValue(consumer, device, "fanControlMode", "Disabled")...)

	issues = append(issues, requireFanLightPairExpectationValueIfPresent(consumer, device, "doubleTapUpToParam55", "Disabled")...)
	issues = append(issues, requireFanLightPairExpectationValueIfPresent(consumer, device, "doubleTapDownToParam56", "Disabled")...)
	issues = append(issues, requireFanLightPairExpectationValueIfPresent(consumer, device, "singleTapBehavior", "Old Behavior")...)

	return issues
}

func requireFanLightPairExpectationValue(consumer fanLightPairConsumer, device resolvedVerifyDevice, param string, want string) []agentIssue {
	return requireFanLightPairExpectation(consumer, device, param, want, fmt.Sprintf("%s must be %q", param, want), func(value string) bool {
		return strings.TrimSpace(value) == want
	})
}

func requireFanLightPairExpectationValueIfPresent(consumer fanLightPairConsumer, device resolvedVerifyDevice, param string, want string) []agentIssue {
	value, ok := expectationValue(device.expects, param)
	if !ok {
		return nil
	}
	if strings.TrimSpace(value) == want {
		return nil
	}
	return []agentIssue{fanLightPairContractIssue(consumer, "fan-light-pair-config-param-mismatch", fmt.Sprintf("matching config automation expects %s=%q for %q, want %q", param, value, consumer.Z2MName, want), "Update the device config automation so physical device behavior matches the fan/light pair blueprint contract.")}
}

func requireFanLightPairExpectation(consumer fanLightPairConsumer, device resolvedVerifyDevice, param string, want string, summary string, valid func(string) bool) []agentIssue {
	value, ok := expectationValue(device.expects, param)
	if !ok {
		return []agentIssue{fanLightPairContractIssue(consumer, "fan-light-pair-config-param-missing", fmt.Sprintf("matching config automation does not verify %s for %q", param, consumer.Z2MName), "Update the device settings blueprint/config automation so this parameter is written and read back by `dm verify`.")}
	}
	if valid(value) {
		return nil
	}

	wantText := want
	if wantText == "" {
		wantText = summary
	}
	return []agentIssue{fanLightPairContractIssue(consumer, "fan-light-pair-config-param-mismatch", fmt.Sprintf("matching config automation expects %s=%q for %q, want %s", param, value, consumer.Z2MName, wantText), "Update the device config automation so physical device behavior matches the fan/light pair blueprint contract.")}
}

func expectationValue(expectations []haverify.ParamExpectation, param string) (string, bool) {
	for _, expectation := range expectations {
		if expectation.MQTTParam == param {
			return strings.TrimSpace(expectation.Value), true
		}
	}
	return "", false
}

func fanLightPairContractIssue(consumer fanLightPairConsumer, id string, summary string, hint string) agentIssue {
	if hint == "" {
		hint = "Run `./dm verify --dry-run --json` to inspect resolved device parameter expectations."
	}
	return agentIssue{
		ID:       id,
		Severity: "error",
		File:     consumer.Path,
		Summary:  summary,
		Details:  consumer.Alias,
		Hint:     hint,
	}
}

func lintZigbee2MQTTFilteredUpdateAttribute(path string, node *yaml.Node) []agentIssue {
	root := yamlDocumentRoot(node)
	devices := mapValue(root, "devices")
	if devices == nil || devices.Kind != yaml.MappingNode {
		return nil
	}

	var issues []agentIssue
	for i := 0; i+1 < len(devices.Content); i += 2 {
		device := devices.Content[i+1]
		if device.Kind != yaml.MappingNode {
			continue
		}
		filtered := mapValue(device, "filtered_attributes")
		if filtered == nil || filtered.Kind != yaml.SequenceNode {
			continue
		}
		name := yamlScalarValue(mapValue(device, "friendly_name"))
		if name == "" {
			name = devices.Content[i].Value
		}
		for _, item := range filtered.Content {
			if strings.TrimSpace(yamlScalarValue(item)) != "^update$" {
				continue
			}
			issues = append(issues, agentIssue{
				ID:       "z2m-filtered-update-attribute",
				Severity: "error",
				File:     path,
				Line:     item.Line,
				Summary:  fmt.Sprintf("Zigbee2MQTT device %q filters the update payload object", name),
				Hint:     "Remove `^update$` from filtered_attributes; issue #9 tracks that Z2M-generated OTA update entities need value_json.update fields.",
			})
		}
	}
	return issues
}

func lintZigbee2MQTTInovelliNoisyAttributeFilters(path string, node *yaml.Node) []agentIssue {
	root := yamlDocumentRoot(node)
	devices := mapValue(root, "devices")
	if devices == nil || devices.Kind != yaml.MappingNode {
		return nil
	}

	required := []string{
		"^defaultLed[1-7]ColorWhenOff$",
		"^defaultLed[1-7]IntensityWhenOn$",
		"^defaultLed[1-7]IntensityWhenOff$",
		"^mmWaveVersion$",
	}

	var issues []agentIssue
	for i := 0; i+1 < len(devices.Content); i += 2 {
		device := devices.Content[i+1]
		if device.Kind != yaml.MappingNode {
			continue
		}
		filtered := mapValue(device, "filtered_attributes")
		if filtered == nil || filtered.Kind != yaml.SequenceNode || !yamlSequenceContainsScalar(filtered, "^mmwave_targets$") {
			continue
		}

		var missing []string
		for _, pattern := range required {
			if !yamlSequenceContainsScalar(filtered, pattern) {
				missing = append(missing, pattern)
			}
		}
		if len(missing) == 0 {
			continue
		}

		name := yamlScalarValue(mapValue(device, "friendly_name"))
		if name == "" {
			name = devices.Content[i].Value
		}
		issues = append(issues, agentIssue{
			ID:       "z2m-inovelli-noisy-attribute-filter-missing",
			Severity: "warning",
			File:     path,
			Line:     filtered.Line,
			Summary:  fmt.Sprintf("Zigbee2MQTT Inovelli device %q is missing filters for optional noisy attributes: %s", name, strings.Join(missing, ", ")),
			Hint:     "Add the missing filtered_attributes patterns to keep optional Inovelli fields out of normal state payloads.",
		})
	}
	return issues
}

func lintZigbee2MQTTInovelliNoisyDiscoveryOverrides(path string, node *yaml.Node) []agentIssue {
	root := yamlDocumentRoot(node)
	devices := mapValue(root, "devices")
	if devices == nil || devices.Kind != yaml.MappingNode {
		return nil
	}

	deviceOptions := mapValue(root, "device_options")
	defaultHomeAssistant := mapValue(deviceOptions, "homeassistant")
	required := inovelliOptionalDiscoveryProperties()

	var issues []agentIssue
	for i := 0; i+1 < len(devices.Content); i += 2 {
		device := devices.Content[i+1]
		if device.Kind != yaml.MappingNode {
			continue
		}
		filtered := mapValue(device, "filtered_attributes")
		if filtered == nil || filtered.Kind != yaml.SequenceNode || !yamlSequenceContainsScalar(filtered, "^mmwave_targets$") {
			continue
		}

		homeAssistant := mapValue(device, "homeassistant")
		if homeAssistant == nil {
			homeAssistant = defaultHomeAssistant
		}
		if homeAssistant != nil && homeAssistant.Kind == yaml.ScalarNode &&
			(homeAssistant.Tag == "!!null" || strings.EqualFold(strings.TrimSpace(homeAssistant.Value), "false")) {
			continue
		}

		var missing []string
		for _, property := range required {
			override := yamlMapValueIncludingMerge(homeAssistant, property)
			if override == nil || override.Tag != "!!null" {
				missing = append(missing, property)
			}
		}
		if len(missing) == 0 {
			continue
		}

		name := yamlScalarValue(mapValue(device, "friendly_name"))
		if name == "" {
			name = devices.Content[i].Value
		}
		line := device.Line
		if homeAssistant != nil {
			line = homeAssistant.Line
		}
		issues = append(issues, agentIssue{
			ID:       "z2m-inovelli-noisy-discovery-override-missing",
			Severity: "warning",
			File:     path,
			Line:     line,
			Summary:  fmt.Sprintf("Zigbee2MQTT Inovelli device %q still discovers optional noisy attributes: %s", name, strings.Join(missing, ", ")),
			Hint:     "Set the missing Home Assistant discovery overrides to null; filtered_attributes changes state payloads but does not remove discovery templates.",
		})
	}
	return issues
}

func inovelliOptionalDiscoveryProperties() []string {
	properties := make([]string, 0, 22)
	for led := 1; led <= 7; led++ {
		properties = append(properties,
			fmt.Sprintf("defaultLed%dColorWhenOff", led),
			fmt.Sprintf("defaultLed%dIntensityWhenOff", led),
			fmt.Sprintf("defaultLed%dIntensityWhenOn", led),
		)
	}
	return append(properties, "mmWaveVersion")
}

func lintNestedZHAEventFilters(path string, node *yaml.Node) []agentIssue {
	var issues []agentIssue
	walkYAMLMaps(node, func(m *yaml.Node) {
		if yamlScalarValue(mapValue(m, "event_type")) != "zha_event" {
			return
		}
		eventData := mapValue(m, "event_data")
		if eventData == nil || eventData.Kind != yaml.MappingNode {
			return
		}
		for _, nestedKey := range []string{"args", "params"} {
			if nested := mapValue(eventData, nestedKey); nested != nil && nested.Kind == yaml.MappingNode {
				issues = append(issues, agentIssue{
					ID:       "nested-zha-event-filter",
					Severity: "error",
					File:     path,
					Line:     nested.Line,
					Summary:  fmt.Sprintf("zha_event trigger filters nested event_data.%s, which Home Assistant ignores", nestedKey),
					Hint:     "Filter on top-level command/device_id fields and inspect nested params inside variables or conditions.",
				})
			}
		}
	})
	return issues
}

func lintBareConditionInThen(path string, node *yaml.Node) []agentIssue {
	var issues []agentIssue
	walkYAMLMaps(node, func(m *yaml.Node) {
		thenNode := mapValue(m, "then")
		if thenNode == nil || thenNode.Kind != yaml.SequenceNode {
			return
		}
		for _, item := range thenNode.Content {
			if item.Kind != yaml.MappingNode || mapValue(item, "condition") == nil {
				continue
			}
			if len(item.Content) <= 2 {
				issues = append(issues, agentIssue{
					ID:       "bare-condition-in-then",
					Severity: "error",
					File:     path,
					Line:     item.Line,
					Summary:  "bare condition inside then block does not express an explicit early exit",
					Hint:     "Use a nested if/then with a stop action when the sequence should halt.",
				})
			}
		}
	})
	return issues
}

func lintSingleModeExclusivity(path string, node *yaml.Node) []agentIssue {
	var issues []agentIssue
	walkYAMLMaps(node, func(m *yaml.Node) {
		if yamlScalarValue(mapValue(m, "mode")) != "single" {
			return
		}
		text := strings.ToLower(strings.Join([]string{
			yamlScalarValue(mapValue(m, "id")),
			yamlScalarValue(mapValue(m, "alias")),
			yamlScalarValue(mapValue(m, "description")),
		}, " "))
		if strings.Contains(text, "exclusiv") || strings.Contains(text, "mutual") {
			issues = append(issues, agentIssue{
				ID:       "single-mode-exclusivity",
				Severity: "error",
				File:     path,
				Line:     mapValue(m, "mode").Line,
				Summary:  "exclusivity automation uses mode: single",
				Hint:     "Use mode: restart so rapid state changes always converge on the latest trigger.",
			})
		}
	})
	return issues
}

func lintTemplateBooleanLiterals(path string, content string) []agentIssue {
	var issues []agentIssue
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "false" && trimmed != "true" {
			continue
		}
		if recentTemplateControl(lines, idx) {
			issues = append(issues, agentIssue{
				ID:       "template-string-boolean",
				Severity: "error",
				File:     path,
				Line:     idx + 1,
				Summary:  fmt.Sprintf("template emits bare %q, likely as a string", trimmed),
				Hint:     fmt.Sprintf("Use `{{ %s }}` when a Home Assistant template must return a boolean.", trimmed),
			})
		}
	}
	return issues
}

var rawStatesEntityStateRe = regexp.MustCompile(`states\.[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+\.state`)

func lintRawStatesEntityState(path string, content string) []agentIssue {
	var issues []agentIssue
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		if rawStatesEntityStateRe.MatchString(line) {
			issues = append(issues, agentIssue{
				ID:       "raw-states-entity-state",
				Severity: "error",
				File:     path,
				Line:     idx + 1,
				Summary:  "template uses raw states.domain.object.state access",
				Hint:     "Use `states('entity_id')` or `is_state('entity_id', 'value')` so missing entities are handled predictably.",
			})
		}
	}
	return issues
}

func lintOvernightTimeWindows(path string, node *yaml.Node) []agentIssue {
	var issues []agentIssue
	walkYAMLMaps(node, func(m *yaml.Node) {
		if yamlScalarValue(mapValue(m, "condition")) != "time" {
			return
		}
		afterNode := mapValue(m, "after")
		beforeNode := mapValue(m, "before")
		after, afterOK := parseClockTime(yamlScalarValue(afterNode))
		before, beforeOK := parseClockTime(yamlScalarValue(beforeNode))
		if !afterOK || !beforeOK || !after.After(before) {
			return
		}
		issues = append(issues, agentIssue{
			ID:       "overnight-time-window",
			Severity: "warning",
			File:     path,
			Line:     m.Line,
			Summary:  "time condition crosses midnight without explicit wraparound structure",
			Hint:     "Use an explicit OR/wraparound condition so agents and reviewers can see the overnight behavior.",
		})
	})
	return issues
}

func lintBlueprintMetadata(path string, node *yaml.Node) []agentIssue {
	root := node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return []agentIssue{{
			ID:       "blueprint-invalid-root",
			Severity: "error",
			File:     path,
			Summary:  "blueprint YAML root is not a mapping",
			Hint:     "Declare a top-level blueprint mapping with name and domain metadata.",
		}}
	}
	blueprint := mapValue(root, "blueprint")
	if blueprint == nil || blueprint.Kind != yaml.MappingNode {
		return []agentIssue{{
			ID:       "blueprint-missing-metadata",
			Severity: "error",
			File:     path,
			Line:     root.Line,
			Summary:  "automation blueprint is missing top-level blueprint metadata",
			Hint:     "Add blueprint.name and blueprint.domain: automation.",
		}}
	}
	var issues []agentIssue
	if yamlScalarValue(mapValue(blueprint, "name")) == "" {
		issues = append(issues, agentIssue{
			ID:       "blueprint-missing-name",
			Severity: "error",
			File:     path,
			Line:     blueprint.Line,
			Summary:  "automation blueprint is missing blueprint.name",
			Hint:     "Add a stable human-readable blueprint name.",
		})
	}
	if domain := yamlScalarValue(mapValue(blueprint, "domain")); domain != "automation" {
		issues = append(issues, agentIssue{
			ID:       "blueprint-domain-not-automation",
			Severity: "error",
			File:     path,
			Line:     blueprint.Line,
			Summary:  "automation blueprint does not declare blueprint.domain: automation",
			Hint:     "Set blueprint.domain to automation for files under ha-config/blueprints/automation/.",
		})
	}
	return issues
}

type automationBlueprintUse struct {
	Path string
	File string
	Line int
}

func checkConfigReferenceDefects() []agentIssue {
	var issues []agentIssue
	issues = append(issues, checkLovelaceDashboardFiles()...)

	blueprints, blueprintIssues := localAutomationBlueprints()
	issues = append(issues, blueprintIssues...)
	uses, useIssues := automationBlueprintUses()
	issues = append(issues, useIssues...)
	if len(blueprintIssues) == 0 && len(useIssues) == 0 {
		issues = append(issues, lintAutomationBlueprintReferences(blueprints, uses)...)
		issues = append(issues, checkLocalBlueprintDocRefs(blueprints)...)
	}
	return issues
}

func checkLovelaceDashboardFiles() []agentIssue {
	var issues []agentIssue
	for _, path := range []string{configFile("configuration.yaml"), projectArtifactPath("docs/config-k8s.yaml")} {
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "lovelace-dashboard-read-failed",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("could not read Lovelace dashboard config: %v", err),
			})
			continue
		}

		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}
		root := yamlDocumentRoot(&doc)
		lovelace := mapValue(root, "lovelace")
		dashboards := mapValue(lovelace, "dashboards")
		if dashboards == nil || dashboards.Kind != yaml.MappingNode {
			continue
		}

		for i := 0; i+1 < len(dashboards.Content); i += 2 {
			dashboard := dashboards.Content[i+1]
			if dashboard.Kind != yaml.MappingNode {
				continue
			}
			filename := mapValue(dashboard, "filename")
			target := resolveHAConfigPath(yamlScalarValue(filename))
			if target == "" || fileExists(target) {
				continue
			}
			issues = append(issues, agentIssue{
				ID:       "lovelace-dashboard-missing-file",
				Severity: "error",
				File:     path,
				Line:     filename.Line,
				Summary:  fmt.Sprintf("Lovelace dashboard references missing YAML file %q", yamlScalarValue(filename)),
				Hint:     "Create the dashboard YAML file or remove the dashboard registration.",
			})
		}
	}
	return issues
}

func localAutomationBlueprints() (map[string]string, []agentIssue) {
	blueprints := make(map[string]string)
	var issues []agentIssue
	for _, pattern := range []string{"blueprints/automation/**/*.yaml", "blueprints/automation/**/*.yml"} {
		matches, err := globUnderConfig(pattern)
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "automation-blueprint-scan-failed",
				Severity: "error",
				Summary:  fmt.Sprintf("could not discover automation blueprints: %v", err),
			})
			continue
		}
		for _, path := range matches {
			rel := strings.TrimPrefix(relativeConfigPath(path), "blueprints/automation/")
			blueprints[filepath.ToSlash(rel)] = path
		}
	}
	return blueprints, issues
}

func automationBlueprintUses() ([]automationBlueprintUse, []agentIssue) {
	matches, err := globUnderConfig("automations/**/*.yaml")
	if err != nil {
		return nil, []agentIssue{{
			ID:       "automation-blueprint-use-scan-failed",
			Severity: "error",
			Summary:  fmt.Sprintf("could not discover automation files for blueprint references: %v", err),
		}}
	}

	var uses []automationBlueprintUse
	var issues []agentIssue
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, agentIssue{
				ID:       "automation-blueprint-use-read-failed",
				Severity: "error",
				File:     path,
				Summary:  fmt.Sprintf("could not read automation file for blueprint references: %v", err),
			})
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			continue
		}
		walkYAMLMaps(&doc, func(m *yaml.Node) {
			useBlueprint := mapValue(m, "use_blueprint")
			if useBlueprint == nil || useBlueprint.Kind != yaml.MappingNode {
				return
			}
			pathNode := mapValue(useBlueprint, "path")
			blueprintPath := normalizeAutomationBlueprintPath(yamlScalarValue(pathNode))
			if blueprintPath == "" {
				return
			}
			uses = append(uses, automationBlueprintUse{
				Path: blueprintPath,
				File: path,
				Line: pathNode.Line,
			})
		})
	}
	return uses, issues
}

func lintAutomationBlueprintReferences(blueprints map[string]string, uses []automationBlueprintUse) []agentIssue {
	used := make(map[string]bool)
	var issues []agentIssue
	for _, use := range uses {
		used[use.Path] = true
		if _, ok := blueprints[use.Path]; ok {
			continue
		}
		issues = append(issues, agentIssue{
			ID:       "automation-blueprint-missing-file",
			Severity: "error",
			File:     use.File,
			Line:     use.Line,
			Summary:  fmt.Sprintf("automation uses missing blueprint %q", use.Path),
			Hint:     "Restore the blueprint under ha-config/blueprints/automation/ or update the automation to a real local blueprint.",
		})
	}

	var unused []string
	for rel := range blueprints {
		if !used[rel] {
			unused = append(unused, rel)
		}
	}
	sort.Strings(unused)
	for _, rel := range unused {
		issues = append(issues, agentIssue{
			ID:       "automation-blueprint-unused",
			Severity: "warning",
			File:     blueprints[rel],
			Summary:  fmt.Sprintf("automation blueprint %q has no local use_blueprint consumers", rel),
			Hint:     "Add an automation consumer, move the file to docs/examples, or remove it if obsolete.",
		})
	}
	return issues
}

func checkLocalBlueprintDocRefs(blueprints map[string]string) []agentIssue {
	// Full blueprint paths are unambiguous. Short paths are recognized only for
	// namespaces present in this project, avoiding unrelated YAML links.
	patterns := []string{`(?:ha-config/)?blueprints/automation/([A-Za-z0-9_./-]+\.ya?ml)`}
	namespaces := make(map[string]bool)
	for name := range blueprints {
		if namespace, _, ok := strings.Cut(name, "/"); ok {
			namespaces[namespace] = true
		}
	}
	for namespace := range namespaces {
		patterns = append(patterns, `(`+regexp.QuoteMeta(namespace)+`/[A-Za-z0-9_./-]+\.ya?ml)`)
	}
	sort.Strings(patterns)
	localBlueprintDocRefRe := regexp.MustCompile(strings.Join(patterns, "|"))
	var docs []string
	for _, scope := range []struct{ root, pattern string }{
		{selectedProjectRoot(), "docs/**/*.md"},
		{configFile("."), "**/*.md"},
	} {
		matches, err := doublestar.Glob(os.DirFS(scope.root), scope.pattern)
		if err != nil {
			return []agentIssue{{
				ID: "blueprint-doc-scan-failed", Severity: "error",
				Summary: fmt.Sprintf("could not discover markdown files for blueprint references: %v", err),
			}}
		}
		for _, match := range matches {
			docs = append(docs, filepath.Join(scope.root, filepath.FromSlash(match)))
		}
	}

	var issues []agentIssue
	for _, path := range uniqueStrings(docs) {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		for idx, line := range lines {
			matches := localBlueprintDocRefRe.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				var rel string
				for _, group := range match[1:] {
					if group != "" {
						rel = normalizeAutomationBlueprintPath(group)
						break
					}
				}
				if _, ok := blueprints[rel]; ok {
					continue
				}
				issues = append(issues, agentIssue{
					ID:       "blueprint-doc-missing-file",
					Severity: "error",
					File:     path,
					Line:     idx + 1,
					Summary:  fmt.Sprintf("documentation references missing local blueprint %q", rel),
					Hint:     "Restore the blueprint or update the documentation to a real local blueprint.",
				})
			}
		}
	}
	return issues
}

func resolveHAConfigPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Trim(value, `"'`)
	value = strings.TrimPrefix(value, "/config/")
	value = strings.TrimPrefix(value, "config/")
	value = strings.TrimPrefix(value, "ha-config/")
	return configFile(value)
}

func normalizeAutomationBlueprintPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Trim(value, `"'`)
	for _, prefix := range []string{
		"/config/blueprints/automation/",
		"config/blueprints/automation/",
		"ha-config/blueprints/automation/",
		"blueprints/automation/",
	} {
		value = strings.TrimPrefix(value, prefix)
	}
	return filepath.ToSlash(filepath.Clean(value))
}

func parseClockTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func recentTemplateControl(lines []string, idx int) bool {
	start := idx - 8
	if start < 0 {
		start = 0
	}
	for i := start; i < idx; i++ {
		line := lines[i]
		if strings.Contains(line, "{% if") || strings.Contains(line, "{% elif") || strings.Contains(line, "{% else") {
			return true
		}
	}
	return false
}

func buildAgentContext() agentContext {
	git := agentGitContext{Changes: gitStatusShort()}
	git.Available = projectGitCommand("rev-parse", "--git-dir").Run() == nil
	git.Dirty = len(git.Changes) > 0

	docs := make([]agentDocRef, 0, len(requiredAgentDocs))
	if !managedRepository() {
		for _, path := range []string{"AGENTS.md", "README.md", ".denmother.yaml"} {
			full := projectArtifactPath(path)
			if fileExists(full) {
				docs = append(docs, agentDocRef{Path: full, Title: path, Exists: true})
			}
		}
	}
	for _, doc := range requiredAgentDocs {
		if !managedRepository() {
			break
		}
		doc.Path = projectArtifactPath(doc.Path)
		doc.Exists = fileExists(doc.Path)
		docs = append(docs, doc)
	}

	var validation validationSummary
	if agentContextOffline {
		validation = runSelectedValidation(configPath, true, []string{"yaml", "config", "guard", "entities"})
		validation.IncompleteChecks = append(validation.IncompleteChecks, "schema")
	} else {
		validation = runValidationSuite(configPath, true)
	}
	validationStep := validation.Step()

	baseline := map[string]any{
		"validation": map[string]any{
			"status":            string(validationStep.Status),
			"summary":           validationStep.Summary,
			"failed_checks":     validation.FailedChecks,
			"incomplete_checks": validation.IncompleteChecks,
		},
		"fast_tests": map[string]any{
			"status":  "not_run",
			"reason":  "fast tests mutate the local development Home Assistant instance",
			"command": "./dm check --ensure-dev --json",
		},
	}

	localDevAvailable := false
	localDevSource := ""
	if localConfig, err := agentContextLocalConfig(); err == nil {
		localDevAvailable = true
		localDevSource = localConfig.Source
	}

	return agentContext{
		CollectionStatus: "success", VerificationStatus: string(validationStep.Status), ProjectRoot: selectedProjectRoot(), ConfigRoot: configFile(""), Version: version,
		Git:               git,
		Docs:              docs,
		Profiles:          []string{"local", "dev", "prod"},
		Credentials:       credentialPresence(),
		LocalDevAvailable: localDevAvailable,
		LocalDevSource:    localDevSource,
		Baseline:          baseline,
		RecommendedCommands: []agentCommandRef{
			{Command: "./dm agent next --json", Description: "Choose the next best agent action from current repo state", Mutates: false},
			{Command: "./dm agent tasks --json", Description: "Emit ranked, agent-executable cleanup tasks with acceptance criteria", Mutates: false},
			{Command: "./dm agent review --json", Description: "Review the current diff for tests, production risk, docs drift, and next commands", Mutates: false},
			{Command: "./dm agent lint --json", Description: "Validate agent-facing docs, secret hygiene, and common automation defects", Mutates: false},
			{Command: "./dm agent quality --json", Description: "Report the static quality score and cleanup targets for agent work", Mutates: false},
			{Command: "./dm docs generate --check --json", Description: "Verify generated architecture maps are current", Mutates: false},
			{Command: "./dm observe <automation_entity> --json", Description: "Correlate read-only traces, logs, and local config for one automation", Mutates: false},
			{Command: "./dm trace <automation_entity> --json", Description: "Inspect read-only Home Assistant automation traces", Mutates: false},
			{Command: "./dm logs --json", Description: "Inspect read-only Home Assistant logbook entries", Mutates: false},
			{Command: "./dm plan status --json", Description: "List active and completed execution plans", Mutates: false},
			{Command: "./dm test plan --json", Description: "Plan the narrowest useful automation test run from the current diff", Mutates: false},
			{Command: "./dm test --changed --json", Description: "Run automation tests impacted by the current diff", Mutates: true},
			{Command: "./dm test doctor --compact --json", Description: "Inspect grouped static automation-test hygiene repair targets", Mutates: false},
			{Command: "./dm test doctor --json", Description: "Inspect static automation-test hygiene and repair targets", Mutates: false},
			{Command: "./dm dev status --json", Description: "Inspect the worktree-isolated local HA environment", Mutates: false},
			{Command: "./dm dev up --json", Description: "Start an isolated local Home Assistant environment for this worktree", Mutates: true},
			{Command: "./dm dev dashboard DASHBOARD --json", Description: "Smoke-check local Lovelace dashboard entity and resource readiness", Mutates: false},
			{Command: "./dm dev fixtures DASHBOARD --json", Description: "Refresh redacted local dashboard state fixtures from a read-only HA target", Mutates: true},
			{Command: "./dm check --ensure-dev --json", Description: "Start local dev HA when needed, then validate config and run fast tests", Mutates: true},
			{Command: "./dm check dev --json", Description: "Run full development suite with trace validation", Mutates: true},
			{Command: "./dm check prod --json", Description: "Read-only production verify and audit sweep", Mutates: false},
		},
		ProductionGuardrails: []string{
			"Do not print token values.",
			"Use production commands only with sourced .env/.env.local credentials.",
			"Production test runs require explicit --allow-prod because they mutate live state.",
		},
	}
}

func agentContextLocalConfig() (*haconfig.HAConfig, error) {
	if agentContextOffline || os.Getenv("DM_MCP_WORKER_ROOT") != "" {
		return nil, fmt.Errorf("runtime discovery disabled")
	}
	return resolveProjectLocalConfig()
}

func credentialPresence() map[string]bool {
	keys := []string{
		"HASS_DEV_URL",
		"HASS_DEV_TOKEN",
		"HASS_PROD_URL",
		"HASS_PROD_TOKEN",
		"HASS_URL",
		"HASS_TOKEN",
		"HASS_BEARER_TOKEN",
	}
	presence := make(map[string]bool, len(keys))
	for _, key := range keys {
		presence[key] = os.Getenv(key) != ""
	}
	return presence
}

func gitStatusShort() []string {
	output, err := projectGitCommand("status", "--short").Output()
	if err != nil {
		return nil
	}
	var changes []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			changes = append(changes, line)
		}
	}
	return changes
}

func gitTrackedFiles() ([]string, error) {
	root, err := projectGitCommand("rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, err
	}
	output, err := projectGitCommand("ls-files", "--full-name", "-z").Output()
	if err != nil {
		return nil, err
	}
	return gitReviewPaths(strings.TrimSpace(string(root)), normalizeChangedFiles(strings.Split(string(output), "\x00"))), nil
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func issueArtifacts(issues []agentIssue) []string {
	var artifacts []string
	seen := make(map[string]bool)
	for _, issue := range issues {
		if issue.File == "" || seen[issue.File] {
			continue
		}
		seen[issue.File] = true
		artifacts = append(artifacts, issue.File)
	}
	sort.Strings(artifacts)
	return artifacts
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	var unique []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func walkYAMLMaps(node *yaml.Node, visit func(*yaml.Node)) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		visit(node)
	}
	for _, child := range node.Content {
		walkYAMLMaps(child, visit)
	}
}

func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func yamlMapValueIncludingMerge(mapping *yaml.Node, key string) *yaml.Node {
	return yamlMapValueIncludingMergeVisited(mapping, key, make(map[*yaml.Node]bool))
}

func yamlMapValueIncludingMergeVisited(mapping *yaml.Node, key string, visited map[*yaml.Node]bool) *yaml.Node {
	if mapping == nil || visited[mapping] {
		return nil
	}
	visited[mapping] = true
	if mapping.Kind == yaml.AliasNode {
		return yamlMapValueIncludingMergeVisited(mapping.Alias, key, visited)
	}
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	if value := mapValue(mapping, key); value != nil {
		return value
	}

	merge := mapValue(mapping, "<<")
	if merge == nil {
		return nil
	}
	if merge.Kind == yaml.SequenceNode {
		for _, item := range merge.Content {
			if value := yamlMapValueIncludingMergeVisited(item, key, visited); value != nil {
				return value
			}
		}
		return nil
	}
	return yamlMapValueIncludingMergeVisited(merge, key, visited)
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func yamlSequenceContainsScalar(sequence *yaml.Node, value string) bool {
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return false
	}
	for _, item := range sequence.Content {
		if strings.TrimSpace(yamlScalarValue(item)) == value {
			return true
		}
	}
	return false
}

func yamlDocumentRoot(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}
