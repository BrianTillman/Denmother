package cmd

import (
	"fmt"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	testProdURL     string
	testProdToken   string
	testDevURL      string
	testDevToken    string
	testHAURL       string
	testHAToken     string
	testPattern     string
	testCase        string
	testChanged     bool
	testChangedBase string
	testVerbose     bool
	testTags        []string
	testTrace       bool
	testAllowProd   bool
	testJSON        bool
)

var (
	resolveTestTarget   = operator.ResolveTargetContext
	runTestExecuteTests = executeTests
)

var testCmd = &cobra.Command{
	Use:   "test [test-file]",
	Short: "Run end-to-end automation tests",
	Long: `Run end-to-end tests for Home Assistant automations.

Tests are defined in YAML files that specify:
  - Initial state setup
  - Trigger actions (service calls, state changes)
  - Expected outcomes (assertions)

The test runner connects to a running Home Assistant instance and executes
tests by triggering entity state changes and verifying automation responses.

This command defaults to the DEVELOPMENT Home Assistant instance since tests
typically run against the local dev container.

Connection Resolution (in order of precedence):
  1. Explicit override flags (--dev-url/--dev-token or --prod-url/--prod-token)
  2. Environment variables for the selected instance
  3. Legacy environment variables
  4. Local dev container (auto-detected for development)

Examples:
  # Run all tests (uses local dev container if running)
  dm test

  # Run specific test file
  dm test ha-config/tests/presence/office_lights_test.yaml

  # Run tests matching tags
  dm test --tags presence,lights,fast

  # Run only tests impacted by the current git diff
  dm test --changed --json

  # Run only tests impacted by a pull request base ref
  dm test --changed --changed-base origin/master --json

  # Verbose output
  dm test -v

  # Test against production (requires explicit acknowledgement)
  dm test --prod-url https://ha.example.com --prod-token TOKEN --allow-prod`,
	RunE: runTest,
}

func init() {
	testCmd.Flags().StringVar(&testDevURL, "dev-url", "", "Development Home Assistant URL")
	testCmd.Flags().StringVar(&testDevToken, "dev-token", "", "Development Home Assistant token")
	testCmd.Flags().StringVar(&testProdURL, "prod-url", "", "Production Home Assistant URL")
	testCmd.Flags().StringVar(&testProdToken, "prod-token", "", "Production Home Assistant token")
	testCmd.Flags().StringVar(&testHAURL, "ha-url", "", "Home Assistant URL (legacy, use --dev-url)")
	testCmd.Flags().StringVar(&testHAToken, "ha-token", "", "Home Assistant token (legacy, use --dev-token)")
	testCmd.Flags().StringVar(&testPattern, "pattern", "",
		"Glob pattern for test files (e.g., 'ha-config/tests/**/*_test.yaml')")
	testCmd.Flags().StringVar(&testCase, "case", "",
		"Run only test cases whose name contains this value")
	testCmd.Flags().BoolVar(&testChanged, "changed", false,
		"Run automation tests impacted by the current git diff")
	testCmd.Flags().StringVar(&testChangedBase, "changed-base", "",
		"Base git ref for --changed selection (defaults to local uncommitted changes)")
	testCmd.Flags().BoolVarP(&testVerbose, "verbose", "v", false,
		"Verbose output")
	testCmd.Flags().StringSliceVar(&testTags, "tags", []string{},
		"Filter tests by tags (comma-separated)")
	testCmd.Flags().BoolVar(&testTrace, "trace", false,
		"Request automation trace validation (declared trace_assertions always run)")
	testCmd.Flags().BoolVar(&testAllowProd, "allow-prod", false,
		"Allow mutating a production Home Assistant instance")
	testCmd.Flags().BoolVar(&testJSON, "json", false,
		"Emit a machine-readable JSON summary")

	rootCmd.AddCommand(testCmd)
}

