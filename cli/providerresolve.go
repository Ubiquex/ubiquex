package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

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
	if err := validateProviderSelection(providerPath, source, version); err != nil {
		return "", "", err
	}
	if providerPath != "" {
		return providerPath, "", nil
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
}

// validateProviderSelection is every check resolveProviderBinary can make
// without touching the network: is a selection present at all, and is it
// coherent. Acquire's own download is the only part that genuinely needs
// I/O, so everything above it lives here where a caller can run it early.
//
// Split out so a pre-flight and the real resolution cannot disagree. The
// alternative, a second copy of the same conditions somewhere earlier in
// a command, is exactly the shape that let ship's own provider gate drift
// from its five siblings in the first place.
func validateProviderSelection(providerPath, source, version string) error {
	switch {
	case providerPath != "" && source != "":
		return fmt.Errorf("--provider and --source are mutually exclusive")
	case providerPath != "":
		return nil
	case source != "":
		if version == "" {
			return fmt.Errorf("--source requires --provider-version (explicit version pins only — no \"latest\" resolution)")
		}
		if _, err := provider.ParseSource(source); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("either --provider or --source (with --provider-version) is required")
	}
}

// hasProviderTable reports whether a stack declares a provider table of
// either kind: [thirdparty_providers] (Terraform registry sources) or
// [providers] (ubx's own dynamic-provider-backed sources).
//
// Both, always, and that is the whole point of it existing. The two
// tables were split in ccd8b8d3 and the "does this stack use a pool"
// condition was then written out by hand in six places. Five got both
// terms; ship got only the first, so a stack declaring nothing but
// [providers] (exactly what `ubx init --dynamic-source` writes) fell to
// ship's legacy single-provider branch and demanded a --provider or
// --source it had no reason to have. The table check is one idea, so it
// is one function now rather than six literals.
func hasProviderTable(cfg *Config) bool {
	return len(cfg.ThirdpartyProviders) > 0 || len(cfg.Providers) > 0
}

// preflightProviderRoute answers "can this stack reach a provider at
// all" from config and flags alone: no acquire, no launch, no network.
//
// It exists for ordering rather than for a new rule. `ubx ship`'s
// signing moment (core.Accept, via confirmAndAccept) appends to an
// append-only ledger, and a ledger cannot un-append. Provider resolution
// used to happen seventy lines later, so a stack with no reachable
// provider got its proposal durably accepted and only then refused, which
// leaves an accepted-but-unshippable record that nothing can retract.
//
// The check costs nothing, so it runs before the signature. Anything
// that genuinely needs the network (Acquire's download, a dynamic
// provider launch) stays where it is: this only rules out the cases that
// were always going to fail.
func preflightProviderRoute(cmd *cobra.Command, cfg *Config, providerPath, source, providerVersion string) error {
	// A table means the pool path, which resolves per-provider and lazily.
	// There is no single up-front selection to validate.
	if hasProviderTable(cfg) {
		return nil
	}
	// Taken by value: applyProviderDefaults fills these from [provider]
	// and the mutation must not leak into the caller's own flag variables,
	// which the real resolution reads again later.
	applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
	return validateProviderSelection(providerPath, source, providerVersion)
}

// inferProviderForType, UBI-49 finding #4's own thirdparty-only answer
// to "which declared provider owns this resource type", is gone.
//
// It launched each [thirdparty_providers] source in turn and asked its
// schema, which was right, but it could only ever see one of the two
// tables: a dynamic provider is not acquired from a registry, so no
// amount of fixing its inputs would have let it find one. cli/scan.go
// now uses declaredProvidersForInference plus resolver.InferProvider
// instead, the same pair status/drift/scanfleet/scanall/scandiscover
// already use, which resolves both tables through the pool.
//
// Recorded rather than silently deleted because the duplicate-launch
// tradeoff it documented is still real and still accepted: a provider is
// launched to read its schema, then used for the real read. That cost
// moved, it did not disappear.
