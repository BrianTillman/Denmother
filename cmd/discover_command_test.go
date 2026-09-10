package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BrianTillman/Denmother/internal/discover"
	"github.com/BrianTillman/Denmother/internal/operator"
	"github.com/spf13/cobra"
)

type fakeDiscoverRunner func(context.Context) (*discover.LevitonReport, error)

func (f fakeDiscoverRunner) Run(ctx context.Context) (*discover.LevitonReport, error) { return f(ctx) }

func TestDiscoverCommandContractAndProjectReferences(t *testing.T) {
	oldJSON, oldTimeout, oldConfig, oldExplicit := discoverJSON, discoverTimeout, configPath, configExplicit
	oldProdURL, oldProdToken, oldDevURL, oldDevToken := discoverProdURL, discoverProdToken, discoverDevURL, discoverDevToken
	oldOutput, oldNew := discoverOutput, newDiscoverCommandRunner
	t.Cleanup(func() {
		discoverJSON, discoverTimeout, configPath, configExplicit = oldJSON, oldTimeout, oldConfig, oldExplicit
		discoverProdURL, discoverProdToken, discoverDevURL, discoverDevToken = oldProdURL, oldProdToken, oldDevURL, oldDevToken
		discoverOutput, newDiscoverCommandRunner = oldOutput, oldNew
	})
	root := t.TempDir()
	configPath = filepath.Join(root, "configuration")
	configExplicit = true
	if err := os.MkdirAll(configPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".denmother.yaml"), []byte("version: 1\nconfig_dir: configuration\nreferences_dir: inventories\n"), 0644); err != nil {
		t.Fatal(err)
	}
	discoverJSON, discoverTimeout, discoverOutput = true, 1, ""
	discoverProdURL, discoverProdToken, discoverDevURL, discoverDevToken = "https://ha.example.com", "secret", "", ""
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, tc := range []struct {
		name   string
		report *discover.LevitonReport
		err    error
		status operator.Status
	}{
		{name: "success", report: &discover.LevitonReport{RegistryAvailable: true}, status: operator.StatusSuccess},
		{name: "partial", report: &discover.LevitonReport{Warnings: []string{"registry unavailable"}}, status: operator.StatusPartial},
		{name: "warning", report: &discover.LevitonReport{NewDevices: []*discover.DiscoveredDevice{{Name: "new"}}}, status: operator.StatusWarning},
		{name: "failure", err: errors.New("request canceled"), status: operator.StatusFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newDiscoverCommandRunner = func(opts discover.Options) discoverCommandRunner {
				if opts.OutputPath != filepath.Join(root, "inventories", "leviton-discovery.json") {
					t.Fatalf("wrong path: %s", opts.OutputPath)
				}
				if !opts.Quiet {
					t.Fatal("JSON must suppress progress")
				}
				return fakeDiscoverRunner(func(got context.Context) (*discover.LevitonReport, error) {
					if got != ctx {
						t.Fatal("command context not propagated")
					}
					return tc.report, tc.err
				})
			}
			command := &cobra.Command{}
			command.SetContext(ctx)
			var output bytes.Buffer
			command.SetOut(&output)
			err := runDiscover(command, nil)
			if (err != nil) != (tc.status != operator.StatusSuccess) {
				t.Fatalf("unexpected error: %v", err)
			}
			var result operator.Result
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatalf("not one JSON envelope: %v: %s", err, output.String())
			}
			if result.SchemaVersion != operator.SchemaVersion || result.Status != tc.status || result.Command != "discover" {
				t.Fatalf("bad envelope: %+v", result)
			}
			if bytes.Contains(output.Bytes(), []byte("secret")) {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestDiscoverInvalidTimeoutStillEmitsJSON(t *testing.T) {
	oldJSON, oldTimeout := discoverJSON, discoverTimeout
	defer func() { discoverJSON, discoverTimeout = oldJSON, oldTimeout }()
	discoverJSON, discoverTimeout = true, 0
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	if err := runDiscover(command, nil); err == nil {
		t.Fatal("expected failure")
	}
	var result operator.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != operator.StatusFailure {
		t.Fatalf("bad status: %s", result.Status)
	}
}