func runTest(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = commandContext()
	}
	rt := newCommandRuntime("test", testJSON, cmd.OutOrStdout())

	effectiveArgs := args
	var planSummary *testPlanSummary
	planStatus := operator.StatusSuccess
	if testChanged {
		if len(args) > 0 || testPattern != "" {
			rt.AddStep(operator.Step{
				ID:      "select-changed-tests",
				Title:   "Select changed tests",
				Status:  operator.StatusFailure,
				Summary: "--changed cannot be combined with explicit test files or --pattern",
				Hints:   []string{"Use either `./dm test --changed --json` or an explicit `./dm test <file> --json` command."},
			})
			return rt.Complete(operator.StatusFailure, "changed test selection is ambiguous")
		}

		var err error
		if testChangedBase != "" {
			changedFiles, changedErr := changedFilesForReviewBase(testChangedBase)
			if changedErr != nil {
				rt.AddStep(operator.Step{
					ID:      "plan-tests",
					Title:   "Plan impacted tests",
					Status:  operator.StatusFailure,
					Summary: changedErr.Error(),
				})
				return rt.Complete(operator.StatusFailure, "changed test plan failed")
			}
			planSummary, err = buildTestPlanSummary(changedFiles)
		} else {
			changedFiles, changedErr := changedFilesForReview()
			if changedErr != nil {
				rt.AddStep(operator.Step{
					ID:      "plan-tests",
					Title:   "Plan impacted tests",
					Status:  operator.StatusFailure,
					Summary: changedErr.Error(),
				})
				return rt.Complete(operator.StatusFailure, "changed test plan failed")
			}
			planSummary, err = buildTestPlanSummary(changedFiles)
		}
		if err != nil {
			rt.AddStep(operator.Step{
				ID:      "plan-tests",
				Title:   "Plan impacted tests",
				Status:  operator.StatusFailure,
				Summary: err.Error(),
			})
			return rt.Complete(operator.StatusFailure, "changed test plan failed")
		}

		planStep := testPlanStep(planSummary)
		rt.AddStep(planStep)
		planStatus = planStep.Status
		effectiveArgs = plannedTestPaths(planSummary.TestFiles)
		if len(effectiveArgs) == 0 {
			for _, command := range testPlanNextCommands(planSummary) {
				rt.AddHint(command)
			}
			return rt.Complete(planStatus, testPlanSummaryText(planSummary))
		}
	}

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     testProdURL,
		ProdToken:   testProdToken,
		DevURL:      testDevURL,
		DevToken:    testDevToken,
		LegacyURL:   testHAURL,
		LegacyToken: testHAToken,
	}

	resolved, err := resolveTestTarget(ctx, haconfig.InstanceDev, flags, operator.ModeMutating, testAllowProd)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()

	if err != nil {
		step := operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve Home Assistant target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		}
		if _, ok := err.(*operator.GuardrailError); ok {
			step.ErrorCode = "guardrail_blocked"
			step.Hints = append(step.Hints, "Re-run with --allow-prod only when you intentionally want to mutate production Home Assistant state.")
			rt.AddHint("Production test runs are blocked until you explicitly acknowledge them with --allow-prod.")
		}
		rt.AddStep(step)
		return rt.Complete(operator.StatusFailure, "test run could not start")
	}

	summary, testErr := runTestExecuteTests(resolved.Config, testExecutionOptions{
		Context:      ctx,
		Args:         effectiveArgs,
		Pattern:      testPattern,
		Case:         testCase,
		Tags:         testTags,
		Verbose:      testVerbose && !testJSON,
		Trace:        testTrace,
		PrintResults: !testJSON,
	})
	if testErr != nil {
		rt.AddStep(operator.Step{
			ID:      "run-tests",
			Title:   "Run automation tests",
			Status:  operator.StatusFailure,
			Summary: testErr.Error(),
		})
		rt.AddHint("Check the Home Assistant URL/token resolution and test selection, then rerun `dm test`.")
		return rt.Complete(operator.StatusFailure, "test run failed before execution")
	}

	step := summary.Step("run-tests", "Run automation tests", testTags)
	if planSummary != nil {
		if step.Details == nil {
			step.Details = map[string]any{}
		}
		step.Details["test_plan"] = planSummary
	}
	rt.AddStep(step)

	if step.Status == operator.StatusWarning {
		rt.AddHint("No tests ran for the selected files or tags.")
	}

	finalStatus := operator.MergeStatus(planStatus, step.Status)
	return rt.Complete(finalStatus, changedTestSummaryText(planSummary, summary, finalStatus))
}

func testSummaryText(summary *testSummary) string {
	if summary == nil {
		return "test run finished with no summary"
	}

	switch summary.Status() {
	case operator.StatusSuccess:
		return fmt.Sprintf("tests passed: %d passed, %d skipped across %d selected file(s)", summary.Passed, summary.Skipped, summary.FilesSelected)
	case operator.StatusWarning:
		return fmt.Sprintf("no test cases executed (%d skipped)", summary.Skipped)
	default:
		return fmt.Sprintf("test run failed: %s across %d selected file(s)", summary.describeCounts(), summary.FilesSelected)
	}
}

func changedTestSummaryText(plan *testPlanSummary, summary *testSummary, status operator.Status) string {
	if plan == nil {
		return testSummaryText(summary)
	}
	if summary == nil || !summary.AnyRun() {
		return testPlanSummaryText(plan)
	}
	if status == operator.StatusWarning && len(plan.MissingTests) > 0 && summary.Status() == operator.StatusSuccess {
		return fmt.Sprintf("changed tests passed; %d missing conventional test(s) still need coverage", len(plan.MissingTests))
	}
	return testSummaryText(summary)
}
