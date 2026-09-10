// Package devname derives stable per-worktree Docker project and container names.
package devname

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeRe = regexp.MustCompile(`[^a-z0-9]+`)

// Project returns the isolated compose project name for a repository root.
func Project(root string) string {
	base := strings.ToLower(filepath.Base(root))
	base = unsafeRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "worktree"
	}
	if len(base) > 24 {
		base = base[:24]
		base = strings.Trim(base, "-")
	}
	return fmt.Sprintf("hass-dev-%s-%08x", base, Hash(root))
}

// HomeAssistantContainer returns the worktree-scoped Home Assistant container name.
func HomeAssistantContainer(root string) string {
	return Project(root) + "-homeassistant"
}

// Hash is a stable 32-bit identifier for a worktree root.
func Hash(value string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum32()
}
