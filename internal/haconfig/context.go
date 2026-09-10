package haconfig

import (
	"context"
	"os/exec"
	"time"
)

// Local discovery must not hang indefinitely on an unresponsive Docker daemon
// or Git filesystem. A caller deadline can shorten this per-process bound.
func boundedCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = time.Second
	output, err := command.Output()
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
