# Projects and targets

Project settings select your Home Assistant configuration, inventories, dashboard
fixtures, and runtime state. Start with a synthetic example or use an existing
HA repository.

## Initialize a project

After [installing Denmother](installing.md), initialize settings and a synthetic
starter. This does not start Home Assistant or an agent.

```sh
dm init --directory /path/to/project --agent --json
```

`--agent` creates `AGENTS.md`, or `DENMOTHER-AGENT.md` when `AGENTS.md` already
exists. Read or link the latter from existing agent instructions. Managed skill
registration does not require either file; see
[harness registration](installing.md#harness-registration) for team/project scope.

Existing files are preserved. `init --dry-run` lists planned files without
creating them, and initialization refuses symlink destinations. Nested Git ignore
files protect generated runtime credentials and default evidence. Existing ignore
files are preserved and should retain those rules. See [starter selection](#select-a-starter)
for custom configuration paths and the separate example created alongside an
existing HA configuration.

## Select configuration and policy

Project commands select HA YAML with `--config PATH`. The default directory is
`ha-config`. Installation, skill management, version, and capability commands do
not resolve HA project settings or credentials.
A relative `--config` searches the working directory and its ancestors for a
matching directory. Use an absolute path to make selection unambiguous. Without
the flag, discovery first searches for project settings that choose the default
configuration. Paths inside `.denmother.yaml` are relative to its project.
For example, select a different default configuration and inventory directory:

```yaml
version: 1
config_dir: home-assistant
references_dir: inventory
forbidden_integrations: [demo]
```

Unknown settings, unsupported versions, and multiple YAML documents are errors.
Policies apply only to the configuration selected by `config_dir`; selecting
another directory does not inherit that project's restrictions.

| Setting | Default | Purpose |
| --- | --- | --- |
| `config_dir` | `ha-config` | Home Assistant YAML directory |
| `references_dir` | `docs/reference` | Local sync inventories, relative to project |
| `forbidden_integrations` | empty | Optional integration denylist |
| `managed_repository` | false | Opt into additional opinionated documentation and device conventions |
| `fan_light_pair_blueprint` | empty (disabled) | Explicit blueprint path for the managed fan/light device contract |
| `dev_compose_template` | embedded portable template | Advanced replacement Compose template, relative to project |
| `dev_fixtures` | `.devcontainer/dev-state-fixtures.json` | Display fixture file, relative to project; seeded into absent states after portable HA startup |
| `dev_scenarios` | `.devcontainer/scenarios.json` | Named display scenarios, relative to project |

Leave `managed_repository` disabled for normal adoption. It enables conventions
from the repository where Denmother originated, including additional expected
agent documents and device policies. Naming conventions are advisory; the CLI
does not rename entities.
Fan/light contract checks additionally require `fan_light_pair_blueprint`, such as
`example/fan_light_pair.yaml`; no private blueprint is assumed or bundled.
Custom Compose templates own their bootstrap and isolation. The portable
runtime copies YAML and local `www/` web assets into a runtime volume and records
source hashes. Native schema validation rejects a different mount or stale
portable runtime source.
An explicit `HA_CONTAINER` cannot bypass source identity checks. A direct
`/config` bind mount is also supported for externally provisioned containers.

## Choose a Home Assistant target

Use development HA for tests and previews. The portable runtime creates local
credentials automatically; an externally managed instance needs a URL and token.
Prefer environment variables for tokens so they stay out of command history.

Development credentials resolve from explicit `--dev-url`/`--dev-token`, then
`HASS_DEV_URL`/`HASS_DEV_TOKEN`, then legacy development variables, then the
selected project's generated runtime. Production uses `--prod-url`/`--prod-token`,
`HASS_PROD_URL`/`HASS_PROD_TOKEN`, then legacy `HASS_URL`/`HASS_TOKEN`.
Explicit cross-instance flags override the command's default instance.
`test`, `trace`, `logs`, and `observe` default to development; `audit`, `verify`,
`sync`, `discover`, and `dev fixtures` default to production. `check` defaults to
the local workflow; its `dev` and `prod` profiles are explicit. Inspect `--help`
and the result's `target` metadata. Production tests require `--allow-prod` even
for loopback URLs; observation commands read HA without triggering tests.

```sh
dm test --config /path/to/dev-config --dev-url http://localhost:8123 --json
dm observe automation.ENTITY --config /path/to/ha-config \
  --prod-url https://ha.example.com --json
```

Use `HASS_DEV_TOKEN` for the first command and `HASS_PROD_TOKEN` for the second.
Substitute your actual development URL, production URL, and automation ID.

URLs must be absolute HTTP(S), without userinfo, query strings, or fragments.
URL-embedded credentials are rejected before token lookup. HTTPS reverse-proxy
path prefixes are supported by the REST and WebSocket clients.

Generated local credentials live under `.devcontainer/worktrees/` and are
ignored by Git. Tokens are written with mode 0600 and passed through request
headers or subprocess stdin. Keep generated runtime files out of source copies.

## Results

Commands with `--json` emit the `dm.operator.v1` envelope. It contains `status`,
`exit_code`, `summary`, optional `target`, and individual `steps`. A step can
carry findings, artifacts, hints, and structured details. `dm docs generate`
emits the JSON schema under the selected project's `docs/generated/`.
Generated docs and execution plans belong to that project even when invoked
from another working directory. Review and test planning inspect the selected
Git repository (including repositories without commits) and discover actual Go
module roots for test recommendations.

`dm discover --json` emits the same result envelope, and saves its detailed
report under the project's `references_dir`. It matches Leviton advertisements
using entity/device registry identities when available. Missing registry or
mDNS data produces a partial result that identifies any fallback matching method;
unidentifiable Matter advertisements do not establish device matches.

See [dashboard development](dashboards.md) for native HA views, local custom
cards, browser checks, fixture loading, and scenarios.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Failure, including blocked guardrails |
| 2 | Warning, including insufficient observed audit evidence |
| 3 | Partial/incomplete verification |

Root validation supports `--json`; add `--no-schema` for offline checks without
automation tests. `dm check --json` runs a structured validation-and-test workflow.
See [agent integration](agents.md) for capabilities, executable next actions,
error codes, compact output, and context status. Root validation reads the working
tree and does not support `--staged`. `dm sort --staged` selects staged filenames
for sorting; it does not validate a staged snapshot.

## Select a starter

`dm init --directory /path/to/project --example dashboard` creates the two-view
Lovelace project, custom card, fixture and scenario settings.
`--example automations` creates runnable timer, event and blueprint examples;
`--example quickstart` remains the default. Add `--agent` for optional project
instructions. Prefer `dm skills install` for managed skill registration;
`init --skill` remains an unmanaged copy option that preserves existing files
and does not upgrade registrations.
The selected example also supports a custom `--config` directory. Existing files
are preserved. If a home configuration already exists, the starter is placed in
`examples/denmother` (quickstart) or `examples/denmother-EXAMPLE`; use the
`example_config` and commands in the JSON response.

For compatibility testing, set `DM_DEV_HA_IMAGE` to an explicit Docker image
reference before `dev up`. The default remains pinned. Use separate project
copies for different HA versions so an older HA never opens newer runtime storage.
