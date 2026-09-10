# Installing Denmother

One setup command installs `dm` and its matching agent skill. Start from source,
use a local archive offline, or register the embedded skill from an existing
binary. Ownership checks preserve your edited instructions during upgrades.

## Install from source

The first public release and Homebrew tap have not been published. From a source
checkout, install the CLI and skills for detected harnesses:

```sh
./setup --from-source
```

The bootstrap implements Linux/macOS amd64 and arm64 installation. It requires
Bash, Python 3.9+, and the Go toolchain specified in [go.mod](../go.mod) for source
builds. See [compatibility](compatibility.md) for platform tests and prerequisites.
Setup defaults to `$HOME/.local/bin/dm`; follow its PATH guidance before continuing.
Use `--binary-only` for just the CLI or `--harness codex,claude` to select skills.

Try your first offline check in a fresh project:

```sh
dm version --json
dm init --directory /tmp/denmother-first-project --json
dm --config /tmp/denmother-first-project/ha-config --no-schema --json
```

Installation is separate from project initialization and starting Home Assistant.
For runtime tests, continue with [automation testing](testing.md); for browser
previews, use the [dashboard starter](dashboards.md). Docker is required for the
portable runtime, and Node.js/npm are additionally required for browser rendering.

## Inputs and plans

| Input | Command | What you need |
| --- | --- | --- |
| Source checkout | `./setup --from-source` | Matching Go toolchain and dependencies |
| Existing executable | `./setup --binary /path/to/dm --offline` | A compatible native binary |
| Local release archive | `./setup --archive /path/to/denmother-0.1.0-rc.1-linux-amd64.tar.gz --offline` | Archive and adjacent `SHA256SUMS`, or an explicit checksum |
| Unpacked release | `./setup` | The verified package's companion binary |
| Published version, when available | `./setup --version 0.1.0-rc.1` | Matching release assets accessible over HTTPS |

Local archives must use `denmother-VERSION-OS-ARCH.tar.gz` naming. Supply
`--checksum PATH` or `--sha256 HEX` if not using adjacent `SHA256SUMS`; these
options are mutually exclusive and apply only to local archives. The bootstrap
checks the checksum, archive entries, executable header, version, and platform
before installation. It extracts only verified bytes. Release-origin checksums
establish integrity against the published checksum, not independent publisher identity.

Source builds remain `dev` and record Git revision/dirty state; `--version` does
not stamp a source build. With no local input or version, setup resolves the
latest stable GitHub release. `--repository OWNER/REPO` selects a fork. Download
mode requires published assets; use `--from-source` to build a checkout.

`--offline` prohibits downloads. Offline source builds also require cached
dependencies and a local toolchain: setup sets `GOPROXY=off`, `GOSUMDB=off`, and
`GOTOOLCHAIN=local`. Skill registration itself is always local.

Preview destinations using an available binary:

```sh
./setup --binary /path/to/dm --dry-run --json
dm setup --dry-run --json
```

Dry runs create no directories and do not build or download. Without a binary to
run the planner, bootstrap dry-run returns a partial acquisition plan (exit 3)
with destination verification deferred. `--bin-dir PATH` selects another binary
directory. The compatibility entry point `scripts/install.sh` accepts
`--directory` and `--archive-dir` for binary-only setup.

## Harness registration

If `dm` is already available, install or inspect its matching skill directly:

```sh
dm skills install --harness codex,claude --json
dm skills status --harness codex,claude --json
```

Use `codex`, `claude`, or the default `detected`. Repeated `--harness` flags and
comma-separated values are accepted. Detection uses an executable on PATH,
nonempty harness configuration, or an existing registration manifest; an empty
configuration directory alone does not select a harness. Explicit selection lets you register
before configuring the client. No detected harness produces an advisory.

Denmother's adapters use these locations:

| Harness | User skill directory | Project skill directory | Configuration evidence |
| --- | --- | --- | --- |
| Codex | `$HOME/.agents/skills/denmother` | `PROJECT/.agents/skills/denmother` | `codex` on PATH or nonempty `$CODEX_HOME/config.toml` (default `$HOME/.codex`) |
| Claude Code | `$CLAUDE_CONFIG_DIR/skills/denmother` (default `$HOME/.claude`) | `PROJECT/.claude/skills/denmother` | `claude` on PATH or nonempty configuration `settings.json` |

