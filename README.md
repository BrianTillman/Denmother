# Denmother

**Build, test, and debug your Home Assistant setup before it runs your home.**

Denmother (`dm`) is a local development toolkit for Home Assistant. Catch broken
YAML references, test automation behavior in an isolated Home Assistant, and
preview dashboards under different conditions before changing your live setup.
When something fails, inspect execution traces, state assertions, logs, and
source references from the same CLI.

Use it from your terminal, CI, or a coding agent. Bundled examples run without
physical devices or exports from your home.

## Why Denmother?

- **Catch mistakes before a reload.** Check YAML syntax, configuration structure,
  and entity references with source locations; `--fix` suggests entity corrections.
  Add Home Assistant's native schema validation against your loaded development
  configuration, plus optional repository policies.
- **Run an isolated Home Assistant.** Start a real Home Assistant with
  Docker Compose, generated local credentials, and separate runtime state for
  each configuration and worktree. The portable runtime keeps HA on an internal
  network and exposes its UI through a local proxy.
- **Verify triggers and actions.** Exercise helper transitions, events,
  timers, and blueprints. Assert states and attributes, then check a fresh trace
  for the expected trigger, service calls, action order, or absence of unwanted
  actions. Configured cleanup and restoration also run after failure or cancellation.
- **See your dashboards in action.** Render native YAML or saved development
  dashboards in a browser, check every view, and capture screenshots. Synthetic
  fixtures and named scenarios let you preview different states; browser checks
  catch missing entities, broken resources, and custom-card errors.
- **Find out why an automation failed.** Bring an automation's traces, logbook,
  error logs, and local configuration/test references together with `dm observe`.
  Use history audits to compare observed outcomes with declared expectations
  without triggering the automation.
- **Put coding agents to work on HA.** Discover project context, review
  changes, select tests from a Git diff, and return structured results with
  suggested next actions. Bundled skills support Codex and Claude Code; a local
  MCP server exposes focused tools for clients without shell access.

