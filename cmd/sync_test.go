package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BrianTillman/Denmother/internal/haconfig"
	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

type fakeSyncRunner struct {
	quiet          bool
	syncEntitiesFn func() (int, string, error)
	syncAllFn      func() (*hasync.SyncResult, error)
	findOrphansFn  func() (*hasync.OrphanResult, error)
}

func (f *fakeSyncRunner) SetQuiet(quiet bool) {
	f.quiet = quiet
}

func (f *fakeSyncRunner) SyncEntities() (int, string, error) {
	return f.syncEntitiesFn()
}

func (f *fakeSyncRunner) SyncAll() (*hasync.SyncResult, error) {
	return f.syncAllFn()
}

func (f *fakeSyncRunner) FindOrphanedDevices() (*hasync.OrphanResult, error) {
	return f.findOrphansFn()
}

func TestRunSyncEntityOnlyPartialWhenSchemaGenerationFails(t *testing.T) {
	oldJSON := syncJSON
	oldEntitiesOnly := syncEntitiesOnly
	oldResolve := resolveSyncTarget
	oldNewRunner := newSyncCommandRunner
	oldGenerateSchema := generateSyncSchema

	syncJSON = true
	syncEntitiesOnly = true
	resolveSyncTarget = func(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		return &operator.ResolvedTarget{
			Instance: haconfig.InstanceProd,
			Config: &haconfig.HAConfig{
				URL:    "https://prod.example",
				Token:  "token",
				Source: "test",
			},
			Target: &operator.Target{
				Name:     "production",
				Instance: string(haconfig.InstanceProd),
				URL:      "https://prod.example",
				Source:   "test",
				Risk:     operator.RiskCaution,
				Mode:     operator.ModeReadOnly,
			},
		}, nil
	}
	newSyncCommandRunner = func(url, token, configPath string) syncCommandRunner {
		return &fakeSyncRunner{
			syncEntitiesFn: func() (int, string, error) {
				return 7, "docs/reference/entity-list.txt", nil
			},
			syncAllFn: func() (*hasync.SyncResult, error) {
				t.Fatal("SyncAll should not run for entities-only sync")
				return nil, nil
			},
			findOrphansFn: func() (*hasync.OrphanResult, error) {
				t.Fatal("FindOrphanedDevices should not run for entities-only sync")
				return nil, nil
			},
		}
	}
	generateSyncSchema = func(string) (string, error) {
		return "", errors.New("schema boom")
	}

	defer func() {
		syncJSON = oldJSON
		syncEntitiesOnly = oldEntitiesOnly
		resolveSyncTarget = oldResolve
		newSyncCommandRunner = oldNewRunner
		generateSyncSchema = oldGenerateSchema
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runSync(cmd, nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	var exitErr *operator.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != operator.ExitPartial {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), operator.ExitPartial)
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusPartial {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusPartial)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
}

func TestRunSyncFullPartialUsesResolvedURLForMismatchReport(t *testing.T) {
	oldJSON := syncJSON
	oldEntitiesOnly := syncEntitiesOnly
	oldSkipMismatch := skipMismatch
	oldSkipOrphans := skipOrphans
	oldResolve := resolveSyncTarget
	oldNewRunner := newSyncCommandRunner
	oldGenerateSchema := generateSyncSchema
	oldAnalyze := analyzeSyncMismatches
	oldReport := generateSyncMismatchReport

	syncJSON = true
	syncEntitiesOnly = false
	skipMismatch = false
	skipOrphans = false

	reportURL := ""
	resolveSyncTarget = func(defaultInstance haconfig.Instance, flags haconfig.InstanceFlags, mode operator.Mode, allowProd bool) (*operator.ResolvedTarget, error) {
		return &operator.ResolvedTarget{
			Instance: haconfig.InstanceProd,
			Config: &haconfig.HAConfig{
				URL:    "https://prod.example",
				Token:  "token",
				Source: "test",
			},
			Target: &operator.Target{
				Name:     "production",
				Instance: string(haconfig.InstanceProd),
				URL:      "https://prod.example",
				Source:   "test",
				Risk:     operator.RiskCaution,
				Mode:     operator.ModeReadOnly,
			},
		}, nil
	}
	newSyncCommandRunner = func(url, token, configPath string) syncCommandRunner {
		return &fakeSyncRunner{
			syncEntitiesFn: func() (int, string, error) {
				t.Fatal("SyncEntities should not run for full sync")
				return 0, "", nil
			},
			syncAllFn: func() (*hasync.SyncResult, error) {
				return &hasync.SyncResult{
					EntitiesCount:  10,
					DevicesCount:   2,
					AreasCount:     1,
					EntityListPath: "docs/reference/entity-list.txt",
					DeviceListPath: "docs/reference/device-list.txt",
					AreaListPath:   "docs/reference/area-list.txt",
					Entities:       map[string]bool{"light.test": true},
					Warnings:       []string{"device/area fetch was partial"},
				}, nil
			},
			findOrphansFn: func() (*hasync.OrphanResult, error) {
				return &hasync.OrphanResult{ReportPath: "docs/reference/orphaned-devices.txt"}, nil
			},
		}
	}
	analyzeSyncMismatches = func(string, map[string]bool) (*hasync.MismatchAnalysis, error) {
		return &hasync.MismatchAnalysis{
			TotalConfigRefs:   1,
			TotalLiveEntities: 1,
			ExactMatches:      1,
		}, nil
	}
	generateSyncMismatchReport = func(_ string, haURL string, _ *hasync.MismatchAnalysis) (*hasync.MismatchReport, error) {
		reportURL = haURL
		return &hasync.MismatchReport{ReportPath: "docs/reference/mismatch-report.txt"}, nil
	}
	generateSyncSchema = func(string) (string, error) {
		return "docs/reference/schema.json", nil
	}

	defer func() {
		syncJSON = oldJSON
		syncEntitiesOnly = oldEntitiesOnly
		skipMismatch = oldSkipMismatch
		skipOrphans = oldSkipOrphans
		resolveSyncTarget = oldResolve
		newSyncCommandRunner = oldNewRunner
		generateSyncSchema = oldGenerateSchema
		analyzeSyncMismatches = oldAnalyze
		generateSyncMismatchReport = oldReport
	}()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := runSync(cmd, nil)
	if err == nil {
		t.Fatal("expected non-nil error because sync result should be partial")
	}

	var exitErr *operator.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != operator.ExitPartial {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), operator.ExitPartial)
	}
	if reportURL != "https://prod.example" {
		t.Fatalf("report URL = %q, want %q", reportURL, "https://prod.example")
	}

	var result operator.Result
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("failed to decode JSON output: %v", decodeErr)
	}
	if result.Status != operator.StatusPartial {
		t.Fatalf("status = %q, want %q", result.Status, operator.StatusPartial)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
}
