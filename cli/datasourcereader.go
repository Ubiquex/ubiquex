package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// attachDataSourceReaders is resolver.Resolve's own real dependency for
// piece 3 of UBI-178 (docs/schema.md's own "Amendment: data sources"):
// a live core.StateReader for whichever declared provider(s) actually
// own a type referenced by dataSources, mutating providers in place
// (the same backing array every one of `ubx resolve`/`ubx plan`/
// `ubx promote`/`ubx terminate` already built via loadResolveProviders
// -- no new slice, no copy).
//
// Gated entirely on len(dataSources) > 0: a stack with no data_sources[]
// returns (nil, nil) immediately, before ever touching cfg/ledger/a real
// provider connection -- zero behavior change, zero extra connection
// lifetime, for the overwhelming majority of `ubx resolve`-family
// invocations that never declare one. The returned io.Closer is nil in
// exactly that case too -- callers guard `if closer != nil { defer
// closer.Close() }`, never an unconditional defer.
//
// Two real launch shapes, mirroring loadResolveProviders' own existing
// branch on the identical condition (resolveProviderPrecedence(cfg)):
//   - A real [thirdparty_providers]/[providers] config: reuses
//     providerPool (cli/providerpool.go), the same real, already-tested
//     multi-provider client cache `ubx ship`/`ubx status`/`ubx scan`
//     already use -- one live connection per distinct owning provider,
//     never one per data source.
//   - The legacy single --provider-path/--source/--version flags (no
//     config table at all, docs/resolver.md's own staged retirement
//     plan): launches that one provider directly, exactly like
//     loadResolveProviders' own legacy branch already does for schema,
//     just without closing the connection immediately afterward.
//
// providerConfig for the legacy path is an empty object -- the legacy
// flag shape has never carried a real per-provider config block the way
// [provider_configs] does (loadResolveProviders' own legacy branch
// never populates one either, for the identical real reason: nothing
// downstream of a schema-only fetch has ever needed it before this).
func attachDataSourceReaders(ctx context.Context, cfg *Config, ledger *core.Ledger, providers []resolver.DeclaredProvider, dataSources []resolver.DataSourceIntent, providerPath, source, providerVersion string) (io.Closer, error) {
	if len(dataSources) == 0 {
		return nil, nil
	}

	salt, err := ledger.Salt()
	if err != nil {
		return nil, fmt.Errorf("resolve data sources: %w", err)
	}

	if len(resolveProviderPrecedence(cfg)) == 0 {
		path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
		if err != nil {
			return nil, fmt.Errorf("resolve data sources: %w", err)
		}
		client, err := provider.Launch(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("resolve data sources: %w", err)
		}
		applier := newApplier(client.Provider, salt, source)
		for i := range providers {
			providers[i].Reader = applier
			providers[i].ProviderConfig = json.RawMessage("{}")
		}
		return client, nil
	}

	pool, err := newProviderPool(salt, cfg.ThirdpartyProviders, cfg.Providers, cfg.ProviderConfigs)
	if err != nil {
		return nil, fmt.Errorf("resolve data sources: %w", err)
	}
	if err := attachDataSourceReadersFromPool(ctx, pool, providers, dataSources); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// attachDataSourceReadersFromPool is attachDataSourceReaders' own real
// routing core, split out so a hermetic test can exercise it directly
// against a pool built the same way cli/providerpool_test.go's own
// newTestPool already does (a real *providerPool, launch/dynamicLaunch
// swapped for a fake afterward) -- without needing a real provider
// binary, network access, or a real ledger's own salt. attachDataSourceReaders
// itself (the real production entry point) is what builds pool for
// real; this function only ever routes and mutates providers in place.
func attachDataSourceReadersFromPool(ctx context.Context, pool *providerPool, providers []resolver.DeclaredProvider, dataSources []resolver.DataSourceIntent) error {
	seen := map[string]bool{}
	for _, di := range dataSources {
		prov, err := resolver.InferProvider(providers, di.Type, nil)
		if err != nil {
			return fmt.Errorf("resolve data source %s.%s: %w", di.Type, di.Name, err)
		}
		if seen[prov.Source] {
			continue
		}
		seen[prov.Source] = true

		applier, providerConfig, err := pool.Get(ctx, prov.Source, prov.Version)
		if err != nil {
			return fmt.Errorf("resolve data sources: connect to %s: %w", prov.Source, err)
		}
		for i := range providers {
			if providers[i].Source == prov.Source && providers[i].Version == prov.Version {
				providers[i].Reader = applier
				providers[i].ProviderConfig = providerConfig
			}
		}
	}
	return nil
}
