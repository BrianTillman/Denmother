# Release procedure

Package a version of Denmother that someone can install and use outside your
checkout. The release tools produce versioned native binaries, checksums,
documentation, synthetic starters, and matching agent guidance; an optional
Homebrew formula uses those same archives.

## Verify and build

Run from the repository root with the pinned Go toolchain and Python 3.12+.
`release check` includes the bootstrap and concurrent evaluation-recorder Python
tests alongside the Go test/race/vet suites.

```sh
go build -o ./dm .
go run github.com/zricethezav/gitleaks/v8@v8.24.3 dir . --redact
go run github.com/zricethezav/gitleaks/v8@v8.24.3 git . --redact --log-opts="--all"
./dm release check
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --no-config --enable-only=govet,staticcheck,unused ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
./dm dev up --config examples/quickstart/ha-config --json
./dm --config examples/quickstart/ha-config
./dm test --config examples/quickstart/ha-config --json
./dm dev down --config examples/quickstart/ha-config --json
./dm dev up --config examples/automations/ha-config --json
./dm --config examples/automations/ha-config --json
./dm test --config examples/automations/ha-config --json
DENMOTHER_RUNTIME_TEST_CONFIG="$PWD/examples/automations/ha-config" go test -race -v ./internal/hatest -run '^TestNativeRuntimeTargetsAndCleanup$' -count=1
./dm dev down --config examples/automations/ha-config --json
npm exec --yes --package=playwright@1.61.1 -- playwright install --with-deps chromium
./dm dev dashboard denmother-demo --config examples/dashboard/ha-config --ensure-dev --render --require-running --json
DENMOTHER_TEST_STORAGE_CONFIG="$PWD/examples/dashboard/ha-config" go test ./cmd -run '^TestStorageDashboardLiveAcceptance$' -count=1
./dm dev scenario warm --config examples/dashboard/ha-config --json
./dm dev dashboard denmother-demo --config examples/dashboard/ha-config --render --view details --require-running --json
./dm dev down --config examples/dashboard/ha-config --json
./dm release build --version 0.1.0-rc.1
python3 scripts/platform-acceptance.py --archive-dir dist --directory /tmp/denmother-release-acceptance
```

Run the source scan in a clean checkout before creating a development runtime.
Generated local credentials remain private even when Git ignores them.

Archives and `SHA256SUMS` are written to ignored `dist/`. Builds embed the
version, revision (including dirty state), build time, and Go/platform metadata.
Packaging uses explicit document and example file lists; generated
runtime directories and credential files are excluded. Archives contain `dm`
(or `dm.exe`), documentation, license notices, examples, the bundled skill, agent evaluation
fixtures, and the checksum-verifying setup bootstrap. The Python bootstrap and Go packager
share the enumerated `scripts/release-assets.txt` allowlist. Windows packages
use tar.gz, which can be extracted with modern Windows tar.

Before a public release, record the test commands and results, verify the Linux
binary reports the intended version, check archive contents and checksums, and
ensure the release commit is clean. Test an unpacked package outside the source
checkout. Cross-builds do not establish runtime compatibility on other systems.

Enable private vulnerability reporting and require successful CI before merging
release changes. Scan both the source snapshot and all reachable Git history
with a secret scanner. Check PR descriptions, issue attachments, workflow logs,
and release assets for private data before publication.

The CI workflow builds and uploads test artifacts. The separate `release.yml`
workflow runs source, automation, and dashboard browser checks when a
`v<VERSION>` tag is pushed, verifies
installation outside the checkout, and publishes the matching archives, checksums,
and Homebrew formula. A hyphenated version is marked prerelease. It never creates
a tag or updates a tap. The initial public version has not been tagged yet.

Pushing a release tag is the publication action. Use the manual steps below only
when publishing without that workflow; do not race a manual release against it.

## Release checklist and evidence

Verify the exact commit that will be tagged. Keep a release record with:

