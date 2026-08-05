// override.go is UBI-86 Part 2's own mechanism: the design comment on
// UBI-86 (2026-08-04) names the real problem precisely -- a blueprint's
// own internal resource attributes are owned by the blueprint's AUTHOR,
// not the caller; only call-site parameters are editable by whoever
// instantiates it. If a caller drifts (via console/CLI) a value the
// blueprint never exposed as a parameter and adopts it, the ledger is
// correct but there's no source-of-truth text the caller owns to edit to
// make that adoption "stick" against a future re-call of the blueprint.
//
// The fix, same idea as Terraform's own *_override.tf: a caller can
// override any resolved attribute by address, applied AFTER a blueprint
// call resolves (ExpandCalls, invoke.go) and BEFORE resolver.Resolve
// ever runs -- an override's own value flows through the identical
// $ref/$cross resolution machinery any other config value does, never a
// bolted-on post-resolve patch. See resolver.Override/IntentFile.
// Overrides's own doc comment for the wire shape, and docs/blueprint.md's
// own "Override mechanism" section for the full design record.
package blueprint

import (
	"encoding/json"
	"fmt"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// ApplyOverrides merges every intent.Overrides entry into its target
// resource's own Config (by address), then clears Overrides -- mirroring
// ExpandCalls' own "expand once, then clear" convention exactly (called
// immediately after it in cli/resolve.go/cli/plan.go). A no-op, and
// never allocates, for a document with no overrides at all -- the common
// case, unaffected.
//
// Works against ANY resource address in this same document, not only a
// blueprint-produced one -- the motivating case is a blueprint's own
// unparameterized internal field, but there's no mechanical reason to
// forbid overriding a hand-authored resource too, and special-casing
// that would add complexity this design doesn't need.
//
// An override targeting a resource outside this document's own Stack, or
// naming no resource that actually exists in intent.Resources (checked
// AFTER blueprint calls have already been expanded into real resources,
// so an override CAN target a resource a blueprint call just produced),
// is a hard, named error -- never silently ignored.
func ApplyOverrides(intent *resolver.IntentFile) error {
	if len(intent.Overrides) == 0 {
		return nil
	}

	byAddr := make(map[string]int, len(intent.Resources))
	for i, ri := range intent.Resources {
		byAddr[ri.Type+"."+ri.Name] = i
	}

	for _, ov := range intent.Overrides {
		addr, ok := core.ParseAddress(ov.Address)
		if !ok {
			return fmt.Errorf("override %q: not a valid <stack>.<type>.<name> address", ov.Address)
		}
		if addr.Stack != intent.Stack {
			return fmt.Errorf("override %q: targets stack %q, this document is stack %q -- an override can only target its own calling stack's own resources", ov.Address, addr.Stack, intent.Stack)
		}
		idx, found := byAddr[addr.Type+"."+addr.Name]
		if !found {
			return fmt.Errorf("override %q: no such resource in this document (checked after blueprint calls were expanded)", ov.Address)
		}

		var config map[string]json.RawMessage
		if err := json.Unmarshal(intent.Resources[idx].Config, &config); err != nil {
			return fmt.Errorf("override %q: decode existing config: %w", ov.Address, err)
		}
		if config == nil {
			config = map[string]json.RawMessage{}
		}
		for field, val := range ov.Config {
			if err := checkOverridePolicy(addr, field); err != nil {
				return fmt.Errorf("override %s.%s: %w", ov.Address, field, err)
			}
			config[field] = val
		}

		merged, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("override %q: %w", ov.Address, err)
		}
		intent.Resources[idx].Config = merged
	}

	intent.Overrides = nil
	return nil
}

// checkOverridePolicy is UBI-86's own REAL, NAMED security requirement,
// not yet enforced: an override must not be able to bypass a field a
// blueprint's own BOUND POLICY protects. UBI-118 (the bound + org-wide
// policy engine this would check against) was split off UBI-74 entirely
// before Slice 1 even started and remains HELD -- as of this function's
// own writing, there is zero policy-engine scaffolding anywhere in this
// codebase (core/, blueprint/) to enforce against. Per UBI-86's own
// explicit instruction ("this may mean documenting the requirement and
// adding a TODO enforcement hook rather than full enforcement -- decide
// and state explicitly which"): this is that hook, decided and stated
// explicitly here. It always returns nil today -- a real, documented gap,
// never silently hidden -- and it is the ONLY call site ApplyOverrides
// ever routes a field-level override through, so wiring in real
// enforcement once UBI-118 exists is a one-function change, not a
// redesign.
func checkOverridePolicy(addr core.Address, field string) error {
	return nil
}
