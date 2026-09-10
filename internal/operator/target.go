package operator

import (
	"context"
	"github.com/BrianTillman/Denmother/internal/haconfig"
)

// RiskLevel classifies the risk of an operation on a selected target.
type RiskLevel string

const (
	RiskSafe    RiskLevel = "safe"
	RiskCaution RiskLevel = "caution"
	RiskHigh    RiskLevel = "high"
)

// Mode distinguishes read-only commands from commands that change HA state.
type Mode string

const (
	ModeReadOnly Mode = "read_only"
	ModeMutating Mode = "mutating"
)

// Target records the selected HA instance, operation mode, and risk level.
type Target struct {
	Name       string    `json:"name"`
	Instance   string    `json:"instance"`
	URL        string    `json:"url,omitempty"`
	Source     string    `json:"source,omitempty"`
	Risk       RiskLevel `json:"risk"`
	IsLocal    bool      `json:"is_local"`
	Guardrails []string  `json:"guardrails,omitempty"`
	Mode       Mode      `json:"mode"`
}

// ResolvedTarget binds a normalized target to an HA connection config.
type ResolvedTarget struct {
	Instance haconfig.Instance
	Config   *haconfig.HAConfig
	Target   *Target
}

// GuardrailError reports that target policy blocked the operation.
type GuardrailError struct {
	Message string
}

func (e *GuardrailError) Error() string {
	return e.Message
}

// SelectInstance resolves explicit cross-instance overrides for commands that
// default to one environment but allow targeting the other via flags.
func SelectInstance(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags) haconfig.Instance {
	switch defaultInstance {
	case haconfig.InstanceDev:
		if flags.ProdURL != "" || flags.ProdToken != "" {
			return haconfig.InstanceProd
		}
	case haconfig.InstanceProd:
		if flags.DevURL != "" || flags.DevToken != "" {
			return haconfig.InstanceDev
		}
	}
	return defaultInstance
}

// ResolveTarget resolves a shared target and enforces any applicable guardrails.
func ResolveTarget(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode Mode, allowProd bool) (*ResolvedTarget, error) {
	return ResolveTargetContext(context.Background(), defaultInstance, flags, mode, allowProd)
}

// ResolveTargetContext propagates cancellation through discovery and authentication.
func ResolveTargetContext(ctx context.Context, defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode Mode, allowProd bool) (*ResolvedTarget, error) {
	instance := SelectInstance(defaultInstance, flags)

	config, err := haconfig.ResolveInstanceConfigContext(ctx, instance, flags)
	if err != nil {
		return nil, err
	}

	target := &Target{
		Name:     instanceName(instance, config.IsLocal),
		Instance: string(instance),
		URL:      config.URL,
		Source:   config.Source,
		Risk:     classifyRisk(instance, config.IsLocal, mode),
		IsLocal:  config.IsLocal,
		Mode:     mode,
	}

	guardrails, guardErr := guardrailsForTarget(instance, config.IsLocal, mode, allowProd)
	target.Guardrails = append(target.Guardrails, guardrails...)
	if guardErr != nil {
		return &ResolvedTarget{
			Instance: instance,
			Config:   config,
			Target:   target,
		}, guardErr
	}

	return &ResolvedTarget{
		Instance: instance,
		Config:   config,
		Target:   target,
	}, nil
}

func instanceName(instance haconfig.Instance, isLocal bool) string {
	// A loopback address can be a tunnel to an explicitly selected production HA.
	if instance == haconfig.InstanceProd {
		return "production"
	}
	if isLocal {
		return "local"
	}
	switch instance {
	case haconfig.InstanceProd:
		return "production"
	case haconfig.InstanceDev:
		return "development"
	default:
		return string(instance)
	}
}

func classifyRisk(instance haconfig.Instance, isLocal bool, mode Mode) RiskLevel {
	if instance == haconfig.InstanceProd {
		if mode == ModeMutating {
			return RiskHigh
		}
		return RiskCaution
	}
	if isLocal {
		return RiskSafe
	}
	return RiskCaution
}

func guardrailsForTarget(instance haconfig.Instance, isLocal bool, mode Mode, allowProd bool) ([]string, error) {
	switch {
	case mode == ModeReadOnly && instance == haconfig.InstanceProd:
		return []string{"Reads live Home Assistant state."}, nil
	case mode == ModeMutating && instance == haconfig.InstanceProd:
		guardrails := []string{"Mutates production Home Assistant state."}
		if !allowProd {
			return guardrails, &GuardrailError{
				Message: "production target requires --allow-prod because tests mutate live Home Assistant state",
			}
		}
		return guardrails, nil
	case isLocal && mode == ModeReadOnly:
		return []string{"Reads from the local development Home Assistant instance."}, nil
	case mode == ModeMutating && isLocal:
		return []string{"Mutates the local development Home Assistant instance."}, nil
	default:
		return []string{"Uses explicit development targeting."}, nil
	}
}
