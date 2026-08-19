package cli

import (
	"context"
	"encoding/json"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// DriftedField is one attribute that differs between a resource's own
// last-recorded ledger state and its live, freshly-scanned state -- the
// exact (path, before, after) triple core.DiffAttributes/
// core.FilterNormalizationNoise already compute, unmodified: never
// re-serialized into a display string here, since render --md's own
// prose and render --sync-overrides' own per-medium override-statement
// templating each need a different rendering of the identical fact.
type DriftedField struct {
	Path   string
	Before json.RawMessage
	After  json.RawMessage
}

// DriftedResource is one live resource whose current state differs from
// its last-recorded ledger state, plus every field that differs.
// Observed is the resource's own FULL live-observed state (core.ScanResult.
// Observed) -- render --sync-overrides needs it, not just Fields' own
// per-path diff: resolver.Override.Config keys are TOP-LEVEL wire
// attribute names only, never a dot-path (its own doc comment), so a
// drifted NESTED path (e.g. "tags.mutated") must override its whole
// CONTAINING top-level attribute's own complete current value ("tags" in
// full), which only Observed -- not the partial before/after diff --
// actually carries.
type DriftedResource struct {
	Address  core.Address
	Fields   []DriftedField
	Observed json.RawMessage
}

// scanDrift walks fleet via core.RunScan, reusing `ubx status --drift`'s
// own EXACT classification mechanism (core.RunScan/core.DiffAttributes/
// core.FilterNormalizationNoise -- classifyFleetEntry in status.go calls
// the identical three functions) -- never a second, competing drift
// algorithm -- and returns every resource that came back
// core.ScanDrifted, plus its own per-field diff. A resource that's
// clean, unreadable, or has no recorded lookup key is silently skipped,
// never an error: a fleet with SOME resources ubx currently can't read
// shouldn't block rendering or override-generation for the ones it CAN.
// Mirrors newStatusCmd's own single-provider/multi-provider branching
// exactly (cli/status.go), so a real user's own [providers] config
// works here identically to how it already does for `ubx status
// --drift`.
func scanDrift(ctx context.Context, ledger *core.Ledger, cfg *Config, providerPath, source, providerVersion, providerConfig string, fleet []core.FleetEntry) ([]DriftedResource, error) {
	salt, err := ledger.Salt()
	if err != nil {
		return nil, err
	}

	scanOne := func(app core.StateReader, provConfig json.RawMessage, provSource string, e core.FleetEntry) (*DriftedResource, error) {
		if len(e.Lookup) == 0 {
			return nil, nil
		}
		res, err := core.RunScan(ctx, app, ledger, core.ScanRequest{
			Address:        e.Address,
			ProviderConfig: provConfig,
			CurrentState:   e.Lookup,
			ProviderSource: provSource,
		})
		if err != nil || res.Outcome != core.ScanDrifted {
			return nil, nil
		}
		before, after, derr := core.DiffAttributes(res.PreviousState, res.Observed)
		if derr != nil {
			return nil, nil
		}
		before, after = core.FilterNormalizationNoise(before, after, res.ResourceSchema)
		var fields []DriftedField
		for _, path := range sortedAttributePaths(before, after) {
			fields = append(fields, DriftedField{Path: path, Before: before[path], After: after[path]})
		}
		if len(fields) == 0 {
			return nil, nil
		}
		return &DriftedResource{Address: e.Address, Fields: fields, Observed: res.Observed}, nil
	}

	var drifted []DriftedResource
	if len(cfg.ThirdpartyProviders) > 0 {
		pool, err := newProviderPool(salt, cfg.ThirdpartyProviders, cfg.Providers, cfg.ProviderConfigs)
		if err != nil {
			return nil, err
		}
		defer pool.Close()

		var declared []resolver.DeclaredProvider
		for _, e := range fleet {
			provSrc, provVer := "", ""
			switch {
			case e.Provider != nil:
				provSrc, provVer = e.Provider.Source, e.Provider.Version
			default:
				if declared == nil {
					declared, err = declaredProvidersForInference(ctx, pool, cfg.ThirdpartyProviders)
					if err != nil {
						return nil, err
					}
				}
				winner, ierr := resolver.InferProvider(declared, e.Address.Type, nil)
				if ierr != nil {
					continue
				}
				provSrc, provVer = winner.Source, winner.Version
			}
			app, provConfig, gerr := pool.Get(ctx, provSrc, provVer)
			if gerr != nil {
				continue
			}
			d, err := scanOne(app, provConfig, provSrc, e)
			if err != nil {
				return nil, err
			}
			if d != nil {
				drifted = append(drifted, *d)
			}
		}
		return drifted, nil
	}

	path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
	if err != nil {
		return nil, err
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	stateReader := newStateReader(client.Provider, salt, source)

	for _, e := range fleet {
		d, err := scanOne(stateReader, json.RawMessage(providerConfig), source, e)
		if err != nil {
			return nil, err
		}
		if d != nil {
			drifted = append(drifted, *d)
		}
	}
	return drifted, nil
}