| Gate | Evidence to retain |
| --- | --- |
| Public source | Source/history scans, documentation links, and reviewed archive allowlist |
| Implementation checks | Go tests/race/vet/static analysis, Python suites, and workflow checks |
| HA runtime | Results for both pinned versions: schema, automation, native target/cleanup, dashboard, scenario, and saved-storage tests |
| Native packages | Platform acceptance results and Homebrew install/test results for the release revision |
| Archive identity | Version, clean revision, platform, archive hashes, and `SHA256SUMS` |
| Independent installation | Unpacked/archive installation outside the checkout; skill resources remain usable after input payload removal |
| Ownership and recovery | Disposable destination checks preserve foreign files and edits and report interrupted operations |
| Agent compatibility claims | Fresh authenticated client discovery/invocation results with client version and skill digest |

Name the host, command, exit status, and evidence location for each result.
Record unavailable checks as unverified. Local results without a revision cannot
certify a later release. Keep raw workstation logs, credentials, home state,
browser captures, and client transcripts out of public source; publish only
reviewed evidence appropriate to the claim. Use [compatibility](compatibility.md)
to identify missing coverage. Installer regression and live-client checks are
listed under [installer acceptance](#installer-acceptance).

## Homebrew

Generate a binary formula alongside the existing archives by supplying the
GitHub repository that will host the release:

```sh
./dm release build --version 0.1.0-rc.1 --homebrew-repository BrianTillman/Denmother
```

The result is `dist/homebrew/Formula/denmother.rb`. It selects the macOS or Linux
ARM64/x86-64 archive, installs `dm` and shell completions, and preserves the
documentation and quickstart under Homebrew's share directory. Checksums come
directly from the archives built in the same invocation. Download URLs use
`https://github.com/OWNER/REPO/releases/download/v<VERSION>/` and the existing
archive names; the tag must have the `v` prefix. Prerelease versions are supported
for a testing tap. Omitting `--homebrew-repository` keeps archive-only packaging.

Publish in this order:

1. Complete the release checks above and review the generated formula.
2. Publish the exact `dist/*.tar.gz` files and `dist/SHA256SUMS` as assets of the
   matching `v<VERSION>` GitHub Release. Do not rebuild after copying the formula:
   new build timestamps change the archive checksums.
3. Copy `dist/homebrew/Formula/denmother.rb` into `Formula/denmother.rb` in the
   chosen `OWNER/homebrew-TAP` repository and publish that update.
4. On a supported Homebrew host, run `brew install OWNER/TAP/denmother` and
   `brew test OWNER/TAP/denmother`. For an existing installation, run
   `brew update` and `brew upgrade OWNER/TAP/denmother` first.

After installing or upgrading through the selected tap, register the matching
embedded skill with the package's CLI:

```sh
dm skills install --harness detected --json
dm skills status --json
```

Use an explicit harness selection if none is detected. Do not run the binary
bootstrap to replace a package-managed binary; future CLI upgrades use the same
tap's `brew upgrade OWNER/TAP/denmother`, followed by `dm skills install`.

This is a custom tap formula using upstream binary archives. It does not build
Homebrew bottles or submit to homebrew/core. No tap, tag, or release is created
by `dm`. Follow Homebrew's
[tap maintenance guide](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)
and [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook) when publishing.

The standalone CI workflow generates the formula and tests an installation on
macOS using the uploaded archives served over loopback. It changes only the
download location, preserving the generated checksums. `brew test` checks the
stamped version, validates a writable copy of the bundled example offline, and
checks that malformed YAML fails. Linux and other macOS architecture runtime
acceptance still requires a corresponding host.

## Compatibility evidence

The CI runtime matrix exercises pinned HA 2026.8.3 and 2026.9.0 images. Its native
archive matrix verifies checksums and runs the packaged CLI on Linux x86-64/ARM64,
macOS x86-64/ARM64, and Windows x86-64. The Homebrew job separately exercises the
generated formula. Save their artifacts and exact revision in the release record;
check that those jobs passed on the release revision. See [compatibility](compatibility.md).

Dashboard screenshots and JSON are uploaded even when a gate fails. Release
publication occurs only after all gates in the tag job pass. Before pushing a
tag, also verify the normal CI platform and HA-version matrix for that exact
commit; the tag job does not substitute for native testing on other hosts.

## Installer acceptance

The native archive matrix above validates executable behavior and project
starters. Also run installer regressions, install outside the checkout, and
check client loading before claiming harness compatibility.

### Installer regression tests

Installer tests use temporary homes, projects, harness layouts, archives, and a
local HTTPS release server. They do not modify the developer's real harnesses or
contact a Home Assistant instance.

```sh
go test ./internal/install ./cmd
python3 -B -m unittest discover -s scripts -p bootstrap_test.py
go test -race ./...
go vet ./...
```

For a short additional parser fuzz run:

```sh
go test ./internal/install -run '^$' -fuzz FuzzMetadataDecode -fuzztime=10s -parallel=2
go test ./internal/install -run '^$' -fuzz FuzzManagedPath -fuzztime=10s -parallel=2
```

Hardening regressions exercise malformed/oversized metadata, unrelated binary
recovery targets, missing-versus-empty files, first-install rollback, permission
restoration, locks released after process death, caller cancellation, readonly
error rendering, archive replacement after hashing, bounded gzip expansion,
sparse archives, malformed delegated/version/release results, and conflicting
flags before acquisition.

Other regressions cover installation across harnesses and destinations, upgrades,
conflicts, package ownership, PATH shadowing, archive checks, offline mode,
dry runs, selective uninstall, JSON output, and skill resources remaining usable
after input archives and checkouts are removed.

### Install outside the checkout

On a Linux amd64 release host, for example:

```sh
install_root="$(mktemp -d)"
install_home="$install_root/home"
install_bin="$install_root/bin"
mkdir -p "$install_home" "$install_root/payload"
cp dist/denmother-0.1.0-rc.1-linux-amd64.tar.gz dist/SHA256SUMS "$install_root/payload/"

with_install_home() {
  env HOME="$install_home" CODEX_HOME="$install_home/.codex" \
    CLAUDE_CONFIG_DIR="$install_home/.claude" PATH="$install_bin:$PATH" "$@"
}

with_install_home ./setup \
  --archive "$install_root/payload/denmother-0.1.0-rc.1-linux-amd64.tar.gz" \
  --bin-dir "$install_bin" --harness codex,claude --offline --json
with_install_home "$install_bin/dm" skills status --harness codex,claude --json
rm "$install_root/payload/denmother-0.1.0-rc.1-linux-amd64.tar.gz" "$install_root/payload/SHA256SUMS"
with_install_home "$install_bin/dm" skills status --harness codex,claude --json
```

Substitute the built version and host platform. Retain the result files and
binary checksum outside the copied payload. For an already verified unpacked
Unix package, also run its companion `./setup` with the same isolated environment
and a fresh binary/skill destination; remove the extraction directory and
recheck status. Repeat setup before removal to verify no-op behavior. The full
Go installer acceptance test covers source builds, archives, ownership conflicts,
and interruption/recovery automatically.

The release workflow uses temporary HOME, CODEX_HOME, CLAUDE_CONFIG_DIR, and bin
paths for bootstrap and registration checks before publication. These filesystem
checks do not authenticate or launch an agent. Complete the separate
[live harness smoke tests](#live-harness-smoke-tests)
before claiming a supported harness version. Windows archive execution does not
establish native Windows bootstrap support.

### Live harness smoke tests

Before claiming a supported harness version, start a fresh authenticated session
in a disposable home/project with the managed skill installed. Confirm native
skill discovery, then ask the agent to inspect a synthetic HA project using
Denmother. Verify the invoked binary, registration scope, actual CLI commands and
results, and reporting of checks that could not run. Record harness version,
model when available, platform, skill digest, and reviewed evidence. Keep
production targets out of the test and retain transcripts locally.

These checks are separate from installer fixtures and from the
[task evaluations](../evals/agent-adoption/README.md), which supply a skill directly.
Do not treat a successful status command or CLI/MCP transport comparison as a
live discovery pass.

Record acceptance against an exact revision and binary checksum using the
[release checklist](#release-checklist-and-evidence). The
[compatibility matrix](compatibility.md) distinguishes configured test coverage
from native execution and client loading. Fixture tests do not establish a
minimum supported harness version. A native Windows bootstrap remains a
[contribution opportunity](../ROADMAP.md).
