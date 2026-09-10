# Agent acceptance evaluations

Measure whether an agent can use Denmother to make and explain a Home Assistant
change. Four disposable projects exercise entity repair, test authoring, trace
diagnosis, and reporting of incomplete checks. Use them when improving skill
guidance, CLI diagnostics, or a client integration.

The cases give contributors comparable inputs and explicit rubrics. Go contract
tests establish CLI behavior; these evaluations measure the agent's command
selection and use of evidence. The repository ships the fixtures and recorder,
while you choose the client/model and review the result.

## Prepare the cases

Build Denmother, then prepare four disposable projects:

```sh
go build -o /tmp/denmother-dm .
python3 evals/agent-adoption/prepare.py --dm /tmp/denmother-dm --directory /tmp/denmother-eval
```

The directory must not already exist. Preparation uses the embedded synthetic
starter and skill, then introduces one task-specific change. No production data,
model API key, network, Docker startup, or agent SDK is required for preparation.

Preparation currently uses `dm init --agent --skill` to create a standalone,
unmanaged project skill fixture. This tests an agent with a supplied skill; it
does not test the managed installer's ownership or a harness's native discovery
paths. Normal users should follow
[managed registration](../../docs/installing.md#harness-registration).

To test native harness registration, use a separate fresh project initialized
without `--skill`, install with `dm skills install --harness codex` (or `claude`)
under a temporary user home or explicit project scope, and follow
[live harness acceptance](../../docs/releasing.md#live-harness-smoke-tests).
Record this discovery/invocation outcome separately from the four task rubrics.
Do not attempt managed installation over the unmanaged evaluation fixture.

## Run and review

For each project, start a fresh agent session with the content of `TASK.md` and
that project as its working directory. Give it shell/file access to that project
and the provided binary. Keep production credentials out of the environment.
Provide no rubric, reference answer, or earlier session's conclusions. Do not
reuse an agent's conversational context across cases. The runner/client is chosen
by the evaluator; this suite does not automatically send code to a model service.

Capture the transcript, actual process calls and exits, changed files, and
`REPORT.md`. Evaluate against [cases.json](cases.json) after the run. A task passes
only when every rubric item is supported by observed behavior and artifacts.
Review diagnostic quality and check reported outcomes against the artifacts; an
agent's self-reported pass is insufficient.

| Case | Independent verification |
| --- | --- |
| Broken entity | Offline validation passes; target matches the configured lamp; inventory and validation remain unchanged |
| Author test | `dm test doctor --json` loads the test and reports candidate natural-trigger trace coverage; inspect resulting-state assertion and cleanup |
| Trace failure | Report attributes failure to `fresh-failure`, locates `action/0`, and does not claim a demonstrated source-code fix |
| Incomplete verification | Context/offline validation retain partial status; report identifies missing inventory and native/runtime evidence |

All four cases run offline. Runtime behavior is unverified even if
the agent authors a valid test. Add a disposable HA runtime
test to measure test execution and recovery; the existing CI runtime job
verifies the shipped example, not an agent's newly authored test.

Record date, client/model, Denmother build, skill version, per-case pass/fail,
elapsed time, command count, manual corrections, and evidence location. Repeat
after changes to routing, result semantics, or test authoring guidance. Use fresh
directories and compare task outcomes, not exact response wording. Do not ship
raw transcripts or home-derived evidence in release archives.

## Record comparable runs

Use `record.py start --project CASE --dm /absolute/dm --client CLIENT --model MODEL`
immediately before handing a fresh task to a client. Record the exact exposed
model revision; use an explicit unavailable value when a client does not expose
it. `--transport mcp` labels MCP sessions. The record hashes the binary and skill.

During CLI sessions, use `record.py run --project CASE -- COMMAND FLAGS` to retain
actual command results, exit codes and durations. Do not put credentials in these
arguments or use private home data in evaluation cases. The evaluator reviews the
case rubric and report, then runs `record.py finish --project CASE --outcome pass
--manual-corrections 0 --evidence REPORT.md`. This records elapsed time from
task handoff to the final report write, CLI command count, review completion
time, and the report hash. Record failures and partial results with their
corresponding outcome.

Keep one new project per case/client/revision/transport. Compare outcome, evidence
quality, elapsed time, command count and corrections only across records with
known comparable inputs. For MCP, retain the client's tool transcript as well;
the CLI wrapper cannot count MCP calls. Unknown model identifiers remain a limit
on model comparisons. Repeat all four cases after final workflow/skill changes.

## CLI/MCP transport parity

After preparing fresh fixtures, run `go run ./evals/agent-adoption/mcpcompare
--dm /absolute/dm --directory /tmp/denmother-mcp-eval --output /tmp/mcp-parity.json`.
This deterministic SDK stdio client compares context, offline validation, planning,
and review across all four cases and reads the failed-trace resource. It records
latency, tool calls, and evidence bytes. Agent completion and corrections remain
unmeasured until the independent sessions and rubric review above are performed.
See [MCP contracts](../../docs/mcp.md) for setup and limits.
