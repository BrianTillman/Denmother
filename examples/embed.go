// Package examples embeds synthetic Home Assistant starter configurations.
package examples

import "embed"

//go:embed quickstart/ha-config/configuration.yaml quickstart/ha-config/automations/presence/motion_lamp.yaml quickstart/ha-config/tests/presence/motion_lamp_test.yaml quickstart/docs/reference/entity-list.txt
var Quickstart embed.FS

// Starters lists only distributable source inputs, never generated runtime files.
//
//go:embed automations/docs/reference/entity-list.txt automations/ha-config/automations/blueprint_lamp.yaml automations/ha-config/automations/event_lamp.yaml automations/ha-config/automations/timer_lamp.yaml automations/ha-config/blueprints/automation/denmother/helper_lamp.yaml automations/ha-config/configuration.yaml automations/ha-config/tests/blueprint_lamp_test.yaml automations/ha-config/tests/event_lamp_test.yaml automations/ha-config/tests/timer_lamp_test.yaml dashboard/.denmother.yaml dashboard/ha-config/configuration.yaml dashboard/ha-config/dashboards/demo.yaml dashboard/ha-config/www/denmother-status-card.js dashboard/fixtures.json dashboard/scenarios.json dashboard/docs/reference/entity-list.txt
var Starters embed.FS
