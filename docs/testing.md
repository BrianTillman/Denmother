# Automation tests

Turn an automation's intended behavior into a repeatable test. Denmother drives
a real Home Assistant instance, waits for the resulting states, and checks a
fresh execution trace to show which trigger and actions ran. Tests live beside
your YAML and can be rerun from the terminal, CI, or a coding agent.

For prerequisites and device limits, see [compatibility](compatibility.md).

## Run your first test

After [installing Denmother](installing.md), start a synthetic project with
Docker Engine and Compose v2 running:

```sh
dm init --directory /tmp/denmother-tests --json
cd /tmp/denmother-tests
dm dev up --json
dm --json
dm test --json
dm dev down --json
```

Use a fresh directory. The example turns a motion helper on, checks that the lamp
helper follows, verifies the automation's service call in a fresh trace, and
restores test state. It also checks restoration of automation enablement.
Rerun `dev up` after editing configuration YAML. `dev down` stops the runtime and
preserves its state; `dev reset` discards it.

## Write a test

Tests are YAML files ending in `_test.yaml`, normally under `<config>/tests`.
The [complete example](../examples/quickstart/ha-config/tests/presence/motion_lamp_test.yaml)
pairs an automation with a runnable test. Each file declares `version: 1`, `name`,
optional `tags`, a `config`, and `tests`.

Each case supports `setup`, service-call `trigger`, `events`, state/attribute
`assertions`, `trace_assertions`, `cleanup`, and optional `subtests`. Prefer a
natural state or event trigger and assert an output changed by the automation.
The [bundled test-authoring example](../skills/denmother/references/testing.md)
shows a complete recipe you can adapt.

Setup actions specify `entity_id`, `state`, and optional `attributes` and
`method`. Service calls use `service: domain.service`, optional `target.entity_id`,
`data`, and `delay`. Assertions accept `entity_id`, `state`, optional attribute
predicates, and a duration such as `timeout: 5s`. State and attributes must match
on the same observation; polling continues until both match or time expires.
State assertions accept exact values, numeric comparisons, `contains`, and
`matches` (regular expressions). Attribute assertions accept exact values and
comparison expressions.

## Select tests

From the selected project directory, or with `--config PATH`:

| Selection or check | Command |
| --- | --- |
| Whole suite | `dm test --json` |
| One file | `dm test ha-config/tests/presence/motion_lamp_test.yaml --json` |
| Cases whose names match | `dm test FILE --case "Motion triggers" --json` |
| Files tagged for the fast suite | `dm test --tags fast --json` |
| Explain which tests a Git diff affects | `dm test plan --json` |
| Run the diff-selected tests | `dm test --changed --json` |
| Inspect test structure and coverage hygiene | `dm test doctor --json` |
| Validate and run the full development suite with traces | `dm check dev --json` |

An empty selection never counts as a tested pass. Diff selection uses static
heuristics; follow its broader-run recommendations when dependencies are dynamic.
`test doctor` does not execute the suite. Helper-only assertions verify setup or
helper behavior; use a natural trigger and fresh trace for automation coverage.
Use `dm observe automation.ENTITY --json` to investigate runtime failures with
traces, logs, and local references. Preserve the selected configuration and target.

## Timer, event and blueprint examples

```sh
dm init --directory /tmp/denmother-automations --example automations --json
dm dev up --config /tmp/denmother-automations/ha-config --json
dm --config /tmp/denmother-automations/ha-config --json
dm test --config /tmp/denmother-automations/ha-config --json
dm dev down --config /tmp/denmother-automations/ha-config --json
```

The timer example waits for a real `timer.finished` event. The event example
fires `denmother_demo` with matching event data. The blueprint example uses a
local blueprint and a helper state transition. Each verifies the resulting lamp
helper and a fresh completed trace, then cleans up through services. Recorder and
logbook are enabled for read-only observation. No device pairing is required.

## Runtime recipes

