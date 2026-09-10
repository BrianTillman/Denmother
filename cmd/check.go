package cmd

import (
	"fmt"
	"time"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

var (
	checkJSON      bool
	checkVerbose   bool
	checkProdURL   string
	checkProdToken string
	checkDevURL    string
	checkDevToken  string
	checkHAURL     string
	checkHAToken   string
	checkEnsureDev bool
)

var (
	resolveCheckLocalConfig   = resolveProjectLocalConfig
	resolveCheckProfileTarget = operator.ResolveTarget
	runCheckValidationSuite   = runValidationSuite
	runCheckExecuteTestsFunc  = executeTests
	runCheckExecuteAuditFunc  = executeAudit
	runCheckExecuteVerifyFunc = executeVerify
)

var checkCmd = &cobra.Command{
	Use:   "check [profile]",
	Short: "Run an operator confidence sweep",
	Long: `Run the staged operator workflow.

Bare "dm check" maps to the safe local profile:
  - Run repository validations
  - Run fast automation tests against the local dev instance

Additional Phase 2 profiles:
  dm check dev
    - Run repository validations
    - Run the full automation test suite with trace validation against development

  dm check prod
    - Run repository validations
    - Verify live production device parameters
    - Audit production automation history`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "Emit a machine-readable JSON summary")
	checkCmd.Flags().BoolVarP(&checkVerbose, "verbose", "v", false, "Verbose workflow output")
	checkCmd.Flags().StringVar(&checkDevURL, "dev-url", "", "Development Home Assistant URL")
	checkCmd.Flags().StringVar(&checkDevToken, "dev-token", "", "Development Home Assistant token")
	checkCmd.Flags().StringVar(&checkProdURL, "prod-url", "", "Production Home Assistant URL")
	checkCmd.Flags().StringVar(&checkProdToken, "prod-token", "", "Production Home Assistant token")
	checkCmd.Flags().StringVar(&checkHAURL, "ha-url", "", "Home Assistant URL (legacy, profile-specific)")
	checkCmd.Flags().StringVar(&checkHAToken, "ha-token", "", "Home Assistant token (legacy, profile-specific)")
	checkCmd.Flags().BoolVar(&checkEnsureDev, "ensure-dev", false, "Start the isolated local dev HA environment before local fast tests when needed")

	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	profile := "local"
	if len(args) == 1 {
		profile = args[0]
	}

	rt := newCommandRuntime("check", checkJSON, cmd.OutOrStdout())
	rt.SetProfile(profile)

	switch profile {
	case "local":
		return runLocalCheck(rt)
	case "dev", "prod":
		if profile == "dev" {
			return runDevCheck(rt)
		}
		return runProdCheck(rt)
	default:
		rt.AddHint("Supported profiles: local, dev, prod.")
		return rt.Complete(operator.StatusFailure, fmt.Sprintf("unknown check profile %q (supported: local, dev, prod)", profile))
	}
}

