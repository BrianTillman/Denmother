package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	planJSON  bool
	planTitle string
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Manage versioned agent execution plans",
}

var planInitCmd = &cobra.Command{
	Use:   "init <slug>",
	Short: "Create an active execution plan",
	Args:  cobra.ExactArgs(1),
	RunE:  runPlanInit,
}

var planStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "List active and completed execution plans",
	RunE:  runPlanStatus,
}

var planCompleteCmd = &cobra.Command{
	Use:   "complete <slug>",
	Short: "Move an active execution plan to completed",
	Args:  cobra.ExactArgs(1),
	RunE:  runPlanComplete,
}

type planSummary struct {
	Active       []planRef `json:"active"`
	Completed    []planRef `json:"completed"`
	NextCommands []string  `json:"next_commands,omitempty"`
}

type planRef struct {
	Slug string `json:"slug"`
	Path string `json:"path"`
}

func init() {
	for _, cmd := range []*cobra.Command{planInitCmd, planStatusCmd, planCompleteCmd} {
		cmd.Flags().BoolVar(&planJSON, "json", false, "Emit a machine-readable JSON summary")
	}
	planInitCmd.Flags().StringVar(&planTitle, "title", "", "Human-readable plan title")

	planCmd.AddCommand(planInitCmd)
	planCmd.AddCommand(planStatusCmd)
	planCmd.AddCommand(planCompleteCmd)
	rootCmd.AddCommand(planCmd)
}

func runPlanInit(cmd *cobra.Command, args []string) error {
	if _, err := selectedProject(); err != nil {
		return err
	}
	rt := newCommandRuntime("plan", planJSON, cmd.OutOrStdout())
	rt.SetProfile("init")

	slug, err := normalizePlanSlug(args[0])
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "create-plan",
			Title:   "Create active execution plan",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "plan creation failed")
	}
	path := activePlanPath(slug)
	if fileExists(path) {
		rt.AddStep(operator.Step{
			ID:      "create-plan",
			Title:   "Create active execution plan",
			Status:  operator.StatusFailure,
			Summary: fmt.Sprintf("%s already exists", path),
		})
		return rt.Complete(operator.StatusFailure, "plan already exists")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderPlanTemplate(slug, planTitle)), 0644); err != nil {
		return err
	}

	step := operator.Step{
		ID:           "create-plan",
		Title:        "Create active execution plan",
		Status:       operator.StatusSuccess,
		Summary:      "created " + path,
		Artifacts:    []string{path},
		NextCommands: []string{"./dm plan status --json", "./dm agent review --json"},
		Mutates:      true,
		Details: map[string]any{
			"plan": planRef{Slug: slug, Path: path},
		},
	}
	rt.AddStep(step)
	if !planJSON {
		fmt.Fprintln(cmd.OutOrStdout(), path)
	}
	return rt.Complete(operator.StatusSuccess, "created active execution plan")
}

func runPlanStatus(cmd *cobra.Command, args []string) error {
	if _, err := selectedProject(); err != nil {
		return err
	}
	rt := newCommandRuntime("plan", planJSON, cmd.OutOrStdout())
	rt.SetProfile("status")

	summary := planStatusSummary()
	rt.AddStep(operator.Step{
		ID:           "list-plans",
		Title:        "List execution plans",
		Status:       operator.StatusSuccess,
		Summary:      fmt.Sprintf("%d active plan(s), %d completed plan(s)", len(summary.Active), len(summary.Completed)),
		Artifacts:    planArtifacts(summary),
		NextCommands: summary.NextCommands,
		Details: map[string]any{
			"plans": summary,
		},
	})
	if !planJSON {
		for _, plan := range summary.Active {
			fmt.Fprintf(cmd.OutOrStdout(), "active %s %s\n", plan.Slug, plan.Path)
		}
		for _, plan := range summary.Completed {
			fmt.Fprintf(cmd.OutOrStdout(), "completed %s %s\n", plan.Slug, plan.Path)
		}
	}
	return rt.Complete(operator.StatusSuccess, "listed execution plans")
}

