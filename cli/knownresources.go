package cli

import (
	"encoding/json"

	"github.com/ubiquex/ubiquex/core"
)

// knownResourcesForStack computes the target stack's own currently-
// recorded state for the intent provider (UBI-85, intentprovider.
// DraftRequest.KnownResources' own doc comment): every address
// Fleet(stack) reports, each folded to its own full recorded state via
// FoldState, keyed by "<type>.<name>" -- matching how a resources[]
// entry in the drafted intent/v1 document is itself addressed (the
// stack name is already implied by which stack this whole draft is
// scoped to, so it's dropped from the key). Feeding this in before
// drafting is what lets a re-plan against an EXISTING stack recognize
// "this doc still matches what's already shipped" instead of redrafting
// every resource as a fresh create -- UBI-85's own root cause.
//
// A never-seen stack (Fleet returns nothing) returns a nil map, not an
// error -- exactly the pre-UBI-85 "draft as if the stack were empty"
// behavior, since there is nothing to compare against. A resource Fleet
// reports but FoldState can't reconstruct a state for (shouldn't happen
// via a well-formed ledger, see FoldState's own doc comment) is skipped
// rather than failing the whole draft -- one malformed/legacy entry
// should never block drafting against everything else that's fine, the
// same defensive posture this codebase's other Fleet walks
// (cli/status.go, cli/scanfleet.go) already take for their own
// per-resource failures.
func knownResourcesForStack(l *core.Ledger, stack string) (map[string]json.RawMessage, error) {
	fleet, err := l.Fleet(stack)
	if err != nil {
		return nil, err
	}
	if len(fleet) == 0 {
		return nil, nil
	}
	known := make(map[string]json.RawMessage, len(fleet))
	for _, e := range fleet {
		state, found, err := l.FoldState(e.Address)
		if err != nil || !found {
			continue
		}
		known[e.Address.Type+"."+e.Address.Name] = state
	}
	return known, nil
}
