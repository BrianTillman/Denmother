// Package releaseassets shares the exact release allowlist with the bootstrap.
package releaseassets

import (
	_ "embed"
	"strings"
)

//go:embed release-assets.txt
var assets string

func Files() []string { return strings.Fields(assets) }