Results distinguish success, warnings, incomplete verification, and failure.
See [supported workflows and limits](docs/compatibility.md#choose-a-check) for what each check covers.

## Install

The first public release and Homebrew tap are not yet published. On Linux or
macOS, install the Go toolchain specified in [go.mod](go.mod), Bash, and Python
3.9+, then run:

```sh
git clone https://github.com/BrianTillman/Denmother.git
cd Denmother
./setup --from-source
```

Setup installs `dm` into `$HOME/.local/bin` and matching skills for detected Codex
and Claude Code configurations. Follow its PATH guidance before running `dm`;
use `--binary-only` if you want only the CLI. Ownership checks protect locally
edited files during upgrades. Setup verifies registration without launching an
agent or Home Assistant.

An unpacked release uses `./setup`. See [installation options](docs/installing.md)
for local archives, pinned downloads when published, offline inputs, dry runs,
upgrades, and Homebrew skill registration. See [compatibility](docs/compatibility.md)
for platform and live harness verification limits. Contributors can build a
checkout-local binary without installing it using [the contributor guide](CONTRIBUTING.md),
which also provides an [optional devcontainer](CONTRIBUTING.md#optional-devcontainer).

## Quickstart

From the checkout after installation, try the synthetic motion-and-lamp example:

```sh
dm version --json
dm --config examples/quickstart/ha-config --no-schema
```

This offline check validates the example's YAML and static entity references.
To try a failing check, change `input_boolean.study_lamp` to
`input_boolean.study_missing_lamp` in the
[example automation](examples/quickstart/ha-config/automations/presence/motion_lamp.yaml)
and rerun. Denmother identifies the unknown entity and its source location.
Restore the target before continuing.

With Docker Engine and Compose v2 running:

```sh
dm dev up --config examples/quickstart/ha-config --json
dm --config examples/quickstart/ha-config
dm test --config examples/quickstart/ha-config --json
dm dev down --config examples/quickstart/ha-config --json
```

The first start downloads pinned images and may take several minutes. `dev up`
prints the local URL, creates credentials, and checks authentication. Source YAML
is mounted read-only; the web proxy binds to `127.0.0.1`. Keep that port local.
The example uses two native helpers and has no connection to your real home.

The tests verify that automation enablement is restored through its service,
then turn motion on, wait for the lamp, and check a **new automation trace** for
the expected service call. Cleanup restores the test state. Rerun `dev up` after
changing YAML to refresh the runtime. `dev down` preserves local state;
`dev reset` explicitly discards it.

## Tests that explain what happened

Write automation tests as YAML alongside your configuration. Each case can set
up entities, call services or fire events, wait for state and attribute
assertions, inspect execution traces, and clean up. Run a whole suite, a tagged
group, an individual case, or tests selected from your current Git diff.

For example, the quickstart pairs its lamp-state assertion with this trace check:

```yaml
trace_assertions:
  automation: automation.study_motion_lamp
  expect_completed: true
  expect_no_errors: true
  expect_actions:
    - service: input_boolean.turn_on
      target: input_boolean.study_lamp
```

Declared `trace_assertions` run automatically. Denmother excludes old trace IDs
and, by default, uses the trigger's execution context to attribute the run to the
test. You can also assert action order, reject specific actions, or verify that
no new run occurred. Missing or ambiguous trace evidence fails the check.

With cleanup and automatic restoration enabled, supported controls restore
through their native services, and failed cleanup blocks subsequent cases. See the
[complete test](examples/quickstart/ha-config/tests/presence/motion_lamp_test.yaml),
[test format](docs/testing.md#write-a-test), and [cleanup support](docs/testing.md#cleanup-support).
The [automation starter](docs/testing.md#timer-event-and-blueprint-examples)
adds timer, event, and blueprint examples.

## Preview dashboards and scenarios

The dashboard example includes two views, a local custom card, and synthetic
temperature scenarios. With Docker Compose, Node.js, and npm installed:

```sh
dm dev dashboard denmother-demo --config examples/dashboard/ha-config \
  --ensure-dev --render --json
dm dev scenario warm --config examples/dashboard/ha-config --json
dm dev dashboard denmother-demo --config examples/dashboard/ha-config \
  --render --view details --json
dm dev down --config examples/dashboard/ha-config --json
```

Denmother prepares Playwright and Chromium, checks every view by default, and
saves screenshots under the example project's `artifacts/dashboard-render`.
Open the reported URL to interact with the dashboard in Home Assistant. Browser
system dependencies may need installation; the preflight reports what's missing.

Fixtures fill missing display states; scenarios replace selected states so you
can inspect a different condition immediately. These are visual previews;
automation behavior tests use helpers and real services. Existing dashboards
saved through HA's UI can be inspected with `--storage` in a development instance.
See [dashboard development](docs/dashboards.md) for resources, custom cards, and setup.

## Use with a coding agent

The bundled skill teaches an agent how to validate, test, and diagnose Home
Assistant changes. Setup registers it for detected harnesses. If you already have
`dm`, or want to select a harness explicitly:

```sh
dm skills install --harness codex,claude --json
dm skills status --harness codex,claude --json
```

Choose `codex`, `claude`, or `detected` as appropriate. Then initialize a project:

```sh
dm init --directory /path/to/project --agent --json
dm capabilities --json
dm agent context --config /path/to/project/ha-config --no-schema --json
dm --config /path/to/project/ha-config --no-schema --json
```

`capabilities` describes commands, prerequisites, and available result schemas.
`agent context` collects project state and a verification baseline; `--no-schema`
lets it run without Docker and reports native schema verification as incomplete.
From your project directory, `dm test plan --json` proposes relevant tests,
`dm test --changed --json` runs them against development HA, and
`dm agent review --json` flags coverage gaps and other change risks.
Selection uses static heuristics and
recommends broader testing when dependencies cannot be resolved. Pass
`--config PATH` when working with an alternate configuration.

JSON results include statuses, findings, and suggested next actions. Add
`--compact` to supported JSON commands to keep the response small while saving
the full evidence locally for inspection.

`init` preserves existing files and reports the example configuration path. Use
that path for runtime examples. For an existing HA directory, pass `--config` to
`init`; its synthetic helpers are placed in a separate example. For a team skill,
use `dm skills install --harness codex --scope project --project /path/to/project`
after initialization. See the
[agent quickstart and repair workflow](docs/agents.md),
[bundled skill](skills/denmother/SKILL.md), and [installation guide](docs/installing.md).

For clients without shell access, run
`dm mcp --project-root /absolute/project`. The local stdio server exposes context,
validation, test planning, change review, automation inspection, and explicitly
enabled development tests. The host configures targets and credentials; setup
does not register or start the server. See [MCP setup and contracts](docs/mcp.md).

## Use your own configuration

```sh
dm --config /path/to/ha-config --lint
dm --config /path/to/ha-config --entities
dm test --config /path/to/ha-config --dev-url http://localhost:8123 --json
```

Provide the token with `HASS_DEV_TOKEN`; avoid putting tokens in shell history.
Entity validation uses the selected project's `docs/reference/entity-list.txt`
by default. `dm sync` refreshes it from HA and can also report reference
mismatches and orphaned devices. Paths and optional repository policies are
configured in [.denmother.yaml](docs/project.md).
Missing inventories or unavailable schema checks report incomplete verification.

The portable dev environment accepts a **dedicated secret-free development
configuration**. It rejects `!secret` references and secret files. Mock your
hardware through YAML helpers or supply your own isolated runtime; Denmother
does not copy your production storage or custom integrations automatically.

## More tools for your configuration

| Need | Command |
| --- | --- |
| Validate and run fast local tests in one sweep | `dm check --ensure-dev --json` |
| Validate and run the full development suite with traces | `dm check dev --json` |
| Inspect runtime health and identify synthetic states | `dm dev doctor --json` |
| Inspect recent traces | `dm trace automation.study_motion_lamp --json` |
| Correlate traces, logs, and local references | `dm observe automation.study_motion_lamp --json` |
| Refresh local entity/device/area inventories | `dm sync --json` |
| Generate entity autocomplete for VS Code's YAML extension | `dm schema` |
| Compare history with audit specifications | `dm audit --json` |
| Compare supported Inovelli blueprint settings with live HA states | `dm verify --json` |
| Discover Leviton devices over mDNS and compare with HA | `dm discover --json` |
| Check test hygiene and impacted tests | `dm test doctor --json`, `dm test plan --json` |
| Find maintenance tasks with acceptance criteria | `dm agent tasks --json`, `dm agent next --json` |
| Map automations, blueprint usage, entity references, and HomeKit exposure | `dm docs generate` |
| Check consistent YAML list ordering | `dm sort --check` |

Use `--config PATH` to select your configuration for these commands. `sync`,
`audit`, `verify`, and `discover` default to a production target and read HA
without changing it; sync and discovery write local reports. Provide credentials
through environment variables as described in [target selection](docs/project.md).
Production tests require explicit `--allow-prod`, including for loopback URLs
that could be tunnels.

Device verification supports specific recipes, and audit coverage depends on
available history and expectations. Synthetic tests do not establish physical
device behavior. See [runtime recipes](docs/testing.md#runtime-recipes) and
[device verification](docs/compatibility.md#model-specific-read-only-verification)
for supported recipes and known limits.

## Learn and contribute

- [Guide index: choose a workflow](docs/README.md)
- [Installation, upgrades, and troubleshooting](docs/installing.md)
- [Test format and failure behavior](docs/testing.md)
- [Project settings and target selection](docs/project.md)
- [Dashboard development and synthetic example](docs/dashboards.md)
- [Architecture and source map](docs/architecture.md)
- [Compatibility and current limitations](docs/compatibility.md)
- [Contributor guide](CONTRIBUTING.md), [security policy](SECURITY.md)
- [Release procedure](docs/releasing.md), [changelog](CHANGELOG.md), [roadmap](ROADMAP.md)

Licensed under [MIT](LICENSE). Dependency notices are included in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Denmother is an independent
community project and is not affiliated with Home Assistant.
