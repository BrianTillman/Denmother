# Contributing

Help make Home Assistant changes easier to test, understand, and review.
Denmother welcomes reproducible bug reports, clearer guides, automation recipes,
dashboard examples, and improvements to the CLI and its agent workflow.

## Make your first change

Start with the synthetic [quickstart](README.md#quickstart). It demonstrates
the complete validation and test loop using two helpers. The
[automation examples](docs/testing.md#timer-event-and-blueprint-examples) add
timers, events, and blueprints; the [dashboard guide](docs/dashboards.md) adds
browser checks and named scenarios. All can be explored without physical devices.

Choose a problem you can reproduce or an item from the
[roadmap](ROADMAP.md). Useful first contributions include a clearer diagnostic,
a small regression fixture for an unsupported YAML shape, or an example showing
how to test a common automation. For larger features, discuss the intended user
workflow in an issue before building it.

### Optional devcontainer

With Docker running and VS Code's Dev Containers extension installed, open this
checkout and run **Dev Containers: Reopen in Container**. The checked-in
[configuration](.devcontainer/devcontainer.json) supplies Go matching `go.mod`,
Python 3 with PyYAML, Bash, Git, OpenSSL, Node.js 24, npm, and Docker Compose v2. Initial setup
downloads Go modules and builds `./dm`; it does not install Denmother into your
host account. Rebuild the container after changing its definition. When updating
`go.mod`'s Go version, also update the [Dockerfile](.devcontainer/Dockerfile);
startup checks that they match. Feature versions and digests are recorded in
[devcontainer-lock.json](.devcontainer/devcontainer-lock.json). After changing
features, regenerate it with `npx --yes @devcontainers/cli@0.89.0 build --workspace-folder .`
on the host and commit the updated lockfile; CI enforces it with `--frozen-lockfile`.

The container uses a dedicated Docker-in-Docker daemon. This makes checkout and
temporary-directory mounts work at their container paths and keeps HA's
`localhost` URL reachable from `dm` and Playwright. The Docker feature requires
a privileged development container, so use a host that permits it. Its Docker
images and runtime volumes are stored in separate named volumes, with an initial
download cost; the host's Docker image cache is not shared. See the
[Docker-in-Docker documentation](https://code.visualstudio.com/remote/advancedcontainers/use-docker-kubernetes).
Leave `DM_DEV_HOST_REPO_ROOT` unset in this environment.

Inside the container, run the baseline below or run this complete smoke check:

```sh
bash .devcontainer/smoke.sh --with-deps
```

This invokes the full preflight described below, including browser installation,
both Home Assistant versions, and a disposable Linux Homebrew install/test.
`--with-deps` allows Playwright to install Chromium's system libraries; omit it
once those libraries are installed. The first run downloads images and packages.
To explore interactively, use the README quickstart commands with `./dm` in
place of `dm`. In VS Code's **Ports** panel, forward the port printed by
`./dm dev up` (normally 8200–8699), then open the forwarded local URL. Keep the
port private in Codespaces.

For dashboard work, install Chromium and its system libraries on demand:

```sh
bash .devcontainer/install-browser.sh
./dm dev dashboard denmother-demo --config examples/dashboard/ha-config \
  --ensure-dev --render --json
./dm dev down --config examples/dashboard/ha-config --json
```

The installer reads Denmother's pinned Playwright version and uses the container's
sudo access for browser libraries. Run it again after rebuilding the container or
updating Playwright. CI builds this devcontainer and runs the same full preflight. Native platform and installer acceptance remain covered by the
existing platform jobs. Generated `.devcontainer/worktrees/` files remain ignored.
Stop interactive runtimes with `./dm dev down --config PATH` when finished; their
state persists until explicitly reset or the nested Docker volumes are removed.

### Full local preflight

From the checkout root, run:

```sh
python3 -B scripts/acceptance.py preflight --with-deps
```

Required tools are Go matching `go.mod`, Git, Python 3 with PyYAML, Bash,
Node/npm, Docker with Compose v2, and Chromium's system libraries. The
[devcontainer](.devcontainer/devcontainer.json) supplies the tools and PyYAML.
For native Debian/Ubuntu development, install `python3-yaml`. The optional
`--with-deps` flag lets Playwright install browser OS libraries, which can need
sudo. Docker must be local and able to bind-mount the checkout and temporary
paths. Linux ARM hosts need amd64 container emulation for Homebrew acceptance.

The command reports source, browser, each HA runtime version, packaging, and
Homebrew separately. Required tests must actually run: a missing prerequisite,
a skipped test/subtest, or a check that never ran produces a nonzero exit.
Source checks include formatting, Go tests/race/vet, required YAML preparation
tests, and Python tests. CI additionally performs source/history secret scans,
static analysis, vulnerability checks, and native OS/CPU package acceptance.

Both local development and CI call the same acceptance functions. The HA matrix
lives in [acceptance.json](scripts/acceptance.json). Each version starts fresh
quickstart, automation, and dashboard configurations with new Docker volumes;
checks `recorder/info`, browser/storage lifecycle, an intentional missing-entity
failure, and the warm scenario; and
removes its temporary containers and volumes, including on failure. Existing
example runtimes and inherited HA credentials are not used. A cleanup failure
retains the temporary configuration and prints its location for recovery.

Reports, per-check logs, and dashboard screenshots are retained under
`artifacts/preflight/<run>/`; the command prints the exact `report.json` path.
A passing local report covers Linux amd64 Homebrew in a disposable pinned image.
Native macOS Homebrew and the native platform matrix remain separate CI checks.
Homebrew acceptance leaves host Homebrew packages unchanged; `--with-deps`
allows installation of browser system libraries on the machine running preflight.

For focused acceptance after `go build -o ./dm .`:

```sh
python3 -B scripts/acceptance.py browser --with-deps
python3 -B scripts/acceptance.py runtime                 # both HA versions
python3 -B scripts/acceptance.py runtime --ha 2026.9.0   # one version
```

To test an already generated release, run
`python3 -B scripts/acceptance.py homebrew --archives dist`.
The same script supports `--native` on a disposable macOS Homebrew runner;
that mode temporarily installs the formula into that runner's Homebrew.

### Native setup and baseline checks

Use the Go version in [go.mod](go.mod). Most unit tests use synthetic fixtures
and local mock servers; Docker Compose v2 is needed for live HA acceptance.
Install the repository's pre-commit hook once per clone:

```sh
python3 scripts/precommit.py --install
```

Every commit then checks the exact staged files in a temporary checkout. Unstaged
fixes cannot hide failures in the proposed commit, and your working files and
index are left alone. The gate runs formatting, Go/Python source checks, required
Chromium regressions, a real generated-formula install and `brew test` in a
disposable official Homebrew container, and live dashboard/scenario acceptance
on both HA versions in `scripts/acceptance.json`. Storage dashboard acceptance
runs five times per version and directly verifies the frontend's `recorder/info`
API, so missing runtime integrations fail without relying on browser timing.
Missing tools, skipped required tests, or any failed
check block the commit. Allow several minutes; the first run downloads images
and browser dependencies. Failed snapshots retain their diagnostics under the
printed temporary directory.

Prerequisites are Go, Python 3 with PyYAML, Git, Bash, Node/npm, Docker with Compose v2, and
Chromium's host libraries. Prepare the browser and run its regressions with:

```sh
python3 scripts/acceptance.py browser --with-deps
```

Install PyYAML with your OS package manager (for example, `python3-yaml` on
Debian/Ubuntu) or in an activated Python virtual environment. CI uses PyYAML 6.0.3.
On Linux, installing the browser's OS packages may require sudo. Docker must use
a local daemon that can mount the checkout and temporary directories. The
Homebrew container requires Linux amd64 support (emulation on ARM hosts); it
does not modify your installed Homebrew packages. CI additionally tests native
macOS Homebrew and the other supported OS/CPU combinations. Repeated browser
runs help expose timing failures but cannot guarantee that every flake is caught.

Run the same gate before staging with `python3 scripts/precommit.py --worktree`,
or check only the staged snapshot with `python3 scripts/precommit.py`.
For faster feedback while iterating, use the baseline checks:

```sh
go build -o ./dm .
go test ./...
go vet ./...
python3 -B scripts/check_public_source.py
```

Format Go changes with `gofmt`. For a behavior change, include a regression that
reproduces the failure and verifies the intended result. Use local mock
HTTP/WebSocket servers and synthetic YAML. A documentation-only edit needs
accurate commands, valid links, and the relevant example checks.

## Find the implementation

| Area | Where to work |
| --- | --- |
| CLI commands and workflow composition | `cmd/` |
| YAML references and validation | `internal/hayaml/`, `internal/validator/` |
| Automation tests, trace attribution, and restoration | `internal/hatest/` |
| History audits, inventories, and device expectations | `internal/haaudit/`, `internal/hasync/`, `internal/haverify/` |
| Development HA, dashboards, fixtures, and scenarios | `cmd/dev*.go`, `cmd/devassets/` |
| Project paths, targets, and JSON results | `internal/project/`, `internal/haconfig/`, `internal/operator/` |
| MCP tools and evidence resources | `internal/mcpserver/` |
| Installation and embedded agent guidance | `internal/install/`, `scripts/bootstrap.py`, `skills/denmother/` |

The [architecture guide](docs/architecture.md) explains how these parts fit
together. Keep source locations, useful failure evidence, and the
`dm.operator.v1` result contract intact. Missing verification must remain visible
as incomplete or failed. Additive optional JSON fields are compatible; breaking
changes require a new contract version.

## Check the affected workflow

Run focused tests while iterating; the pre-commit gate is required before committing.
CI also runs race checks, static analysis, public-source checks,
and HA runtime and native platform tests. `./dm release check` runs the local
Go test/race/vet and Python suites; it does not replace the full pre-commit gate. The
[release guide](docs/releasing.md) lists the complete publication checks.

For installer changes, Bash and Python 3.9+ are also needed; the local HTTPS
release-server fixture uses OpenSSL. The following tests use temporary homes,
projects, and archives rather than the developer's harness directories:

```sh
go test ./internal/install ./cmd -run 'TestInstaller|TestBootstrap|TestInstall|TestScopeDiscovery|TestForeignAndSymlink|TestConcurrentInstall|TestBinaryOwnership|TestUpgradePayload|TestPermissionFailure|TestSharedDiscovery|TestReinstallAfter|TestUninstall'
python3 -B -m unittest discover -s scripts -p bootstrap_test.py
```

Run all installer core regressions with `go test ./internal/install`; the
focused CLI invocation above complements them with real executable/archive
checks. Parser fuzz targets `FuzzMetadataDecode` and `FuzzManagedPath` live in the
same package. Use a bounded run such as `go test ./internal/install -run '^$'
-fuzz FuzzMetadataDecode -fuzztime=10s -parallel=2`. Recovery tests should prove
that unrelated, empty, edited, or symlink-replaced files survive, and that a
failed later step still reports earlier rollback/application work. Test failure
JSON and rejected flags for unintended evidence-directory writes.

Maintain the canonical skill under `skills/denmother`; it is embedded directly
in the CLI. Update `scripts/release-assets.txt` when adding distributable files:
the Go packager and bootstrap validator share that allowlist. Test the complete
skill after deleting input archives and checkout copies. Native agent discovery
and invocation are separate from fixture tests; record client version, platform,
credentials availability, and synthetic-project evidence as described in
[live harness acceptance](docs/releasing.md#live-harness-smoke-tests).

Running `./setup --from-source` installs into your user account; use an explicit temporary home
and `--bin-dir` when exercising that flow during development. Documentation
should distinguish setup, skill registration, and `dm init`, and retain the
current release availability and tested platforms.

For runtime changes, run the Docker example commands in the README. Check
both the successful path and an intentional failure. `dev up --prepare-only`
renders files without starting containers. `dev down` stops only the selected
project. Tests and service calls may mutate HA; use a dedicated development
instance. Production tests require explicit `--allow-prod`.

## Submit a useful report or pull request

Include the CLI version, OS, HA version when relevant, the command with
credentials removed, expected/actual behavior, and a minimal synthetic example.
For a pull request, explain the concrete problem, resulting behavior, and checks
you ran. Report skipped runtime or platform checks explicitly.

Commit reusable source, examples, tests, and documentation. Keep personal plans,
dated workstation logs, raw evaluation reports, generated captures, credentials,
and real home inventories local. New public documentation must link only to
publishable files; add it to `scripts/release-assets.txt` when archive users need
it. Keep dependency license text in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
intact. Use the [security policy](SECURITY.md) for private vulnerability reports.