| Recipe | Implementation and evidence | Boundary |
| --- | --- | --- |
| Helper state trigger | Invoke a native helper service, assert the resulting state, inspect the automation trace. | State and integration behavior are exercised in the selected HA instance. |
| Explicit event trigger | Fire the event through HA's WebSocket API, assert the resulting state, inspect its context-linked trace. | The event is synthetic; radio or device delivery is not exercised. |
| Timer expiry | Start a real timer and wait for `timer.finished`. Use documented `fresh_unique` attribution where HA drops the start context. | A single isolated automation run is required for the whole observation window. |
| Blueprint automation | Load its blueprint and inputs in HA, exercise a natural trigger, assert state and trace. | Static `!input` analysis alone cannot establish the instantiated behavior. |
| Device/area service target | Expand selectors with HA's `extract_from_target`, then freeze the matching service-domain entities before setup. | Live registry access is required. Unsupported command, unknown ID, or empty service-domain expansion fails before mutation. |
| Direct/mock state seed | Use `setup.method: direct` or `mock_entities.set_state`. | Only HA's state-machine representation changes; this does not create hardware integration behavior. |
| Arbitrary Jinja target | HA evaluates the automation's Jinja; inspect its resulting trace and entity assertions. | Test recipes themselves do not evaluate arbitrary Jinja. Static plans explain unresolved references and recommend a broader run. |

Service targets accept either a string or a list for `entity_id`, `device_id`,
and `area_id`. Historical `entities`, `devices`, and `areas` list aliases also
work. A selector cannot be specified in both `target` and `data`. Area/device
expansion is frozen for the test's trigger and cleanup actions so subsequent
registry changes do not silently add entities beyond the snapshot. `data`
selectors are also expanded. Native HA extraction determines group membership
and primary-entity behavior. A device without an exposed entity for the service
domain cannot be exercised by this route.

```yaml
trigger:
  - service: light.turn_on
    target:
      area_id: [test_room]
assertions:
  - entity_id: light.test_lamp
    state: "on"
cleanup:
  - service: light.turn_off
    target:
      area_id: test_room
```

Area/device selectors in recorded trace actions are expanded through HA before
matching `expect_actions.target` against an entity. This uses registry membership
at observation time; tests should not edit registries during a case. Static
`test doctor` reports that registry access is needed to determine cleanup targets.

## Verify the execution trace

A trace assertion names `automation: automation.entity_id`. Give the automation
a stable YAML `id` so HA can retain traces. Declared assertions run automatically;
`--trace` requests additional trace checks for tests without a declaration.
Supported checks:

- `expect_completed` and `expect_no_errors`.
- `expect_branch`: the automation trigger ID.
- `expect_actions`: required service, target, and optional data.
- `expect_actions_in_order`: required actions in execution timestamp order.
- `reject_actions`: service calls that must not appear.
- `expect_no_trace`: no fresh run during the observation window; incompatible
  with assertions about an executed run.

Unavailable trace evidence fails a declared trace check. Ordered assertions use
execution timestamps, including repeated/parallel paths; equal or missing times
cannot prove order.

## Trace attribution

The default `context` mode captures the context returned by each WebSocket
`call_service` or `fire_event`. A trace must have that context ID or its immediate
parent ID. Baseline run IDs exclude prior cases; unrelated simultaneous runs are
ignored. Expected actions never select a passing run in place of an attributable
failure. Multiple attributable runs are an error unless the expected trigger
branch uniquely identifies one.

For a case with several actions, `trace_assertions.trigger_index` selects a
zero-based action index: service entries under `trigger` first, then `events`.
Splitting independent actions into separate cases usually makes failures easier
to interpret. Context IDs and the attribution mode are included in JSON case
evidence. Missing context information fails; Denmother does not silently fall
back to timestamp matching for normal traced cases.

Delayed integrations and some custom components may create a new context. An
explicitly isolated recipe can instead declare:

```yaml
trace_assertions:
  automation: automation.test_timer
  attribution: fresh_unique
  attribution_reason: >-
    timer.finished drops timer.start context; this dedicated timer and
    automation have no other triggers in the isolated development instance.
  expect_branch: expired
  expect_completed: true
  expect_no_errors: true
```

`fresh_unique` observes the entire three-second trace window, retains all newly
observed run IDs even if HA evicts one, and requires exactly one completed new
run. A second run fails the check. This mode cannot link the run to a command
context; the test author must ensure that no other trigger can fire.
`attribution_reason` is required, reported in JSON, and surfaced by
`test doctor`; it cannot be combined with `trigger_index`. `expect_no_trace` in
context mode checks attributable new runs; in `fresh_unique` mode it checks that
no new run appears during the window.

