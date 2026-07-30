package cli

import (
	"fmt"
	"os"

	"github.com/ubiquex/ubiquex/intentprovider"
	"github.com/ubiquex/ubiquex/intentprovider/claude"
)

// buildIntentAdapter resolves cfg.Intent (the [intent] config table,
// UBI-41) into a real intentprovider.Adapter. "claude" (this session's
// only real adapter, session 2) is the default when Adapter is unset --
// matching the ticket's own roster order, Claude first.
//
// A package-level var, not a plain func -- the same test-only-injection
// seam configSearchStartDir/openRemoteLedgerStore already establish
// elsewhere in this package: `ubx propose --from-doc`'s own hermetic
// tests swap this for a fake Adapter constructor, never touching the
// real network, without needing a parallel "launch a fake subprocess"
// mechanism the way provider/ledger-store tests do (there's no binary to
// launch here -- Claude's own adapter speaks HTTP directly).
var buildIntentAdapter = func(cfg *Config) (intentprovider.Adapter, error) {
	adapterName := cfg.Intent.Adapter
	if adapterName == "" {
		adapterName = "claude"
	}

	switch adapterName {
	case "claude":
		apiKey, err := resolveKeyRef(cfg.Intent.KeyRef)
		if err != nil {
			return nil, fmt.Errorf("intent: %w", err)
		}
		return claude.New(claude.Config{Model: cfg.Intent.Model, APIKey: apiKey}), nil
	default:
		return nil, fmt.Errorf("intent: adapter %q is not supported yet -- only \"claude\" is built", adapterName)
	}
}

// resolveKeyRef dereferences [intent].key_ref -- never material itself
// (docs/intent-provider.md's own "Component 2" section). An entirely
// unset key_ref is a deliberate, valid choice to rely on ambient
// credentials (the adapter's own SDK default chain, e.g. `ant auth
// login`) -- silent fall-through is correct there. A key_ref that names
// an environment variable which turns out to be unset or empty is a real
// misconfiguration, not an ambient-credentials choice, and is refused
// loudly rather than silently falling through to a chain the operator
// never asked for.
func resolveKeyRef(ref KeyRefConfig) (string, error) {
	if ref.Env == "" {
		return "", nil
	}
	v := os.Getenv(ref.Env)
	if v == "" {
		return "", fmt.Errorf("key_ref.env names %q, but that environment variable is unset or empty", ref.Env)
	}
	return v, nil
}
