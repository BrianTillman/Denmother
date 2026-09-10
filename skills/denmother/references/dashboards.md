# Development dashboard verification

Use the selected project's dedicated development runtime. The shipped dashboard
example is independent of real devices and comes with synthetic fixtures.

`dm dev dashboard DASHBOARD --json` checks referenced entities/resources.
`--render` checks every view by default and saves screenshots; `--view NAME`
selects a view. `--ensure-dev` recreates local HA from current YAML and web
assets, authenticates, and seeds absent fixture states. Omit it to inspect the
running instance without a restart. Rendering requires the browser dependencies
reported by the command and the selected runtime's generated login. Custom
runtime logins use `HASS_DEV_USERNAME` and `HASS_DEV_PASSWORD`.

Use `dm dev scenario --list --json` to discover defined states, and
`dm dev scenario NAME --json` to apply one to the local runtime. Scenarios
change development HA states. Review the rendered screenshot to assess visual
behavior; a clean entity/resource check alone does not establish appearance.

`--storage` reads a dashboard saved in the selected development HA through its
WebSocket APIs. List saved dashboards first and use their exact slug; an untouched
automatically generated dashboard needs an explicit saved configuration.

`dm dev fixtures` writes filtered state fixtures from a read-only HA source and
defaults to production unless an explicit development target is selected.
Fixture filtering does not make every value or screenshot safe to publish.
Keep observations scoped to the requested dashboard, and retain artifact paths
in the report rather than inlining complete state inventories.