func runPlanComplete(cmd *cobra.Command, args []string) error {
	if _, err := selectedProject(); err != nil {
		return err
	}
	rt := newCommandRuntime("plan", planJSON, cmd.OutOrStdout())
	rt.SetProfile("complete")

	slug, err := normalizePlanSlug(args[0])
	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "complete-plan",
			Title:   "Complete execution plan",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "plan completion failed")
	}
	source := activePlanPath(slug)
	dest := completedPlanPath(slug)
	if !fileExists(source) {
		rt.AddStep(operator.Step{
			ID:      "complete-plan",
			Title:   "Complete execution plan",
			Status:  operator.StatusFailure,
			Summary: fmt.Sprintf("%s does not exist", source),
		})
		return rt.Complete(operator.StatusFailure, "active plan not found")
	}
	if fileExists(dest) {
		rt.AddStep(operator.Step{
			ID:      "complete-plan",
			Title:   "Complete execution plan",
			Status:  operator.StatusFailure,
			Summary: fmt.Sprintf("%s already exists", dest),
		})
		return rt.Complete(operator.StatusFailure, "completed plan already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if err := os.Rename(source, dest); err != nil {
		return err
	}
	rt.AddStep(operator.Step{
		ID:           "complete-plan",
		Title:        "Complete execution plan",
		Status:       operator.StatusSuccess,
		Summary:      fmt.Sprintf("moved %s to %s", source, dest),
		Artifacts:    []string{dest},
		NextCommands: []string{"./dm plan status --json", "./dm agent review --json"},
		Mutates:      true,
		Details: map[string]any{
			"from": source,
			"to":   dest,
		},
	})
	if !planJSON {
		fmt.Fprintln(cmd.OutOrStdout(), dest)
	}
	return rt.Complete(operator.StatusSuccess, "completed execution plan")
}

var planSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func normalizePlanSlug(raw string) (string, error) {
	slug := strings.TrimSuffix(strings.TrimSpace(strings.ToLower(raw)), ".md")
	slug = strings.ReplaceAll(slug, "_", "-")
	if !planSlugRe.MatchString(slug) {
		return "", fmt.Errorf("invalid plan slug %q; use lowercase letters, numbers, and hyphens", raw)
	}
	return slug, nil
}

func activePlanPath(slug string) string {
	return filepath.ToSlash(projectArtifactPath("docs/exec-plans/active/" + slug + ".md"))
}

func completedPlanPath(slug string) string {
	return filepath.ToSlash(projectArtifactPath("docs/exec-plans/completed/" + slug + ".md"))
}

func renderPlanTemplate(slug string, title string) string {
	if strings.TrimSpace(title) == "" {
		title = titleFromSlug(slug)
	}
	return fmt.Sprintf(`# %s

Created: %s
Status: active

## Goal

Describe the user-visible outcome this plan should deliver.

## Scope

- In scope:
- Out of scope:

## Acceptance Criteria

- [ ] Implementation is complete.
- [ ] Relevant tests and harness checks pass.
- [ ] Agent-facing docs or generated maps are updated when behavior changes.

## Risk

Document production, HomeKit, credential, or runtime mutation risks.

## Validation Log

- Pending.

## Decisions

- Pending.

## Follow-Up Debt

- Pending.
`, title, time.Now().UTC().Format("2006-01-02"))
}

func titleFromSlug(slug string) string {
	words := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for idx, word := range words {
		if word == "" {
			continue
		}
		words[idx] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func planStatusSummary() planSummary {
	summary := planSummary{
		Active:       collectPlans(projectArtifactPath("docs/exec-plans/active")),
		Completed:    collectPlans(projectArtifactPath("docs/exec-plans/completed")),
		NextCommands: []string{"./dm plan init <slug> --json", "./dm plan complete <slug> --json"},
	}
	return summary
}

func collectPlans(dir string) []planRef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []planRef{}
	}
	plans := []planRef{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == ".gitkeep" || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		plans = append(plans, planRef{
			Slug: slug,
			Path: filepath.ToSlash(filepath.Join(dir, entry.Name())),
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].Slug < plans[j].Slug
	})
	return plans
}

func planArtifacts(summary planSummary) []string {
	var artifacts []string
	for _, plan := range append(summary.Active, summary.Completed...) {
		artifacts = append(artifacts, plan.Path)
	}
	return artifacts
}
