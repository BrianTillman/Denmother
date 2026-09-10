package operator

import (
	"errors"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
)

func TestProductionLoopbackTargetRequiresAcknowledgement(t *testing.T) {
	for _, url := range []string{"http://localhost:8123", "http://127.0.0.1:8123", "http://[::1]:8123"} {
		t.Run(url, func(t *testing.T) {
			flags := haconfig.InstanceFlags{ProdURL: url, ProdToken: "synthetic-test-token"}
			resolved, err := ResolveTarget(haconfig.InstanceDev, flags, ModeMutating, false)
			var guardErr *GuardrailError
			if !errors.As(err, &guardErr) {
				t.Fatalf("production loopback mutation should require --allow-prod, got %v", err)
			}
			if resolved.Target.Name != "production" || resolved.Target.Risk != RiskHigh {
				t.Fatalf("production identity lost: %+v", resolved.Target)
			}
			if _, err := ResolveTarget(haconfig.InstanceDev, flags, ModeMutating, true); err != nil {
				t.Fatalf("acknowledged production target: %v", err)
			}
			readOnly, err := ResolveTarget(haconfig.InstanceProd, flags, ModeReadOnly, false)
			if err != nil || readOnly.Target.Risk != RiskCaution || readOnly.Target.Name != "production" {
				t.Fatalf("read-only production identity: target=%+v err=%v", readOnly, err)
			}
		})
	}
}

func TestSelectInstanceDefaultsWhenNoOverrideIsPresent(t *testing.T) {
	t.Parallel()

	if got := SelectInstance(haconfig.InstanceDev, haconfig.InstanceFlags{}); got != haconfig.InstanceDev {
		t.Fatalf("SelectInstance(dev, empty) = %q, want %q", got, haconfig.InstanceDev)
	}
	if got := SelectInstance(haconfig.InstanceProd, haconfig.InstanceFlags{}); got != haconfig.InstanceProd {
		t.Fatalf("SelectInstance(prod, empty) = %q, want %q", got, haconfig.InstanceProd)
	}
}

func TestSelectInstanceHonorsCrossInstanceFlagOverrides(t *testing.T) {
	t.Parallel()

	if got := SelectInstance(haconfig.InstanceDev, haconfig.InstanceFlags{ProdURL: "https://prod.example"}); got != haconfig.InstanceProd {
		t.Fatalf("SelectInstance(dev, prod override) = %q, want %q", got, haconfig.InstanceProd)
	}
	if got := SelectInstance(haconfig.InstanceProd, haconfig.InstanceFlags{DevURL: "http://localhost:8123"}); got != haconfig.InstanceDev {
		t.Fatalf("SelectInstance(prod, dev override) = %q, want %q", got, haconfig.InstanceDev)
	}
}
