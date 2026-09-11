# Compatibility and supported workflows

Choose a check, review its prerequisites, and see which platforms and device
recipes are covered. The first public release and Homebrew tap are not yet
published; start with [source installation](installing.md#install-from-source).

## Prerequisites

| Workflow | Requirements |
| --- | --- |
| Source build | Go 1.26.8, pinned in [go.mod](../go.mod) |
| Unix setup bootstrap | Linux/macOS amd64 or arm64; Bash and Python 3.9+ |
| Offline checks and project initialization | Native `dm` binary and local files |
| Diff review and test planning | Git |
| Portable development HA | Docker Engine and Compose v2; dedicated development YAML |
| Browser dashboard rendering | Node.js/npm, Playwright/Chromium prepared by `dm`, and host browser libraries |
| Native archive acceptance script | Python 3.12+ for safe archive extraction |

The portable runtime defaults to HA **2026.9.1** and nginx **1.29.8-alpine**.
The CI configuration in `.github/workflows/ci.yml` includes HA **2026.9.0** and
**2026.9.1** runtime jobs and the platform jobs below. Check their results for
the revision you intend to use.

## Choose a check

| Question | Use | What the result establishes |
| --- | --- | --- |
| Is this YAML/reference valid? | `dm --no-schema` | Syntax, structure, policy, and static references within the reported scope |
| Does HA accept the loaded configuration? | `dm` with a matching development container | HA's native schema check against the selected source |
| Does this automation respond as intended? | `dm test` with state and trace assertions | Behavior of the tested case in the selected HA runtime |
| Does this dashboard render? | `dm dev dashboard --render` with a dashboard argument | Browser/card/resource checks and screenshots for the selected views |
| What happened in an existing installation? | `dm observe`, `dm audit` | Available trace/log/history evidence and declared audit expectations |
| Which tests should follow this edit? | `dm test plan`, `dm agent review` | Static diff/dependency recommendations and coverage gaps |

## Configuration and runtime boundaries

- Static analysis extracts known YAML entity references and reports dynamic
  Jinja/`!input` targets for runtime evaluation. Generated entity graphs and test
  plans cannot establish every dynamic dependency.
- The portable runtime accepts a dedicated configuration with no `!secret`
  references or secret files. It copies YAML and local `www/` assets, excluding
  tests and custom components. Its HA container has an internal network; hardware
  integrations needing network access require your own maintained template.
- Native schema checks require the selected source mount and current portable
  source hashes. Custom templates must account for their own transformations,
  bootstrap, and isolation.
- Automation tests depend on HA's trace retention and context propagation.
  Cleanup restores supported controls but cannot undo every external effect.
  See [trace attribution](testing.md#trace-attribution) and
  [cleanup support](testing.md#cleanup-support) for the test contracts.
- Dashboard fixtures and scenarios create transient display states. They help
  assess appearance; helpers and real services supply automation behavior tests.
  Use `dev doctor` to identify states created by fixtures and scenarios.

Leave `managed_repository` disabled for ordinary projects. It opts into
additional documentation and device conventions from Denmother's original
repository; see [project settings](project.md#select-configuration-and-policy).

## Platform and integration coverage

| Surface | Implemented or configured coverage | Additional acceptance needed |
| --- | --- | --- |
| Linux x86-64/ARM64 CLI | Native archive smoke jobs on `ubuntu-latest` and `ubuntu-24.04-arm` | Passing jobs for the release revision; HA runtime evidence on ARM64 |
| macOS x86-64/ARM64 CLI | Native archive smoke jobs on `macos-15-intel` and `macos-15`; separate Homebrew job | Passing jobs for the revision; Docker Desktop runtime acceptance |
| Windows x86-64 CLI | Native archive smoke job on `windows-latest` | Passing job; Docker Desktop paths/volumes; no native Windows bootstrap is supplied |
| Integrated setup | Source/archive, ownership, recovery, offline/dry-run, PATH, and local HTTPS fixtures | Bootstrap execution on each claimed Linux/macOS host |
| Codex/Claude skill registration | User/project/custom-home fixtures, upgrades, uninstall, and resource survival | Fresh authenticated client discovery and invocation by version/platform |
| HA automation runtime | Timer/event/blueprint tests, native area/helper targets and cleanup in both HA lanes | Passing runtime jobs on the release revision |
| Lovelace dashboards | YAML and saved-storage workflows, scenarios, and browser/card regressions | Additional custom cards and browser/viewport combinations |
| Physical devices | Mocked registry/mDNS responses and specific read-only recipes | Model, firmware, integration, and network observations on actual hardware |

Cross-builds establish compilation; native archive tests establish CLI behavior;
HA tests exercise the selected runtime. Each result should name that scope.
No minimum supported agent-client version is established by installer fixtures.

## Verify on your host

Run from a source checkout against a built archive, using a new output directory:

```sh
python3 scripts/platform-acceptance.py --archive-dir dist --directory /tmp/dm-acceptance
```

On Windows, use `python` and a writable path such as `C:\Temp\dm-acceptance`.
The script verifies checksums, extracts the matching native archive, initializes
all starters outside the checkout, validates them offline, checks preservation
on repeat initialization, and verifies an intentionally broken entity fails.
`acceptance.json` records platform, binary checksum, commands, durations, and
results. `--dm /path/to/dm` tests an existing native binary instead.

Archive checks do not launch HA, setup, or an agent session. Use
[installer acceptance](releasing.md#installer-acceptance) and
[client loading verification](releasing.md#live-harness-smoke-tests) for those checks.

For runtime acceptance, initialize fresh `quickstart`, `automations`, and
`dashboard` projects and follow the [release runtime commands](releasing.md).
Set `DM_DEV_HA_IMAGE` before startup to test another image. Use separate project
copies for each HA version so older HA never opens newer runtime storage.
Retain the exact revision, binary checksum, host, commands, outcomes, and reviewed
evidence. A skipped or partial check remains unverified. See the
[release checklist](releasing.md#release-checklist-and-evidence).

## Model-specific read-only verification

`dm verify` helps find settings drift by comparing currently exposed HA states
with expectations derived from supported blueprint recipes. It does not publish
device settings, pair devices, or query firmware directly. Its current routing
recognizes these blueprint path fragments:

- `z2m_inovelli_blue_dimmer_settings`, `z2m_inovelli_mmWave_dimmer_settings`,
  `z2m_inovelli_dimmer_settings`, `z2m_inovelli_fan_canopy_settings`: legacy
  MQTT SET/GET expectation extraction or the declarative settings-list format.
  Declarative dimmer settings validate `VZM31-SN` and `VZM32-SN` inputs. Entity
  mapping depends on the Zigbee2MQTT friendly name and parameter conventions.
- `matter_inovelli_dimmer_settings` and
  `matter_inovelli_fan_canopy_settings`: explicit parameter entity/value inputs.
  Only the mapped inputs are checked; omitted optional inputs do not gain coverage.

Blueprint paths select the verification recipe; they do not identify the device
model. Arbitrary Jinja expressions are unsupported. Unsupported recipes are
skipped, and zero verified devices is not a pass. Run
`verify --dry-run --json` to inspect expectations before live observation.

`dm discover` compares Leviton mDNS advertisements with HA registry identities.
Neither a matching verification recipe nor synthetic test success establishes
physical compatibility. Record observations for the specific model, firmware,
and integration using the procedure below.

## Physical-device read-only acceptance procedure

This repeatable procedure gathers evidence without issuing HA service calls,
writing device settings, pairing, restarting, or changing registries. Use a
separate local artifact directory. The commands write local reports/reference
files and read an explicitly selected existing installation.

1. Record Denmother and HA versions, device model/firmware, integration version,
   transport, and the exact recipe file. Keep credentials in environment
   variables (`HASS_PROD_URL`, `HASS_PROD_TOKEN`), never report text.
2. For a supported Inovelli recipe, inspect expectations, then read live values:

   ```sh
   dm verify --config /path/to/ha-config /path/to/device_settings.yaml --dry-run --json
   dm verify --config /path/to/ha-config /path/to/device_settings.yaml --json
   ```

   Record checked/skipped counts, missing or unavailable entities, each parameter
   mismatch, and option validation. Compare disputed values with HA's existing
   entity attributes and the integration's documentation without changing them.
3. For Leviton discovery on a permitted local network:

   ```sh
   dm discover --config /path/to/ha-config --timeout 30 --output /tmp/leviton-observation.json --json
   ```

   Record registry availability, discovered service types, matches and their
   `method`, all warnings, and uncorrelated devices. `registry` matches use exact
   MAC/HomeKit/serial identities; `legacy_mac_suffix` is an explicitly labeled
   heuristic. Repeat observation if needed; do not infer absence from one mDNS
   window. The tool browses HomeKit, Matter operational, and Matter commissionable
   advertisements. Matter advertisements without identifying metadata may not be
   recognized; discovery does not open commissioning windows or pair devices.
4. Preserve exit codes and result envelopes. Discovery exit 2 reports differences;
   exit 3 reports incomplete registry or mDNS coverage. Record skipped checks and
   missing entities as unverified. Keep complete raw output privately and share
   only redacted evidence.
5. Report only the observed model/firmware/integration/recipe combination. Record
   physical delivery or radio behavior as **not exercised** unless separately
   authorized and observed. Synthetic tests and cross-builds cannot supply it.

## Report a gap

Open an issue with the CLI version, host and HA version, command with credentials
removed, expected/actual behavior, and a small synthetic reproduction. Keep
inventories, screenshots, and traces containing private home information local.
See [Contributing](../CONTRIBUTING.md) for useful first fixes and
[Security](../SECURITY.md) for private vulnerability reports.
