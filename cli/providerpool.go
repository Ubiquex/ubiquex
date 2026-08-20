package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// providerPool is the concrete, cli-side implementation of
// executor.ApplierPool (docs/executor.md's own "Amendment (2026-07-18,
// UBI-43): multi-provider stacks" client pool) -- the one place that
// bridges core/executor's provider-import-free ApplierPool interface and
// the real provider.Acquire/provider.Launch machinery, driven by
// .ubx/config's own [thirdparty_providers]/[providers]/[provider_configs]
// tables (cli/config.go). Launches lazily, on the first Get for a given
// source@version, and never
// more than once per invocation; hands back that provider's own resolved
// config alongside its Applier every time (docs/architecture.md
// §Multi-provider stacks' own per-provider configuration, this session's
// amendment) -- never a single global blob assumed correct for every
// provider.
type providerPool struct {
	mu            sync.Mutex
	versions      map[string]string          // source -> pinned version, from [thirdparty_providers]
	dynamic       map[string]map[string]any  // key -> [providers.<key>] params, ubx's own
	configs       map[string]json.RawMessage // source/key -> resolved provider_config JSON, from [provider_configs]
	launch        launchFunc                 // real provider.Acquire/Launch in production, swappable in tests
	dynamicLaunch launchFunc                 // real ubx-provider-dynamic launch, swappable in tests
	launched      map[string]executor.Applier
	closers       []io.Closer
}

// launchFunc acquires and launches source@version, returning a ready
// Applier plus something to Close() when the pool itself is done -- the
// one seam providerPool's own hermetic tests use to prove its caching/
// config-routing/version-mismatch logic without a real provider binary or
// network access; newRealLaunchFunc is the only production implementation.
type launchFunc func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error)

// newProviderPool builds a providerPool from .ubx/config's own
// [thirdparty_providers]/[provider_configs] tables (versions/configs,
// already decoded -- see applyMultiProviderConfig) plus salt, the same
// per-ledger value newApplier already needs for redaction. Real
// Acquire/Launch, lazy, exactly like the single-provider flow this
// generalizes. dynamic is [providers] (ubx's own, real, dynamic-
// provider-backed sources, this session's own real addition) -- nil or
// empty for every call site that hasn't started declaring one yet, in
// which case Get behaves exactly as it always has.
func newProviderPool(salt []byte, versions map[string]string, dynamic map[string]map[string]any, configs map[string]map[string]any) (*providerPool, error) {
	resolvedConfigs := make(map[string]json.RawMessage, len(configs))
	for source, cfg := range configs {
		b, err := json.Marshal(cfg)
		if err != nil {
			return nil, fmt.Errorf("provider_configs: marshal %q: %w", source, err)
		}
		resolvedConfigs[source] = b
	}
	return &providerPool{
		versions:      versions,
		dynamic:       dynamic,
		configs:       resolvedConfigs,
		launch:        newRealLaunchFunc(salt),
		dynamicLaunch: newDynamicProviderLaunchFunc(salt, dynamic),
		launched:      map[string]executor.Applier{},
	}, nil
}

// newRealLaunchFunc is launchFunc's own production implementation --
// provider.ParseSource/Acquire/Launch, then wrapped into an
// executor.Applier via newApplier, exactly the single-provider flow
// cli/ship.go already used before this session, just parameterized by
// (source, version) instead of hardcoded to the one CLI-flag pair.
func newRealLaunchFunc(salt []byte) launchFunc {
	return func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error) {
		src, err := provider.ParseSource(source)
		if err != nil {
			return nil, nil, err
		}
		result, err := provider.Acquire(ctx, src, version)
		if err != nil {
			return nil, nil, fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
		}
		client, err := provider.Launch(ctx, result.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("launch provider %s@%s: %w", source, version, err)
		}
		return newApplier(client.Provider, salt, source), client, nil
	}
}

// Get satisfies executor.ApplierPool. version is the (source, version)
// pair the resolved proposal itself recorded (core/resolver's own
// inference, docs/resolver.md's amendment) -- version-empty means the
// caller doesn't know or doesn't care (drift_revert's own single-provider
// call shape), never a real request for "any version." A recorded version
// that no longer matches this stack's own currently-pinned one is refused
// outright, not silently substituted: the proposal was signed against a
// specific version, and launching a different one than what was reviewed
// is exactly the kind of silent drift this project exists to catch, not
// reproduce -- re-resolve against the current config instead.
func (p *providerPool) Get(ctx context.Context, source, version string) (executor.Applier, json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, isDynamic := p.dynamic[source]; isDynamic {
		return p.getDynamic(ctx, source)
	}

	pinned, declared := p.versions[source]
	if !declared {
		return nil, nil, fmt.Errorf("provider %q is not declared in this stack's [thirdparty_providers] config", source)
	}
	if version != "" && version != pinned {
		return nil, nil, fmt.Errorf("provider %q is pinned to %s in this stack's [thirdparty_providers] config, but this proposal recorded %s -- re-resolve against the current config", source, pinned, version)
	}

	config := p.configs[source]
	if config == nil {
		config = json.RawMessage("{}")
	}

	key := source + "@" + pinned
	if app, ok := p.launched[key]; ok {
		return app, config, nil
	}

	app, closer, err := p.launch(ctx, source, pinned)
	if err != nil {
		return nil, nil, err
	}
	p.launched[key] = app
	p.closers = append(p.closers, closer)
	return app, config, nil
}

