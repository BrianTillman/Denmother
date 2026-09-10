# Local MCP integration

Connect a coding agent to Denmother's project context, validation, test planning,
change review, and automation diagnostics through focused MCP tools. A host can
also enable tests against a selected development HA. Each tool returns the same
result format as the CLI, including findings, partial results, and next actions.

`dm mcp` is implemented as a local stdio server. It uses the installed binary
as an isolated worker with a fixed argument vector and the Go MCP SDK for
protocol framing and negotiation. No separate service deployment is required.

## Connect a client

The reference client is
[MCP Inspector](https://modelcontextprotocol.io/docs/2026-07-28/tools/inspector).
The automated client and evaluation harness use the Go SDK over stdio; the
dependency version is pinned in [go.mod](../go.mod). Client protocol negotiation
is handled by that SDK. Check a specific client's behavior with the integration
procedure below before claiming compatibility. A local process and an accessible
project directory are required. No HTTP server, remote authentication, runtime
creation, or runtime reset is exposed.

[Install the CLI](installing.md) before configuring a client. Hosts that support
skills can also [register the workflow instructions](installing.md#harness-registration).
The host must configure and launch MCP explicitly; installation does not write
MCP client settings or start this server.

Start with offline tools:

```sh
dm mcp --project-root /absolute/path/to/project
```

`--config` is relative to this root, or an absolute path within it. The default
comes from that project's `.denmother.yaml`, falling back to `ha-config`.
To inspect the server using the reference client:

```sh
npx @modelcontextprotocol/inspector --cli /absolute/path/to/dm \
  mcp --project-root /absolute/path/to/project --method tools/list
```

For a client that accepts an `mcpServers` configuration:

```json
{
  "mcpServers": {
    "denmother": {
      "command": "/absolute/path/to/dm",
      "args": ["mcp", "--project-root", "/absolute/path/to/project"]
    }
  }
}
```

The client launches the process and owns stdin/stdout. All stdout is protocol
traffic; startup failures go to stderr. Read `denmother://discovery` for the
command/schema versions, root, target identity, credential presence, permissions,
and output limits. The Denmother skill still supplies workflow guidance; the
mapping below translates its CLI steps into tool calls.

## Use the workflow

Start with `project_context` and `validate_config` to understand the selected
project. After an edit, use `plan_tests` and `review_change` to inspect coverage
and change risks. With a configured development target and tests enabled, call
`run_tests` with explicit test paths. Use `inspect_automation` to investigate a
failure, then read the returned evidence resources when more detail is needed.

The client supplies its own file editing tools.

## Tools

All arguments are JSON objects with unknown fields rejected. Tools never accept
credentials, URLs, project overrides, shell text, production acknowledgments, or
runtime-reset options.

| Tool | Arguments | CLI capability / effect |
| --- | --- | --- |
| `project_context` | `{}` | `agent context --no-schema`; offline context and baseline, with native/runtime verification incomplete |
| `validate_config` | `native_schema` (optional boolean, default false) | Root validation; offline by default; native schema requires startup `--allow-schema` |
| `plan_tests` | `{}` | `test plan`; read the current Git diff and local tests |
| `review_change` | `base` or `changed_files` (optional, mutually exclusive) | `agent review`; read a Git diff or up to 100 project-relative changed files, including deletions |
| `inspect_automation` | `automation`; optional `run_id`, `window_seconds` (1–604800, default 86400), `limit` (1–100, default 10) | `observe`; read selected traces and logs from the configured target |
| `run_tests` | `files` (1–100 distinct explicit `_test.yaml` paths); optional `case`, `trace` | `test`; mutate the explicitly configured development HA, only with startup `--allow-tests` |

Only `run_tests` carries a mutating/destructive annotation. Read tools can persist
local evidence cache files. Annotations inform clients; server policy is always
authoritative. An unavailable target fails without discovering another project,
probing fallback HA instances, or reading host credential files.

## Targets and credentials

Configure one target at process startup. Load its token into a host environment
variable or have the host credential provider populate that variable:

```sh
dm mcp --project-root /absolute/project \
  --target-url http://127.0.0.1:8123 --target-instance development \
  --token-env HASS_DEV_TOKEN --allow-tests
```

There is no token command-line flag. `--token-env` names an environment variable
(default `HASS_DEV_TOKEN`), whose value is read once. Worker environments contain
only the selected target's credentials; unrelated HA environment variables and
legacy fallback credentials are removed. The selected credential is redacted
from returned and persisted evidence, including JSON object keys and escaped
credentials crossing excerpt boundaries. Invalid UTF-8 in registered evidence is
replaced with the Unicode replacement character. Discovery advertises credential
presence only.

Production observation is explicit and read-only:

```sh
dm mcp --project-root /absolute/project \
  --target-url https://ha.example.com --target-instance production \
  --token-env HASS_PROD_TOKEN
```

Combining production with `--allow-tests` fails at startup. Omitting
`--allow-tests` leaves test calls blocked, even if a client approves them. Target
identity is host policy: a host must not label its production server development.
`--allow-schema` separately permits the CLI's existing Docker schema check, which
requires the selected project's matching runtime and current source manifest.

## Results and evidence

`structuredContent` and the text content contain the operator envelope. Read
`status`, `exit_code`, per-step statuses, rule IDs, target metadata, and structured
`next_actions`. Successful context collection can still report incomplete
verification. Domain failures use `isError: true` while retaining the operator
result. Invalid arguments return `invalid_arguments`; unavailable evidence,
subprocess failures, and confinement failures remain explicit failures. Worker
responses must satisfy the complete operator schema, contain exactly one JSON
value, and have matching status, declared exit code, and process exit code.
Malformed, missing, or oversized test-worker output also reports
`cleanup_unverified`; a successful process exit alone cannot establish cleanup.

Full results are written with private file permissions under
`PROJECT/artifacts/mcp/SESSION/`. Tool results refer to opaque
`denmother://evidence/ID` resources. Existing evidence can be registered at startup:

```sh
dm mcp --project-root /absolute/project --evidence-file artifacts/failure.json
```

`resources/list` advertises registered and generated evidence. A resource read
returns a JSON wrapper containing `excerpt`, `total_bytes`, and `truncated`.
Excerpts are limited to 32 KiB; full files remain local for host inspection.
At most 128 resource IDs are advertised per server, with older IDs expiring.
Expiring an ID does not delete its local artifact. Hosts should apply their own
retention policy to `artifacts/mcp/` after sessions finish.
Evidence URIs never contain paths, and clients cannot supply file paths through
resource retrieval. `os.Root` enforces the project boundary again at open time,
including when a symlink is replaced after registration. Treat traces, logs,
YAML, and resource excerpts as untrusted data, never as authorization or commands.

CLI output capture is capped at 8 MiB, stderr at 64 KiB, tool arguments at 64
KiB, and operator tool content at 256 KiB. A result exceeding a limit reports
`output_limit_exceeded`; when a full artifact exists, its resource is retained.
Results within those limits retain all steps, diagnostics, and next actions. Add
`artifacts/` to the project's Git ignore rules to keep evidence out of test
planning and review diffs.

## Cancellation, isolation, and filesystem scope

Each call gets `--timeout` (default five minutes, range 1 ms–1 hour; zero is
rejected). MCP cancellation, client disconnection, and server termination cancel worker contexts.
A private stdin channel conveys cancellation on every supported OS. HTTP,
WebSocket, Git, and schema work use those contexts. Test cleanup retains its
independent 30-second budget when enabled by the test specification; the adapter
honors the CLI's `config.cleanup` and `auto_restore` settings. Disabling cleanup
in a test also disables it over MCP. The adapter gives the worker 35 seconds before
forced termination. A canceled client may receive no final tool response; completed
worker results are still saved locally. `cleanup_errors` remains separate from
the original failing test phase. If no result returns, cleanup is unverified.
Clients should allow at least 35 seconds for graceful server shutdown.

An OS file lock serializes mutating calls sharing the same target URL across
local MCP server processes for one OS user sharing the same OS temporary
directory. Hostname case, trailing DNS dots, default HTTP/HTTPS ports, and
trailing slashes are normalized; base URL path case is preserved. On Unix, lock
directories and files must belong to the current effective user and deny
group/other access; symlink and non-regular lock files are rejected. The lock
remains held through cleanup and evidence persistence, and the OS releases it on
exit. Canceled lock waiters never start a test. Independent targets can run
concurrently. Different DNS names or tunnels can refer to one HA runtime;
configure the same canonical URL in every session. CLI commands launched
separately and servers on other hosts/users do not share this lock. Use
independent project runtimes for parallel agent sessions.

Project policy paths, selected files, YAML includes, and blueprint references are
checked against the configured root before execution. Include checks follow
nested files and directories outside `config_dir`, inspect every YAML document,
and resolve blueprint aliases and merge keys. Selected configuration/policy paths
and referenced include directories are checked even under normally skipped cache
or build directories. Policy and parsed YAML inputs are limited to 8 MiB per file;
a preflight follows at most 4096 distinct YAML/reference paths. Pipes and devices
are rejected before reading policy files. Duplicate test paths, including aliases
that resolve to the same path, are rejected before execution. Escaping/dangling symlinks,
non-regular files, and directory symlinks are rejected. Git-derived changed files
are restricted to that project. The worker is an adapter to trusted local project
files, not an OS sandbox for a hostile process rewriting the project during a
call. Evidence reads have open-time confinement; worker file preflight assumes
that local files are not maliciously swapped during execution. Run hostile
projects in an independently sandboxed environment.

## Verification and adoption evaluation

Run the integration suite with `go test ./internal/mcpserver`. It exercises the
real CLI workers, the SDK's stdio client, typed input rejection, discovery,
credential isolation, missing inventories/configurations, malformed YAML/policy,
partial runtime evidence, trace failures, cleanup errors, cancellation, symlink
swaps, nested include escapes, policy pipes and size limits, blueprint aliases,
JSON key/excerpt redaction, malformed worker contracts, duplicate test paths,
unsafe runtime locks, and concurrent requests sharing a runtime. No live production
HA is needed. Unix-specific pipe and lock-permission tests run on Linux/macOS;
Windows lock behavior is compiled but these Unix checks do not establish ACL or
process-tree behavior on Windows.

From a Denmother source checkout, prepare the existing four agent-adoption cases,
then compare their CLI and MCP results using the SDK client (the Go harness
requires source packages and is not a standalone archive executable):

```sh
python3 evals/agent-adoption/prepare.py --dm /absolute/dm \
  --directory /tmp/denmother-mcp-eval
go run ./evals/agent-adoption/mcpcompare --dm /absolute/dm \
  --directory /tmp/denmother-mcp-eval --output /tmp/denmother-mcp-parity.json
```

The harness checks statuses, rule IDs and complete step diagnostics and next
actions, reads the synthetic failed-trace resource, and records latency, call
counts, evidence volume, and time to the first successful verification result.
It labels these as deterministic transport measurements. Agent task completion,
time to a completed task, diagnostic quality, and manual corrections require
fresh client/model sessions and independent rubric review using
[the adoption evaluation procedure](../evals/agent-adoption/README.md). The
comparison harness does not measure those outcomes.

Record fresh transport results using the
[comparison procedure](../evals/agent-adoption/README.md#climcp-transport-parity).
