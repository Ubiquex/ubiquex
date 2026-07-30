package cli

import (
	"context"
	"fmt"

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
