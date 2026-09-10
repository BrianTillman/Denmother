# Denmother guides

Find a guide for installing Denmother, testing your configuration, or contributing
a change.

## Choose your starting point

| I want to… | Start here |
| --- | --- |
| Try Denmother with no physical devices | [Install](installing.md), then the [quickstart](../README.md#quickstart) |
| Add Denmother to an existing HA repository | [Projects, starters, and targets](project.md) |
| Write a regression test for an automation | [Automation testing](testing.md) |
| Preview a dashboard or custom card under different states | [Dashboard development](dashboards.md) |
| Give a coding agent context, tests, and diagnostics | [Agent workflow](agents.md) |
| Connect a client through local MCP tools | [MCP integration](mcp.md) |
| Understand trace attribution or restoration between tests | [Trace attribution](testing.md#trace-attribution) and [cleanup support](testing.md#cleanup-support) |
| Check prerequisites, platform coverage, or device support | [Compatibility and supported workflows](compatibility.md) |

For everyday work, validate the selected configuration, refresh development HA
after source edits, run the relevant tests, and use `dm observe` to investigate
failures. Keep the selected `--config` and target consistent across commands.
The [agent guide](agents.md#change-test-and-diagnose) walks through that loop with
commands you can also use directly.

## Contribute or build an integration

| Work | Reference |
| --- | --- |
| Find a useful first contribution and run checks | [Contributing](../CONTRIBUTING.md) and [roadmap](../ROADMAP.md) |
| Find the implementation behind a feature | [Architecture and source map](architecture.md) |
| Consume JSON results and executable next actions | [CLI integration contract](agents.md#integration-contract) |
| Extend installation, ownership, or recovery | [Installer internals](architecture.md#installer-internals) |
| Measure how an agent uses Denmother | [Adoption evaluations](../evals/agent-adoption/README.md) |
| Package and verify a release or maintain a fork | [Release procedure](releasing.md) |
| Report a vulnerability privately | [Security policy](../SECURITY.md) |

See the [changelog](../CHANGELOG.md) for implemented features.