// getDynamic is Get's own real routing branch for a key declared under
// [providers] (ubx's own, dynamic-provider-backed) -- called with p.mu
// already held. Real, deliberate difference from the thirdparty path:
// no version concept at all (ubx-provider-dynamic has no real,
// independently-pinned release process yet, the same honest gap
// dynamicprovider.go's own resolveDynamicProviderBinary doc comment
// already names) -- launched at most once per key, per this pool's own
// lifetime, exactly like a thirdparty provider.
func (p *providerPool) getDynamic(ctx context.Context, key string) (executor.Applier, json.RawMessage, error) {
	config := p.configs[key]
	if config == nil {
		config = json.RawMessage("{}")
	}

	cacheKey := "dynamic:" + key
	if app, ok := p.launched[cacheKey]; ok {
		return app, config, nil
	}

	app, closer, err := p.dynamicLaunch(ctx, key, "")
	if err != nil {
		return nil, nil, err
	}
	p.launched[cacheKey] = app
	p.closers = append(p.closers, closer)
	return app, config, nil
}

// Close closes every provider client this pool actually launched (never
// more than one per declared source, per Get's own caching) -- called
// once, by the CLI command, after executor.Ship/whatever else used this
// pool returns. Errors are collected, not swallowed, but never stop
// closing the rest -- one misbehaving provider subprocess shouldn't leak
// every other one's own handle.
func (p *providerPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	for _, c := range p.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close provider pool: %v", errs)
	}
	return nil
}

// declaredProvidersForInference launches every source in versions via pool
// (its own Get-level caching means a source already launched for an
// explicitly-provider-tagged Fleet entry is never launched twice) and
// wraps each into a resolver.DeclaredProvider backed by
// resourceTypeSchemaInspector -- the set resolver.InferProvider needs to
// answer "which declared provider owns this type" for a legacy/adopted
// Fleet entry with no recorded provider of its own (cli/status.go,
// cli/scanall.go, UBI-43 session 5). Called lazily -- only once the walk
// actually hits its first such entry -- so a stack where every resource
// already carries its own recorded provider never pays for this at all.
// sortedProviderSources keeps the result (and therefore any
// ErrAmbiguousType's own "checked:" listing) in reproducible order.
func declaredProvidersForInference(ctx context.Context, pool *providerPool, versions map[string]string) ([]resolver.DeclaredProvider, error) {
	sources := sortedProviderSources(versions)
	declared := make([]resolver.DeclaredProvider, 0, len(sources))
	for _, source := range sources {
		app, _, err := pool.Get(ctx, source, versions[source])
		if err != nil {
			return nil, fmt.Errorf("declared providers: %w", err)
		}
		_, resourceSchemas, err := app.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("declared providers: %s: schema: %w", source, err)
		}
		declared = append(declared, resolver.DeclaredProvider{
			Source:  source,
			Version: versions[source],
			Schema:  resourceTypeSchemaInspector{resourceSchemas: resourceSchemas},
		})
	}
	return declared, nil
}

// resolvedProviderSource is one real, precedence-resolved entry for a
// given short key -- either ubx's own dynamic-provider-backed source
// (Dynamic=true, Params from [providers.<key>]) or a real
// Terraform-registry thirdparty source (Dynamic=false, Source/Version
// from [thirdparty_providers]).
type resolvedProviderSource struct {
	Key     string
	Dynamic bool
	Params  map[string]any // set when Dynamic
	Source  string         // set when !Dynamic
	Version string         // set when !Dynamic
}

// resolveProviderPrecedence merges cfg.Providers ([providers], ubx's
// own) and cfg.ThirdpartyProviders ([thirdparty_providers], real
// Terraform-registry sources) into one real, deterministic set, keyed by
// short name -- [providers] always wins when the SAME real key is
// declared in both, the founder's own explicit precedence rule for this
// session's two-namespace restructure ("[providers] means ubx's own,
// [thirdparty_providers] means everyone else's ... if both namespaces
// declare the same key, [providers] wins").
//
// [thirdparty_providers] itself stays exactly its real, existing flat
// map[string]string shape (source -> pinned version, never reshaped);
// its own real, comparable short key -- for precedence comparison only,
// this function never changes what's actually stored or how Get looks
// providers up -- is its source string's last "/" segment
// (providerShortName, the identical real derivation cli/sdk.go already
// uses for SDK target-repo naming, reused here rather than a second
// derivation of the same real idea).
func resolveProviderPrecedence(cfg *Config) map[string]resolvedProviderSource {
	out := make(map[string]resolvedProviderSource, len(cfg.ThirdpartyProviders)+len(cfg.Providers))
	for source, version := range cfg.ThirdpartyProviders {
		key := providerShortName(source)
		out[key] = resolvedProviderSource{Key: key, Source: source, Version: version}
	}
	for key, params := range cfg.Providers {
		out[key] = resolvedProviderSource{Key: key, Dynamic: true, Params: params}
	}
	return out
}

// sortedProviderSources returns versions' own keys, sorted -- determinism
// is a feature (CLAUDE.md's own standing rule): a stack's declared
// provider set is a Go map once decoded from TOML, and anything that
// walks it (resolve's own eager schema-fetch loop, an error message
// listing every source checked) must do so in a reproducible order, not
// whatever map iteration happens to produce this run.
func sortedProviderSources(versions map[string]string) []string {
	sources := make([]string, 0, len(versions))
	for s := range versions {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	return sources
}