HA's [WebSocket API](https://developers.home-assistant.io/docs/api/websocket/)
provides service-call contexts. Its
[command implementation](https://github.com/home-assistant/core/blob/dev/homeassistant/components/websocket_api/commands.py)
also returns event contexts and exposes native target extraction. Context
propagation through external integrations, long chains of other automations, and
trace retention are integration/runtime constraints, not guarantees made by the
test runner. A service call accepted by HA does not prove a physical action.

## Cleanup support

With `config.cleanup: true`, explicit cleanup runs after each case, including
setup, trigger, assertion, or trace failures. Automatic restoration follows
explicit cleanup; `auto_restore` defaults to true. Snapshots include explicit
setup entities, frozen action targets, assertion entities, and explicit cleanup
targets. They cannot discover every entity affected transitively by an automation:
name those outputs in assertions or declare their cleanup explicitly. Unrelated
message/URL values in service payloads are not treated as entity targets.

| Domain | Automatic service restoration |
| --- | --- |
| `automation`, `input_boolean`, `switch` | `turn_on` / `turn_off` |
| `input_number`, `number`, `counter` | Numeric value via `set_value` |
| `input_text`, `text` | Text via `set_value` |
| `input_select`, `select` | Selected option via `select_option` |
| `input_datetime` | Date, time, or datetime via `set_datetime` |
| `timer` | Cancel idle/elapsed timers; start remaining time and pause paused timers |
| `light` | Power; for an on light, brightness, effect, and the active color representation |
| `fan` | Power; for an on fan, percentage or preset plus recorded oscillation/direction |
| `cover` | Recorded position/tilt, or stable open/closed state |
| `lock` | Stable locked/unlocked state |
| `climate` | HVAC mode, recorded temperature/range, preset, fan, and swing modes |

Explicit direct/mock seeds restore with the state API. Newly created state
records are deleted. Sensors and other representation-only domains retain state
API restoration. Unsupported control domains (`vacuum`, `media_player`,
`alarm_control_panel`, `water_heater`, `humidifier`, `siren`, `script`, `scene`,
`button`, `input_button`) require an explicit cleanup recipe with
`config.auto_restore: false`; automatic snapshot setup rejects them before
mutation unless explicitly seeded as direct/mock states.

Elapsed timer events cannot be replayed reliably. Restoration does not rewind
notifications, media queues, locks requiring private unlock codes, integration
configuration, physical motion, or other hidden side effects. An off light/fan's
remembered settings are not reconstructed by toggling it on. Service failures are
reported, later cleanup is still attempted, and errors are aggregated. Failed
cleanup blocks subsequent cases until a new runner is created. Cancellation
provides a separate 30-second cleanup deadline. Use synthetic entities or a
dedicated development installation for mutation tests.

## Opt-in native runtime acceptance

The repository includes `TestNativeRuntimeTargetsAndCleanup`, which skips unless
`DENMOTHER_RUNTIME_TEST_CONFIG` names a running project's development config.
It resolves only that project's local credentials and requires a loopback URL.
The instance must load `input_boolean`, `input_number`, `input_select`,
`input_text`, and `counter`; the automation example enables these components.

```sh
DENMOTHER_RUNTIME_TEST_CONFIG=/absolute/path/to/examples/automations/ha-config \
  go test -race -v ./internal/hatest -run '^TestNativeRuntimeTargetsAndCleanup$' -count=1
```

This test creates one uniquely named area and six native helpers using HA's
storage/registry APIs. It checks exact area extraction, service-domain filtering,
five helper mutations, automatic snapshot restoration after both passing and
failing assertions, and an untouched helper outside the area. Incrementing the
number/counter afterward verifies their integration-owned values were restored,
beyond checking the state-machine representation. Independent teardown deletes
the seven created records and checks that their registry entries, states, and
area are gone. It does not restart HA or operate physical devices.

CI runs this acceptance against HA **2026.9.0** and **2026.8.3**; inspect the
result for the revision being evaluated. Native device-ID expansion is covered
by mocked registry responses; this synthetic fixture does not create an
integration-owned device. Physical control domains in the
cleanup table retain mocked service-payload coverage, not hardware validation.