func runLocalCheck(rt *operator.Runtime) error {
	if hasExplicitRemoteCheckFlags() {
		rt.AddHint("Use `dm check dev` or `dm check prod` when you need an explicit remote target.")
		return rt.Complete(operator.StatusFailure, "local profile does not accept remote target flags")
	}

	localTarget := &operator.Target{
		Name:     "local",
		Instance: string(haconfig.InstanceDev),
		Risk:     operator.RiskSafe,
		IsLocal:  true,
		Mode:     operator.ModeMutating,
		Guardrails: []string{
			"Runs only against the local development Home Assistant instance.",
		},
	}

	ensureStatus := operator.StatusSuccess
	config, err := resolveCheckLocalConfig()
	if err != nil && checkEnsureDev {
		env, prepErr := prepareDevEnvironment()
		if prepErr != nil {
			ensureStatus = operator.StatusFailure
			rt.AddStep(operator.Step{
				ID:      "ensure-dev",
				Title:   "Ensure local development Home Assistant",
				Status:  operator.StatusFailure,
				Summary: prepErr.Error(),
			})
		} else if startErr := runDockerCompose(env, "up", "-d", "homeassistant", "ui-proxy"); startErr != nil {
			ensureStatus = operator.StatusFailure
			rt.AddStep(operator.Step{
				ID:      "ensure-dev",
				Title:   "Ensure local development Home Assistant",
				Status:  operator.StatusFailure,
				Summary: startErr.Error(),
				Details: map[string]any{
					"environment": env,
				},
			})
		} else if bootErr := bootstrapPortableDev(rootCmd.Context(), env); bootErr != nil {
			ensureStatus = operator.StatusFailure
			rt.AddStep(operator.Step{ID: "ensure-dev", Title: "Authenticate development HA", Status: operator.StatusFailure, Summary: bootErr.Error()})
		} else {
			rt.AddStep(operator.Step{
				ID:      "ensure-dev",
				Title:   "Ensure local development Home Assistant",
				Status:  operator.StatusSuccess,
				Summary: fmt.Sprintf("started %s at %s", env.ProjectName, env.HAURL),
				Mutates: true,
				Details: map[string]any{
					"environment": env,
				},
			})
			config, err = resolveCheckLocalConfig()
		}
	}
	if err == nil {
		localTarget.URL = config.URL
		localTarget.Source = config.Source
	} else {
		localTarget.Source = "local dev container (not currently available)"
	}

	rt.SetTarget(localTarget)
	rt.PrintPreflight()

	overall := operator.StatusSuccess
	overall = operator.MergeStatus(overall, ensureStatus)

	validation := runCheckValidationSuite(configPath, checkJSON)
	validationStep := validation.Step()
	rt.AddStep(validationStep)
	overall = operator.MergeStatus(overall, validationStep.Status)

	if config == nil {
		testStep := operator.Step{
			ID:      "test-fast",
			Title:   "Run fast automation tests",
			Status:  operator.StatusPartial,
			Summary: "skipped fast tests because the local development Home Assistant instance is unavailable",
			Hints: []string{
				"Start the dev environment with `./dm dev up --json` or rerun `./dm check --ensure-dev --json`.",
			},
		}
		rt.AddStep(testStep)
		rt.AddHint("Local validations completed, but fast automation tests could not run without the dev environment.")
		overall = operator.MergeStatus(overall, testStep.Status)
		return rt.Complete(overall, checkSummary(overall))
	}

	testSummary, testErr := runCheckExecuteTestsFunc(config, testExecutionOptions{
		Context:      rootCmd.Context(),
		Tags:         []string{"fast"},
		Verbose:      checkVerbose && !checkJSON,
		PrintResults: !checkJSON,
	})
	if testErr != nil {
		rt.AddStep(operator.Step{
			ID:      "test-fast",
			Title:   "Run fast automation tests",
			Status:  operator.StatusFailure,
			Summary: testErr.Error(),
		})
		rt.AddHint("Fix the local Home Assistant connectivity issue and rerun `dm check`.")
		overall = operator.MergeStatus(overall, operator.StatusFailure)
		return rt.Complete(overall, checkSummary(overall))
	}

	testStep := testSummary.Step("test-fast", "Run fast automation tests", []string{"fast"})
	rt.AddStep(testStep)
	overall = operator.MergeStatus(overall, testStep.Status)

	if testStep.Status == operator.StatusWarning {
		rt.AddHint("No fast tests ran. Add or retag automation tests with the `fast` tag to keep the local workflow useful.")
	}

	return rt.Complete(overall, checkSummary(overall))
}

func runDevCheck(rt *operator.Runtime) error {
	if checkProdURL != "" || checkProdToken != "" {
		rt.AddHint("`dm check dev` is fixed to the development workflow. Use `dm check prod` for production-backed checks.")
		return rt.Complete(operator.StatusFailure, "dev profile does not accept production target flags")
	}

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		DevURL:      checkDevURL,
		DevToken:    checkDevToken,
		LegacyURL:   checkHAURL,
		LegacyToken: checkHAToken,
	}

	resolved, err := resolveCheckProfileTarget(haconfig.InstanceDev, flags, operator.ModeMutating, false)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()

	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve development target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "development workflow could not start")
	}

	overall := operator.StatusSuccess

	validationStep := runCheckValidationSuite(configPath, checkJSON).Step()
	rt.AddStep(validationStep)
	overall = operator.MergeStatus(overall, validationStep.Status)

	testSummary, testErr := runCheckExecuteTestsFunc(resolved.Config, testExecutionOptions{
		Context:      rootCmd.Context(),
		Verbose:      checkVerbose && !checkJSON,
		Trace:        true,
		PrintResults: !checkJSON,
	})
	if testErr != nil {
		rt.AddStep(operator.Step{
			ID:      "test-dev",
			Title:   "Run full development automation suite",
			Status:  operator.StatusFailure,
			Summary: testErr.Error(),
		})
		rt.AddHint("Fix the development Home Assistant connectivity or test selection issue, then rerun `dm check dev`.")
		overall = operator.MergeStatus(overall, operator.StatusFailure)
		return rt.Complete(overall, checkDevSummary(overall))
	}

	testStep := testSummary.Step("test-dev", "Run full development automation suite", nil)
	if testStep.Details == nil {
		testStep.Details = map[string]any{}
	}
	testStep.Details["trace_enabled"] = true
	rt.AddStep(testStep)
	overall = operator.MergeStatus(overall, testStep.Status)

	if testStep.Status == operator.StatusWarning {
		rt.AddHint("No tests ran in the development workflow. Add tests or narrow the suite with `dm test --pattern ...` for investigation.")
	}

	return rt.Complete(overall, checkDevSummary(overall))
}

