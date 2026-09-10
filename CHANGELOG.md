# Changelog

## Unreleased

The first standalone version brings validation, automation testing, dashboard
development, and agent tooling into one local CLI. A public release and Homebrew
tap have not yet been published; use the [source installation](docs/installing.md).

### Build and test

- Catch YAML, structure, entity-reference, and optional policy mistakes; use HA's
  native schema check against the selected development source.
- Start isolated HA runtimes with generated credentials and separate state for
  each configuration/worktree. Initialize synthetic quickstart, timer/event/
  blueprint, and dashboard projects without physical devices.
- Run YAML automation tests that wait for states and attributes, attribute fresh
  traces, check expected or rejected actions, and verify action order.
  Expand device/area service targets through HA and attempt configured cleanup
  after failures or cancellation.
- Plan tests from Git changes, run the affected selection, and inspect static
  test hygiene with `test doctor`.

### Preview and diagnose

- Render native YAML and saved development dashboards, check every selected
  view, and save screenshots. Preview named scenarios and synthetic fixtures,
  including a local custom-card example.
- Combine traces, logbook, error logs, and source/test references with `observe`.
  Audit historical outcomes against specifications without triggering automations.
- Sync entity/device/area inventories, find reference mismatches and orphaned
  devices, generate entity autocomplete, and map configuration relationships.
- Compare supported Inovelli recipe expectations with exposed HA states and
  discover Leviton advertisements over mDNS.

### Integrate and distribute

- Supply versioned JSON results, command/schema discovery, diff review, suggested
  next actions, compact local evidence, and a bundled agent skill.
- Expose focused local MCP tools with host-selected targets, typed arguments,
  confined evidence resources, credential redaction, and serialized development
  tests. Missing worker results report cleanup as unverified.
- Install the CLI and matching skills with archive checksum checks, ownership
  protection, offline inputs, and interrupted-upgrade recovery.
- Package stamped binaries for five platform targets, checksums, and an optional
  Homebrew formula. CI defines source, HA runtime, and native archive checks.

See [compatibility](docs/compatibility.md) for verification targets and limits.
