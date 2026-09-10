# Test authoring

Tests end in `_test.yaml`, usually under `<config>/tests`. Use a stable automation
`id` so Home Assistant can retain execution traces. Prefer a natural state/event
trigger to `automation.trigger` when claiming that the trigger works.

This case assumes `input_boolean.study_motion` naturally triggers
`automation.study_motion_lamp`, which turns on `input_boolean.study_lamp`:

```yaml
version: 1
name: Motion activates the study lamp
tags: [fast]
config:
  timeout: 5s
  cleanup: true
  auto_restore: true
tests:
  - name: Motion turns on the lamp through the automation
    setup:
      - entity_id: input_boolean.study_motion
        state: "off"
      - entity_id: input_boolean.study_lamp
        state: "off"
    trigger:
      - service: input_boolean.turn_on
        target:
          entity_id: input_boolean.study_motion
    assertions:
      - entity_id: input_boolean.study_lamp
        state: "on"
        timeout: 5s
    trace_assertions:
      automation: automation.study_motion_lamp
      expect_completed: true
      expect_no_errors: true
      expect_actions:
        - service: input_boolean.turn_on
          target: input_boolean.study_lamp
    cleanup:
      - service: input_boolean.turn_off
        target:
          entity_id: input_boolean.study_motion
      - service: input_boolean.turn_off
        target:
          entity_id: input_boolean.study_lamp
```

Change entities, expectations, and cleanup to match the intended behavior.
Assert an output changed by the automation and its expected action in a fresh
trace. Checking only the helper set during setup cannot verify the automation.

Declared trace assertions always run. `--trace` adds checks for tests without
declared assertions. The runner excludes trace IDs that existed before triggering.
Default context attribution ignores unrelated runs and rejects ambiguous
attributable runs. For delayed triggers that lose context, `attribution: fresh_unique`
requires `attribution_reason` and exactly one fresh completed run in an isolated window.

Use `expect_no_trace` for a negative trigger case; it cannot be combined with
checks about an executed trace. Use `expect_actions_in_order` only when order is
part of the contract. Equal or missing timestamps do not establish order.

Explicit cleanup runs after failures when `config.cleanup` is enabled. Automatic
restoration covers HA state representations and supported service restoration;
it cannot undo arbitrary external effects. A failed cleanup blocks later cases.
Assertions and cleanup must name transitive automation outputs that need
restoration; the runner cannot discover every side effect from the trigger alone.

Run a test with `dm test FILE --case "case name" --json --config PATH`. An empty
selection never counts as evidence. Inspect failure details before broadening the
run. Refresh the development runtime after editing the automation itself.
