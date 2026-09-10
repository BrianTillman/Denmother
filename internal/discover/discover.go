// Package discover discovers Leviton devices over mDNS and compares their
// identities with Home Assistant without changing HA or network devices.
package discover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"path/filepath"

	"github.com/BrianTillman/Denmother/internal/hasync"
	"github.com/BrianTillman/Denmother/internal/project"
)

type Options struct {
	HAUrl      string
	HAToken    string
	ConfigPath string
	Timeout    time.Duration
	OutputPath string
	Quiet      bool
}

const DefaultOutputPath = "docs/reference/leviton-discovery.json"

type Discoverer struct {
	haClient *hasync.Client
	options  Options
	browse   func(context.Context, []string) ([]*DiscoveredDevice, error)
}

func NewDiscoverer(opts Options) *Discoverer {
	return &Discoverer{haClient: hasync.NewClient(opts.HAUrl, opts.HAToken), options: opts,
		browse: NewMDNSBrowser(opts.Timeout).BrowseServices}
}

func (d *Discoverer) Run(ctx context.Context) (*LevitonReport, error) {
	if d.options.Timeout <= 0 {
		return nil, fmt.Errorf("mDNS timeout must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	states, err := d.haClient.WithContext(ctx).FetchEntities()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch HA entities: %w", err)
	}
	identities, registryErr := d.fetchRegistry(ctx, states)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var warnings []string
	if registryErr != nil {
		warnings = append(warnings, "HA registries unavailable; comparison uses legacy entity-name heuristics: "+registryErr.Error())
	}
	entities := []string{}
	for _, state := range states {
		if legacyEntity(state.EntityID) {
			entities = append(entities, state.EntityID)
		}
	}
	discovered, err := d.browse(ctx, ServiceTypes)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var partial *PartialBrowseError
		if !errors.As(err, &partial) {
			return nil, fmt.Errorf("mDNS discovery failed: %w", err)
		}
		warnings = append(warnings, "Some mDNS services could not be browsed: "+err.Error())
	}
	for _, identity := range identities {
		if identity.Leviton && identity.EntityID != "" && !containsString(entities, identity.EntityID) {
			entities = append(entities, identity.EntityID)
		}
	}
	comparison := CompareWithRegistry(entities, identities, discovered)
	for _, match := range comparison.Matches {
		for _, entity := range match.HAEntities {
			if !containsString(entities, entity) {
				entities = append(entities, entity)
			}
		}
	}
	sort.Strings(entities)
	report := GenerateReport(entities, discovered, comparison)
	report.RegistryAvailable = registryErr == nil
	report.Warnings = warnings
	outputPath := d.options.OutputPath
	if outputPath == "" {
		if d.options.ConfigPath == "" {
			outputPath = DefaultOutputPath
		} else {
			settings, err := project.ForConfig(d.options.ConfigPath)
			if err != nil {
				return nil, err
			}
			outputPath = filepath.Join(settings.ReferencesDir, "leviton-discovery.json")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := SaveReport(report, outputPath); err != nil {
		return nil, fmt.Errorf("failed to save report: %w", err)
	}
	return report, nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
