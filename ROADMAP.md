# Roadmap

Help bring Denmother to more hosts, integrations, and agent clients. These
opportunities have no scheduled release dates. Open an issue with an example
and a way to test the proposed change.

| Opportunity | What a useful contribution demonstrates |
| --- | --- |
| First public release | Passing source, native platform, HA-version, installer, and Homebrew gates on the exact release revision |
| More development hosts | Repeatable Docker Desktop workflows on macOS/Windows and HA runtime acceptance on ARM64 |
| Native Windows setup | A PowerShell bootstrap using the shared Go installer, with native installation and recovery checks |
| More automation recipes | Small examples for additional integrations, target types, and cleanup behavior, with state and trace assertions |
| More dashboard coverage | Reproducible custom-card, browser, or viewport cases with screenshots and tests that catch rendering failures |
| Verified agent onboarding | Fresh-session skill discovery and invocation for specific client versions using disposable projects |
| Better agent outcomes | Comparable task evaluations showing command selection, diagnostic quality, verification, and required corrections |
| Device verification | Read-only observations recorded by model, firmware, integration, and recipe, with synthetic and physical behavior clearly identified |

Start with [Contributing](CONTRIBUTING.md). The
[changelog](CHANGELOG.md) covers implemented features, the
[compatibility matrix](docs/compatibility.md) identifies acceptance gaps, and the
[agent evaluations](evals/agent-adoption/README.md) provide reusable tasks.