`CODEX_HOME` affects Codex configuration detection, not the adapter's user skill
destination. Consult [Codex's skill documentation](https://learn.chatgpt.com/docs/build-skills)
or [Claude Code's skill documentation](https://code.claude.com/docs/en/skills)
for the client's loading behavior. Start a fresh session when checking discovery;
a successful registration does not prove an existing session loaded the skill.

For a team registration in an existing project:

```sh
dm skills install --harness codex --scope project --project /path/to/project --json
dm skills status --harness codex --scope project --project /path/to/project --json
```

Project scope requires an explicit existing `--project` root; `--config` does not
choose this destination. Each teammate needs a compatible `dm` on PATH. Manifests
record absolute installation destinations, so a moved checkout needs its
registration checked again. `--destination '/custom path/denmother'` selects an
exact skill directory for one harness. Repeat all selection options for status,
recovery, and uninstall. Binary and skill directories must not overlap.

Setup does not edit `AGENTS.md` or `CLAUDE.md`, configure MCP, or launch an agent.
Use `dm init --agent` for optional project instructions. The older `init --skill`
option copies to `.agents/skills/denmother` without an ownership manifest. Repeated
initialization preserves that copy without upgrading it. Use managed registration
for upgrades.

## Ownership and recovery

Identical installs are skipped. Upgrades replace unchanged owned files while
preserving unrelated contents. Edited files, foreign directories/binaries, and
symlink destinations block replacement with paths and recovery guidance.
Ownership is recorded in the skill's `.denmother-install.json` and the binary's
`BIN_DIR/.dm.denmother.json`. These are local installation records.

For a conflict, preserve your edits, then restore the originally installed
content, move the conflicting directory aside, or choose a new destination.
There is no force-overwrite mode. An unmanaged `init --skill` copy must be moved
aside before a managed skill can occupy the same directory.

Interrupted operations retain a `MANIFEST.transaction` journal and report applied
and remaining files. After the other installer has stopped, repeat the original
command with `--recover`. Recovery validates the journal, restores the previous
version, and replans. Subsequent edits block recovery and remain preserved.
For example:

```sh
dm skills install --harness codex --scope project --project /path/to/project --recover --json
```

Keep recovery journals private: they include previous file contents. OS locks
release when an installer exits, although lock files persist; do not remove a
lock file while installation may be active. File replacement is atomic per file,
not across a complete multi-harness install. See
[installer internals](architecture.md#installer-internals) for metadata limits,
locking, and transaction validation.

`dm skills uninstall` removes unchanged owned skill files and preserves edited
or unrelated contents. It reports leftovers and retains records needed to retry;
it does not remove the CLI, project settings, credentials, or runtime state.
Repeat the original harness/scope/destination selection when uninstalling.

For a Homebrew package, upgrade through its actual tap with
`brew upgrade OWNER/TAP/denmother`, then rerun `dm skills install` to update the
embedded guidance. Setup preserves Homebrew Cellar and Nix store binaries and
reports package-manager PATH conflicts. Use that manager for binary upgrades.

## Results and troubleshooting

JSON commands use `dm.operator.v1`; inspect `details.installation` for detection,
destinations, operations, conflicts, and verification. Build progress goes to
stderr. Capture stdout even on a nonzero exit. Installer commands reject
`--compact` and `--evidence-dir`; redirect stdout to save a report.

| Exit | Meaning and next step |
| --- | --- |
| 0 | Complete installation/verification or a complete valid plan |
| 1 | Input, integrity, ownership, or permission failure; inspect the affected operation |
| 2 | Advisory such as PATH shadowing, no detected harness, or uninstall leftovers |
| 3 | Incomplete acquisition, installation, or verification; inspect applied and remaining work |

Stable errors include `unsupported_platform`, `version_mismatch`,
`checksum_failure`, `unsafe_archive`, `destination_conflict`, `permission_failure`,
and `incomplete_verification`. Earlier applied operations remain in failed
reports. After interruption or missing final output, inspect `dm skills status`
with the original selectors before retrying. Registration status checks files,
compatibility, and PATH; `harness_invoked` remains false.

## Verification

`skills status` checks files and compatibility; it does not establish that a
client session loaded the skill. Use the [release acceptance procedures](releasing.md#installer-acceptance)
to check installation outside the checkout, ownership and recovery, and fresh
client discovery and invocation. See [compatibility](compatibility.md#platform-and-integration-coverage)
for platform coverage and remaining gaps.
