# Using Denmother with an agent

Give your coding agent a way to verify Home Assistant changes. Denmother supplies
project context, tests selected from a Git diff, fresh trace assertions, dashboard
screenshots, and diagnostics with source references. The agent can use the same
CLI you use and return test results and diagnostics with its edit.

The bundled [Denmother skill](../skills/denmother/SKILL.md) teaches that workflow.
It is embedded in the binary, so registration works offline once `dm` is
available. Clients without shell access can use the [local MCP tools](mcp.md).

Useful first requests include fixing an unknown entity reference, adding a
regression test for a motion automation, or previewing a custom card in the
dashboard starter. Keep the project, intended behavior, and development target
explicit in your request.

## Install and register the skill

Use [setup](installing.md#install-from-source) to install the CLI and skill
together. If `dm` is already installed, register its embedded skill:

```sh
dm skills install --harness detected --json
dm skills status --json
```

See [harness registration](installing.md#harness-registration) for explicit
client selection and team/project scope, and [ownership and recovery](installing.md#ownership-and-recovery)
for upgrades, conflicts, and uninstall. Status checks the installed files;
start a fresh agent session to check that the client loads the skill.

## Start a project

After installing `dm`, run from your HA repository:

```sh
dm init --directory . --agent --json
dm capabilities --json
dm agent context --no-schema --json
dm --no-schema --json
```

`init` writes `.denmother.yaml`, a synthetic helper automation and test, and an
entity inventory. `--config home-assistant` selects a different directory inside
`--directory`. If an existing configuration is present, the synthetic example is
placed in `examples/denmother` with its own project settings. The initialization
result reports the exact example path and commands. Production YAML is not augmented.
The offline context command reports native schema verification as incomplete;
run `dm agent context --json` for the full baseline once development HA is ready.

`--agent` creates `AGENTS.md`, or `DENMOTHER-AGENT.md` when `AGENTS.md` already
exists. Read or link the latter from your existing agent instructions. This is
optional: managed skill registration does not require editing either file.

Existing files are preserved. Use `init --dry-run` to preview additions and read
[project initialization](project.md#initialize-a-project) for generated files and
ignore rules. For existing projects, use the reported example path for runtime
commands. Prefer managed registration over the older `init --skill` copy; see
[harness registration](installing.md#harness-registration).

## Change, test, and diagnose

For the synthetic example created in an empty project:

```sh
dm --no-schema --json
dm dev up --json
dm --json
dm test ha-config/tests/presence/motion_lamp_test.yaml --json
dm observe automation.study_motion_lamp --json --compact
dm dev down --json
```

For an existing configuration, use the example path returned by `init` on every
command above with `--config PATH`. For real changes, use a dedicated development
configuration with helpers replacing hardware. Refresh it with `dev up` after
editing YAML. `dev down` preserves runtime state; `dev reset` discards it.

Try a repair: replace the lamp target in the example automation with
`input_boolean.study_missing_lamp`, run offline validation, and inspect the source
location. Correct the target, rerun validation, refresh the runtime, and rerun
the test. Successful state assertions and a fresh trace establish this synthetic
automation's behavior. Offline validation alone does not.

| Task | Command |
| --- | --- |
| Check skill ownership, resources, compatibility, and PATH | `dm skills status --json` |
| Discover installed commands and schemas | `dm capabilities --json` |
| Inspect project and verification baseline | `dm agent context --json` |
| Validate without Docker or runtime tests | `dm --no-schema --json` |
| Review the requested diff | `dm agent review --json` |
| Select impacted tests | `dm test plan --json` |
| Check test structure and coverage hygiene | `dm test doctor --json` |
| Inspect optional maintenance tasks | `dm agent tasks --json`, `dm agent next --json` |
| Diagnose runtime health | `dm dev doctor --json` |
| Inspect traces and logs | `dm observe automation.ENTITY --json` |

`dm test --changed --json` runs the selection proposed by `test plan`.
`dm check --ensure-dev --json` combines validation and fast local tests;
`dm check dev --json` runs validation and the full development suite with traces.
`agent tasks` suggests maintenance work with acceptance criteria, while
`agent quality` reports static repository hygiene. Use these reports to plan work;
run tests to check behavior.

Diff-based selection requires Git and uses static dependency heuristics. Explicit
test selection works when those heuristics cannot identify the relevant tests.
Maintenance suggestions do not expand the user's requested change.

## Integration contract

`capabilities` gives a concise catalog of the installed Cobra command tree,
prerequisites, default targets, and possible mutation scopes. Use
`dm capabilities --command "test" --json` or `--command "agent context"` for
detailed local and inherited flags. Add `--schemas` to include the complete
`dm.operator.v1` result schema without generating repository files. The catalog
uses `dm.capabilities.v1` and reports the supported result schema version.
Mutation scopes describe possible effects; flags
such as `--check`, `--dry-run`, `--render`, and `--ensure-dev` refine behavior.
`--compact` and `--evidence-dir` add local file writes to otherwise read-only
commands. Installer commands (`setup` and `skills`) reject both flags to preserve
read-only status and dry-run behavior; capture their JSON stdout instead. The CLI
does not prompt interactively.

`steps[].next_actions` contains executable, argument vector, working directory,
mutation scope, whether explicit human handling is needed, and any required
credential environment names. Pass arguments directly to process execution.
Legacy command text remains available. Concrete Denmother recommendations are
rewritten using the running executable and absolute selected configuration;
unresolved templates or shell expressions have no executable action. Read-only
diagnostic recommendations retain the resolved instance URL when supported.
Tokens and `--allow-prod` are never copied into a generated action.

Check status and exit code together: 0 success, 1 failure, 2 warning, 3 partial.
Capture stdout even for nonzero exits. Configuration and argument errors in JSON
mode use the same envelope. Stable error codes include `invalid_arguments`,
`invalid_project`, `validation_failed`, `verification_incomplete`,
`step_failed`, `command_failed`, `guardrail_blocked`, and
`result_processing_failed`. Findings retain more specific rule IDs. Generic
failures require inspecting the associated step before retrying.

Installer results use the same envelope. Their installation step contains
`details.installation`, including plan ID, provenance, selected harnesses,
detection evidence, operations, conflicts, and verification. Installer errors
include `destination_conflict`, `checksum_failure`, `unsupported_platform`,
`version_mismatch`, `permission_failure`, and `incomplete_verification`.
Inspect applied and remaining files before retrying an interrupted operation.

Context includes `git.available`; `git.dirty: false` alone does not establish a
clean repository when Git is unavailable. Context reports both
`collection_status` and `verification_status`.
Its top-level status follows baseline verification, so missing schema or inventory
evidence yields partial even when context collection succeeded. Offline validation
reports `schema_requested: false`, `runtime_tests_run: false`, and the check
scope. Success applies only to the requested checks.

Consumers should accept additive optional fields within `dm.operator.v1` and use
the schema returned by the installed binary when validating output. Breaking
changes require a new version. Skill metadata declares compatibility, but agents
should discover capabilities for development and prerelease builds.

## Evidence and output size

```sh
dm observe automation.study_motion_lamp --json --compact --evidence-dir artifacts/diagnosis
```

`--compact` writes the full result to a unique JSON file and abbreviates detail
payloads: at most ten array entries, shortened long strings, and omitted raw
traces/logs. Envelope and step statuses remain intact. `truncated: true` signals
that details were removed; `evidence` is an absolute path to the complete result.
Read it before concluding that an omitted item is absent. Evidence files use
mode 0600 and newly created evidence directories use 0700 where supported.

`--evidence-dir` alone saves complete evidence without abbreviating stdout.
The default compact directory is `<project>/artifacts/dm`. Both flags require
`--json`. Existing `test doctor --compact` keeps its grouped-summary meaning;
use `test doctor --json --evidence-dir PATH` for full per-case evidence.
Artifacts can contain private home state; they are local outputs, not automatic
uploads. An evidence write failure makes the command fail with
`result_processing_failed`; inspect the retained steps before repeating work.

See [agent acceptance evaluations](../evals/agent-adoption/README.md) for realistic
tasks and [the local MCP adapter](mcp.md) for clients without shell access.
