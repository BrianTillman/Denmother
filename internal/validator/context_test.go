package validator

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidatorCancellationNeverPassesChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := New(t.TempDir(), Options{}).WithContext(ctx)
	for name, check := range map[string]func() error{"yaml": v.ValidateYAML, "config": v.ValidateConfig, "guard": v.ValidateGuard, "entities": v.ValidateEntities, "schema": v.ValidateSchema} {
		result := ResultFromError(name, check())
		if result.Status != CheckIncomplete || !errors.Is(result.Err(), context.Canceled) {
			t.Errorf("%s lost cancellation or reported false validation: %+v", name, result)
		}
	}
}

func TestSchemaBlockingSubprocessHelper(t *testing.T) {
	if os.Getenv("DM_SCHEMA_BLOCK_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("DM_SCHEMA_BLOCK_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestSchemaSubprocessCancellationIsIncomplete(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ready")
	t.Setenv("DM_SCHEMA_BLOCK_HELPER", "1")
	t.Setenv("DM_SCHEMA_BLOCK_READY", marker)
	oldLookPath, oldOutput, oldRun := lookPath, commandOutput, commandRunWith
	t.Cleanup(func() { lookPath, commandOutput, commandRunWith = oldLookPath, oldOutput, oldRun })
	lookPath = func(string) (string, error) { return os.Args[0], nil }
	commandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[2] == "{{.State.Running}}" {
			return []byte("true"), nil
		}
		return []byte(`[{"Source":"` + root + `","Destination":"/config"}]`), nil
	}
	commandRunWith = func(ctx context.Context, name string, args []string, stdout, stderr *bytes.Buffer) error {
		return oldRun(ctx, os.Args[0], []string{"-test.run=^TestSchemaBlockingSubprocessHelper$"}, stdout, stderr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan CheckResult, 1)
	go func() { done <- checkSchemaContext(ctx, root) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("schema subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if result.Status != CheckIncomplete || !errors.Is(result.Err(), context.Canceled) {
			t.Fatalf("canceled native schema reported wrong outcome: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native schema subprocess did not stop")
	}
}
