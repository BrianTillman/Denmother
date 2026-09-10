package devname

import "testing"

func TestProjectIsStableAndScoped(t *testing.T) {
	t.Parallel()

	root := "/tmp/Home Assistant Config"
	first := Project(root)
	second := Project(root)
	if first != second {
		t.Fatalf("project name not stable: %q != %q", first, second)
	}
	if !hasPrefix(first, "hass-dev-home-assistant-config-") {
		t.Fatalf("project name = %q", first)
	}
	if HomeAssistantContainer(root) != first+"-homeassistant" {
		t.Fatalf("container name = %q", HomeAssistantContainer(root))
	}
}

func TestProjectDiffersByRoot(t *testing.T) {
	t.Parallel()

	a := HomeAssistantContainer("/tmp/checkout-a")
	b := HomeAssistantContainer("/tmp/checkout-b")
	if a == b {
		t.Fatalf("expected distinct containers, both %q", a)
	}
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
