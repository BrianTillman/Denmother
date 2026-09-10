// Package skills bundles the versioned Denmother skill with the CLI.
package skills

import "embed"

//go:embed denmother/SKILL.md denmother/references/*.md
var Files embed.FS
