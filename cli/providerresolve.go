package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ubiquex/ubiquex/provider"
)

// resolveProviderBinary returns a path to a provider binary ready to
// Launch. Exactly one of providerPath (a direct path — the pre-UBI-8
// manual "download it yourself" workflow, still supported for dev use and
// tests against fakeprovider) or source+version (registry acquisition via
// provider.Acquire, UBI-8) must be given. checksum is
// "sha256:<hex>" of the acquired binary when acquired via source+version,
// or "" for a direct --provider path (nothing was verified to attribute).
func resolveProviderBinary(ctx context.Context, providerPath, source, version string) (path, checksum string, err error) {
	switch {
	case providerPath != "" && source != "":
		return "", "", fmt.Errorf("--provider and --source are mutually exclusive")
	case providerPath != "":
		return providerPath, "", nil
	case source != "":
		if version == "" {
			return "", "", fmt.Errorf("--source requires --provider-version (explicit version pins only — no \"latest\" resolution)")
		}
		src, err := provider.ParseSource(source)
		if err != nil {
			return "", "", err
		}
		result, err := provider.Acquire(ctx, src, version)
		if err != nil {
			return "", "", fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
		}
		return result.Path, "sha256:" + result.SHA256, nil
	default:
		return "", "", fmt.Errorf("either --provider or --source (with --provider-version) is required")
	}
}

// inferProviderForType is UBI-49 finding #4's own answer to "which
// declared provider owns this resource type" for a single-resource,
// read-only caller (cli/scan.go's per-resource mode) that has a
// [thirdparty_providers] table (versions) but no per-resource provider hint to
// consult -- unlike resolver.InferProvider, which works from an
// already-loaded []resolver.DeclaredProvider (each carrying a live
// SchemaInspector), this launches each declared source in turn (sorted,
// for reproducible "checked:" listings) and asks its own real schema,
// stopping at the first real owner. A provider is launched and asked its
// schema here -- a free, local, no-credentials gRPC call (this project's
// own established "GetProviderSchema costs nothing" finding, STATE.md) --
// and then launched again by the caller for the real read; the same
// known, accepted duplication documented in plan.go for an identical
// situation (--from-diagram's own double provider-load), not a new
// inefficiency this session invented.
func inferProviderForType(ctx context.Context, versions map[string]string, typeName string) (source, version string, err error) {
	sources := sortedProviderSources(versions)
	for _, src := range sources {
		ver := versions[src]
		parsed, perr := provider.ParseSource(src)
		if perr != nil {
			return "", "", perr
		}
		result, aerr := provider.Acquire(ctx, parsed, ver)
		if aerr != nil {
			return "", "", fmt.Errorf("acquire provider %s@%s: %w", src, ver, aerr)
		}
		client, lerr := provider.Launch(ctx, result.Path)
		if lerr != nil {
			return "", "", fmt.Errorf("launch provider %s@%s: %w", src, ver, lerr)
		}
		schemas, serr := client.Provider.Schema(ctx)
		cerr := client.Close()
		if serr != nil {
			return "", "", fmt.Errorf("provider %s@%s: schema: %w", src, ver, serr)
		}
		if cerr != nil {
			return "", "", cerr
		}
		if _, ok := schemas.Resources[typeName]; ok {
			return src, ver, nil
		}
	}
	return "", "", fmt.Errorf("no declared provider's schema has type %q -- checked: %s", typeName, strings.Join(sources, ", "))
}
