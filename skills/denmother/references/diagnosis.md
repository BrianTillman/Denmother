# Failure diagnosis

Preserve the selected project and target while moving between validation, tests,
and `observe`.

Start with the failing step and its error code. `invalid_arguments` requires
checking installed command help; `invalid_project` requires correcting project
selection or settings. `validation_failed` means inspect the per-check findings.
`verification_incomplete` means required evidence was unavailable. Generic
`step_failed` or `command_failed` requires inspecting the step's summary/details.
Inspect the result before retrying an operation that changes HA state.

For installation failures, inspect `details.installation` and the applied and
remaining operations. `destination_conflict` preserves foreign or edited files;
back up edits before choosing another destination or restoring owned content.
`incomplete_verification` is the installer's code for incomplete checks or an
interrupted transaction. `dm skills status` is read-only; repeat the harness,
scope, project, and destination selectors used during installation. Use
`--recover` only for the reported interrupted owned transaction, after confirming
the other installer has stopped. Registration status does not prove skill loading
by an active session. Capture JSON stdout: installer commands reject the compact
and evidence-directory flags.

For a state or trace assertion failure:

1. Confirm that the runtime uses the selected configuration and refreshed YAML.
2. Confirm the setup established the precondition needed for a natural trigger.
3. Inspect the test's trace failures and state observations.
4. If needed, use `dm observe automation.ENTITY --json` for traces, logs, and
   source references, preserving `--config` and the selected HA URL/instance.
   Supply credentials through `HASS_DEV_TOKEN` or `HASS_PROD_TOKEN`.
5. A particular run can be inspected with `dm trace automation.ENTITY --run-id ID
   --json`. Do not use an older successful trace as evidence for the failing test.

For large results, add `--compact --evidence-dir artifacts/diagnosis` and open the
returned full evidence file when the excerpt is insufficient. Artifacts can
contain home state, traces, paths, and logs; keep them local unless sharing was
requested and their content was reviewed.

If Docker is unavailable, offline validation and test hygiene can still provide
useful findings. Report runtime and native schema checks as not performed. Do not
switch to the production instance to fill the gap.