func runProdCheck(rt *operator.Runtime) error {
	if checkDevURL != "" || checkDevToken != "" {
		rt.AddHint("`dm check prod` is fixed to the production workflow. Use `dm check dev` for development-backed checks.")
		return rt.Complete(operator.StatusFailure, "prod profile does not accept development target flags")
	}

	flags := haconfig.InstanceFlags{ConfigPath: configFile(""),
		ProdURL:     checkProdURL,
		ProdToken:   checkProdToken,
		LegacyURL:   checkHAURL,
		LegacyToken: checkHAToken,
	}

	resolved, err := resolveCheckProfileTarget(haconfig.InstanceProd, flags, operator.ModeReadOnly, true)
	if resolved != nil {
		rt.SetTarget(resolved.Target)
	}
	rt.PrintPreflight()

	if err != nil {
		rt.AddStep(operator.Step{
			ID:      "resolve-target",
			Title:   "Resolve production target",
			Status:  operator.StatusFailure,
			Summary: err.Error(),
		})
		return rt.Complete(operator.StatusFailure, "production workflow could not start")
	}

	overall := operator.StatusSuccess

	validationStep := runCheckValidationSuite(configPath, checkJSON).Step()
	rt.AddStep(validationStep)
	overall = operator.MergeStatus(overall, validationStep.Status)

	verifySummary, verifyErr := runCheckExecuteVerifyFunc(resolved.Config, verifyExecutionOptions{
		Verbose:      checkVerbose,
		DryRun:       false,
		PrintResults: !checkJSON,
	})
	if verifyErr != nil {
		rt.AddStep(operator.Step{
			ID:      "verify-prod",
			Title:   "Verify live production device parameters",
			Status:  operator.StatusFailure,
			Summary: verifyErr.Error(),
		})
		overall = operator.MergeStatus(overall, operator.StatusFailure)
	} else {
		verifyStep := verifySummary.Step("verify-prod", "Verify live production device parameters")
		rt.AddStep(verifyStep)
		overall = operator.MergeStatus(overall, verifyStep.Status)
		if verifyStep.Status == operator.StatusWarning {
			rt.AddHint("No verifiable Z2M automations were found in the current config slice.")
		}
	}

	auditSummary, auditErr := runCheckExecuteAuditFunc(resolved.Config, auditExecutionOptions{
		Verbose:      checkVerbose,
		TracesOnly:   false,
		Window:       24 * time.Hour,
		Tolerance:    30 * time.Second,
		PrintResults: !checkJSON,
	}, nil)
	if auditErr != nil {
		rt.AddStep(operator.Step{
			ID:      "audit-prod",
			Title:   "Audit production automation history",
			Status:  operator.StatusFailure,
			Summary: auditErr.Error(),
		})
		overall = operator.MergeStatus(overall, operator.StatusFailure)
	} else {
		auditStep := auditSummary.Step("audit-prod", "Audit production automation history")
		rt.AddStep(auditStep)
		overall = operator.MergeStatus(overall, auditStep.Status)
		if auditStep.Status == operator.StatusWarning {
			rt.AddHint("No production activity matched the default 24h audit window. Run `dm audit --window 48h` for a broader pass.")
		}
		if auditStep.Status == operator.StatusPartial {
			rt.AddHint("Some trace-backed audit detail was unavailable. Re-run `dm audit -v` for deeper inspection.")
		}
	}

	rt.AddHint("Run `dm sync` separately when you want to refresh docs/reference from production after a check sweep.")
	return rt.Complete(overall, checkProdSummary(overall))
}

func checkSummary(status operator.Status) string {
	switch status {
	case operator.StatusSuccess:
		return "local workflow passed: validations are clean and fast tests passed"
	case operator.StatusPartial:
		return "local workflow completed with follow-up needed"
	default:
		return "local workflow found blocking issues"
	}
}

func checkDevSummary(status operator.Status) string {
	switch status {
	case operator.StatusSuccess:
		return "development workflow passed: validations are clean and the full test suite passed with trace validation"
	case operator.StatusPartial:
		return "development workflow completed with follow-up needed"
	default:
		return "development workflow found blocking issues"
	}
}

func checkProdSummary(status operator.Status) string {
	switch status {
	case operator.StatusSuccess:
		return "production workflow passed: validations, device verification, and audit checks succeeded"
	case operator.StatusWarning:
		return "production workflow completed, but some expected production activity was absent"
	case operator.StatusPartial:
		return "production workflow completed with follow-up needed"
	default:
		return "production workflow found blocking issues"
	}
}

func hasExplicitRemoteCheckFlags() bool {
	return checkDevURL != "" || checkDevToken != "" || checkProdURL != "" || checkProdToken != "" || checkHAURL != "" || checkHAToken != ""
}
