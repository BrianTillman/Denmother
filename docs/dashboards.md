# Develop Home Assistant dashboards

Preview your dashboard in real Home Assistant, switch between named scenarios,
and catch rendering problems across every view. Denmother combines entity and
resource checks with screenshots, browser errors, and custom-card diagnostics.

## Try the dashboard starter

After [installing Denmother](installing.md), initialize a fresh project. The
starter includes two views, a local custom card, a YAML helper, and synthetic
temperature scenarios. No physical devices or production exports are needed.

```sh
dm init --directory /tmp/denmother-dashboard --example dashboard --json
cd /tmp/denmother-dashboard
dm dev dashboard denmother-demo --ensure-dev --render --require-running --json
dm dev scenario --list --json
dm dev scenario warm --json
dm dev dashboard denmother-demo --render --view details --json
dm dev down --json
```

Docker Engine and Compose v2 are required to start HA. Browser rendering
requires Node.js and npm; the command prepares pinned Playwright and Chromium
automatically. A browser preflight launches Chromium before any login, so
missing Linux shared libraries produce a setup command and the selected runner
path. On a supported Linux distribution, open that directory and run:

```sh
npx playwright install-deps chromium
```

This installs operating-system libraries and may require administrator access.
Run it yourself if preflight reports missing libraries. See
[Playwright's browser setup](https://playwright.dev/docs/browsers#install-system-dependencies).
The first run may download images and browser files. The result includes the
local HA URL and screenshot artifacts under the selected project's
`artifacts/dashboard-render`. Open the reported dashboard URL in your browser to
interact with HA directly. Generated login credentials are stored in the
runtime's `credentials.json` beside its Compose file; `--render` reads them
automatically. For an externally managed development runtime, set both
`HASS_DEV_USERNAME` and `HASS_DEV_PASSWORD`.

`--ensure-dev` recreates the selected runtime from current YAML and local web
assets, completes authentication, and seeds missing fixture entities. Without
`--ensure-dev`, render uses the existing instance; run `dev up` after source edits.
All declared views are checked by default. Repeat `--view` or supply a
comma-separated list to select view paths or indexes. Every checked view gets its
own screenshot and reports browser, HTTP, configuration, and missing-card errors.

The commands below assume the selected project directory. Add `--config PATH`
when using a different configuration. Run `dm dev up --json` before continuing
if you stopped the example runtime above.

## Use an existing YAML dashboard

Register the dashboard under `lovelace.dashboards` in `configuration.yaml`, or use
HA's YAML-mode default `ui-lovelace.yaml`. Includes and include-directory forms
are supported, including the `dashboards/definitions/*.yaml` compatibility layout.
An unregistered direct YAML path can be inspected, but must
be registered before HA can render it. Use `dev dashboard --list` to see the
selected configuration's YAML registrations.

Declare resources using HA's `lovelace.resources` and `resource_mode: yaml`, or
register resources in the selected HA runtime. Put locally hosted custom-card
JavaScript and assets under the development configuration's `www/`; the portable
runtime copies web assets and HA serves them at `/local/`. No special Denmother
custom-card bundle is required. The isolated HA container has no outbound network;
locally hosted resources make the example independent of third-party servers.

Entity checks use the selected project's `references_dir`, and resource checks
inspect registered URLs. Browser rendering then checks that the custom elements
load. Dynamic Jinja/blueprint inputs cannot be resolved as static entity
references; use runtime evidence for those cases.

## Inspect dashboards saved in Home Assistant

Use `--storage` for a dashboard configured through HA's UI in the selected
**development** instance. The command reads the supported WebSocket dashboard
list/config APIs; it does not read or copy `.storage` files, and does not change
the saved dashboard.

```sh
dm dev dashboard --storage --list --json
dm dev dashboard my-dashboard --storage --render --json
dm dev dashboard my-dashboard --storage --render --view details --json
```

Replace `my-dashboard` with a slug returned by the list command. The saved
dashboard needs an explicit configuration with views. For an untouched
automatically generated dashboard, first take control/save its configuration in
HA. The default dashboard is selected as `lovelace`. YAML and storage sources use
the same entity, resource, view-selection, and screenshot checks. Storage
inspection requires a reachable authenticated development instance; an unknown
slug fails instead of falling back to another dashboard. `--ensure-dev` is
optional and recreates the isolated runtime as usual; omit it to inspect the
existing runtime without restarting it.

Custom-card acceptance covers ordinary custom elements, cards with nested open
shadow roots, canvas cards, delayed element registration, and custom cards with
closed shadow roots. Closed internals cannot be inspected, but the element must
register and become visible; browser/page/HTTP errors are still collected. The
browser never counts ordinary text as a card. Informational HA alerts do not fail
a render.
## Fixtures and scenarios

A project's `.denmother.yaml` can select files relative to that project:

```yaml
version: 1
config_dir: ha-config
dev_fixtures: fixtures.json
dev_scenarios: scenarios.json
```

Defaults remain `.devcontainer/dev-state-fixtures.json` and
`.devcontainer/scenarios.json`. The shipped `examples/dashboard` provides both
formats. An absent default fixture file is optional; an explicitly configured
fixture path must exist. Malformed fixtures fail startup and export merging.
Versioned fixture documents require `schema_version: "dev_state_fixtures.v1"`,
an `entities` object, and optional `description`/`source` metadata. Unknown
versions or fields and invalid entity records are rejected. Legacy flat maps
from entity IDs to `{ "state": "...", "attributes": {} }` remain readable.
States must contain 1–255 Unicode code points, matching HA's character limit;
invalid UTF-8 and unpaired Unicode escapes are rejected. Multi-byte characters
such as accented letters and emoji count as one code point each. Scenario listing and
application report missing or invalid definitions as an error.

`dev up` fills only absent entity states from fixtures after authentication; it
preserves states already provided by YAML integrations. Synthetic states carry a
`denmother_source` attribute; `dev doctor` reports them with a warning, even
when the dashboard renders successfully. Custom Compose templates own their
fixture/bootstrap behavior. To refresh fixture data from an explicit read-only
HA target:

```sh
dm dev fixtures denmother-demo --dev-url http://localhost:8123 --json
```

Replace the URL with the development target you intend to read and use
`HASS_DEV_TOKEN` for its token. `--all-dashboards` refreshes every
registered YAML dashboard; `--replace` replaces the selected fixture file rather
than merging. Without an explicit development target this export command defaults
to production, as described by `--help`. Exported attributes are filtered, but
entity states, names, and screenshots may still be private: use synthetic example
data when sharing.

Scenarios replace the listed display states through HA's
[REST state API](https://developers.home-assistant.io/docs/api/rest/). These states
are transient: they do not implement physical devices or integration services,
and integrations can overwrite them. Use YAML helpers and actual services for
automation behavior tests. Scenarios reject production targets. Restart HA to
reseed fixtures, or apply another scenario to change the visual state.
