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

Use the Go version in [go.mod](go.mod). Most unit tests use synthetic fixtures
and local mock servers; Docker Compose v2 is needed for live HA acceptance.
Build a checkout-local binary and run the baseline checks:

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

Run focused tests while iterating, then the baseline above before submitting a
behavior change. CI also runs race checks, static analysis, public-source checks,
and HA runtime and native platform tests. `./dm release check` runs the local
Go test/race/vet and Python suites; it does not replace live HA acceptance. The
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
