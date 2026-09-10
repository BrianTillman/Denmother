# Security

## Test against the intended instance

Denmother handles HA access tokens and calls services when running tests.
Use a dedicated development instance. An explicit production target requires
`--allow-prod` for tests, including localhost tunnels. A development label cannot
establish that an operator-supplied URL is safe for real devices.

The portable HA container uses an internal Docker network and a loopback web
proxy. Keep that port local. Custom Compose templates own their network and
bootstrap behavior; see [runtime boundaries](docs/compatibility.md#configuration-and-runtime-boundaries).

## Keep evidence and credentials private

Use environment variables for tokens. Do not attach `.env`, token files, HA
storage, device inventories, or unredacted traces to public issues. Redact
entity names, locations, account names, and service data as well as tokens. The
synthetic examples avoid real home data.

## Install and upgrade

Installation verifies downloaded or local archives against their expected
SHA256 checksum and rejects unsafe archive entries before executing a payload.
Checksums from the release origin establish integrity against its published
checksum, not independent publisher identity. Source builds and explicitly
supplied binaries are local inputs; they are not presented as verified published
releases. See [installation inputs](docs/installing.md#inputs-and-plans).

Setup and skill registration do not obtain HA credentials, start HA or an agent,
or configure MCP. Ownership manifests and content hashes protect upgrades and
uninstall from overwriting local edits and foreign files. Interrupted operations
retain recovery journals containing previous file contents; do not attach these
or unreviewed installation reports to public issues. Use the documented
[recovery procedure](docs/installing.md#ownership-and-recovery) for owned changes.

## Report a vulnerability

Report vulnerabilities through
[private vulnerability reporting](https://github.com/BrianTillman/Denmother/security/advisories/new).
Include affected versions, a synthetic reproduction, and the expected boundary.
Keep exploit details and credentials out of public issues.

The project is community maintained. Fixes target the latest release; there is
no guaranteed response time or long-term support policy.
