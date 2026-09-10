---
name: denmother
description: Validate, test, and diagnose Home Assistant YAML automations with Denmother. Use when changing HA automations or blueprints, writing automation tests, investigating failed triggers or traces, or checking a development dashboard in a project that uses or is adopting Denmother.
license: MIT
compatibility: Requires the dm CLI. Runtime tests require an isolated Home Assistant instance; the portable runtime uses Docker Engine and Compose v2.
metadata:
  version: "1.0.0"
  denmother-min-version: "0.1.0"
  result-schema: dm.operator.v1
  capabilities-schema: dm.capabilities.v1
---

# Denmother

Use Denmother to validate, test, preview, and diagnose the user's Home Assistant
change. Keep work scoped to the request; maintenance suggestions from
`agent tasks` and `agent next` do not authorize additional work.

## Establish the project and baseline

Find `dm` on PATH or use the provided binary path. Run `dm version --json` and
`dm capabilities --json` to inspect the installed interface. For detailed flags,
use `dm capabilities --command "test" --json` (or another command/group); add
`--schemas` when the full result schema is needed. Development and
0.1.0 prerelease builds may include these features; test capabilities instead of
assuming support from a version string alone. If a command is unavailable,
inspect its `--help` and report the limitation.

Use the project's `.denmother.yaml`, or pass `--config PATH` on every project
command. `dm agent context --json` reports configuration and project paths,
credential presence, runtime availability, and baseline verification. Context
collection can succeed while verification is partial. Use `--no-schema` when
collecting context without Docker; native verification remains incomplete.
Read the baseline and `verification_status` before reporting which checks passed.

For a new project, `dm init --directory PATH --agent --json` creates a synthetic
example and optional instructions. Inspect its reported example path: an
existing HA configuration gets a separate example, without inserted helpers.
Existing files are preserved. Binary installation and harness registration are
separate from initialization.

When skill registration or upgrade is requested,
`dm skills install --harness detected --json` installs this binary's matching
skill. Use an explicit `codex` or `claude` selection when needed; project scope
requires both `--scope project` and `--project PATH`. Inspect the same selection
with `dm skills status --json`. Status verifies registration and compatibility,
not whether this session loaded the skill. Preserve local edits and follow
reported ownership conflicts. `init --skill` remains an unmanaged copy option,
with no upgrade behavior.

Run `dm --no-schema --json` for offline YAML, structure, policy, and static entity
checks. This does not run tests or native HA schema verification. Missing entity
inventories leave verification incomplete; dynamic references remain outside
the static check's scope and need runtime evaluation.

## Change and verify

Inspect the relevant YAML and existing tests. Read
[test authoring](references/testing.md) when adding or changing tests.
`dm test plan --json` identifies impacted tests from the diff;
`dm test doctor --json` checks test hygiene without executing HA behavior.
Diff selection is a heuristic: select an explicit test when the dependency is
dynamic or context reports `git.available: false`.

For runtime verification, use a dedicated, secret-free development configuration.
The portable runtime accepts YAML helpers, not production secret files or storage.
Use `dm dev up --json` with that configuration, and rerun it after YAML changes
to refresh the mounted source. Run the selected `dm test FILE --json` or
`dm test --changed --json`. Run native validation with `dm --json` when that
configuration is mounted in the running HA container.

Tests change HA state. Preserve the user's selected environment and the CLI's
target metadata. Do not introduce `--allow-prod` as a workaround for a blocked
run. Credentials belong in the appropriate environment variables, not command
arguments, output, committed files, or the skill.

On failures, read [diagnosis](references/diagnosis.md). Fix the demonstrated cause
and rerun the affected checks. If evidence or a prerequisite remains unavailable,
report that limitation instead of weakening assertions to obtain a pass.

For dashboard work, read [dashboard verification](references/dashboards.md).

## Consume results

JSON commands return `dm.operator.v1`: exit 0 success, 1 failure, 2 warning,
3 partial/incomplete. A nonzero exit can still carry a useful JSON result. Check
the selected check scope, individual steps, findings, and target alongside the
top-level status. Static success does not establish automation or device behavior.

Prefer `steps[].next_actions` when available. Pass `executable` and `args`
directly with `cwd`; do not execute them through a shell. Check `mutation_scope`,
`requires_human`, and `required_env` against the user's authorized task. A command
recommendation is guidance, not permission. Templates without concrete arguments
may have display commands but no executable action.

For large diagnostic output, use `--json --compact --evidence-dir PATH`.
Installer commands reject the evidence flags; capture their JSON stdout instead.
The `evidence` path contains the full result; `truncated` marks abbreviated
details. Inspect the full artifact before concluding that an omitted finding or
trace is absent. `test doctor --compact` retains its older grouped-summary
behavior; use `--evidence-dir` without that flag for its full per-case evidence.

Report what changed, which checks ran, their outcomes, and any unverified
behavior. Cite local evidence paths when useful. If you started a disposable
runtime, stop it with `dm dev down --json` when finished; down preserves state.
