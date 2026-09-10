package operator

import (
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
)

func TestGuardrailsForLocalReadOnlyTarget(t *testing.T) {
	t.Parallel()

	guardrails, err := guardrailsForTarget(haconfig.InstanceDev, true, ModeReadOnly, true)
	if err != nil {
		t.Fatalf("guardrailsForTarget returned error: %v", err)
	}
	if len(guardrails) != 1 {
		t.Fatalf("guardrails len = %d, want 1", len(guardrails))
	}
	if guardrails[0] != "Reads from the local development Home Assistant instance." {
		t.Fatalf("unexpected guardrail: %q", guardrails[0])
	}
}

func TestGuardrailsForProductionMutatingTargetRequiresAllowProd(t *testing.T) {
	t.Parallel()

	guardrails, err := guardrailsForTarget(haconfig.InstanceProd, false, ModeMutating, false)
	if len(guardrails) != 1 {
		t.Fatalf("guardrails len = %d, want 1", len(guardrails))
	}
	if guardrails[0] != "Mutates production Home Assistant state." {
		t.Fatalf("unexpected guardrail: %q", guardrails[0])
	}
	if _, ok := err.(*GuardrailError); !ok {
		t.Fatalf("expected GuardrailError, got %T", err)
	}
}
