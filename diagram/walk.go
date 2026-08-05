// walk.go is UBI-86's own extraction of Emit's (D2, emit.go) own fleet
// walk into a shared, medium-agnostic step: real, resolved-time truth
// read off the ledger exactly once, regardless of which render target
// (D2 or `ubx render --md`'s own markdown) consumes it. Emit itself now
// calls Walk and converts its own map[string]interface{} attrs from
// Walk's json.RawMessage-per-field shape -- see Walk's own doc comment
// for why the raw form is kept here instead of decoding straight into
// interface{} the way emit.go's own pre-UBI-86 code used to.
package diagram

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ubiquex/ubiquex/core"
)

// Resource is one live resource's own render-time view, gathered by
// Walk -- real, resolved-time truth pulled from the ledger (Fleet,
// FoldState, and the resource's own creating proposal), never re-derived
// twice for two different output formats.
//
// Attrs stays map[string]json.RawMessage (one raw JSON value per
// top-level attribute), not map[string]interface{} the way emit.go's
// pre-UBI-86 code decoded straight to -- a caller needs the RAW bytes to
// call core.IsRedactedValue per attribute before rendering it (a
// redacted attribute's own decoded interface{} form is
// indistinguishable from an ordinary nested object once decoded, so the
// check has to happen before that step, not after).
type Resource struct {
	Addr core.Address
	// Attrs is this resource's own current, live, top-level attribute
	// set -- already redacted by provider.Redact at the StateReader
	// boundary (any sensitive value already arrives as
	// {"$redacted":{"sha256":"..."}}, core.IsRedactedValue's own marker
	// shape) long before Walk ever reads it.
	Attrs map[string]json.RawMessage
	// DependsOn is this resource's own canonical addresses it depends
	// on, within this stack only -- real, resolved-time truth pulled
	// from its own creating/modifying proposal, never guessed at.
	DependsOn []string
	// CrossPins is every cross_stack_pin resolution.inputs entry FROM
	// this resource, recorded on its own most-recently-touching
	// proposal (unlike DependsOn/BlueprintRef, which must come from the
	// CREATING proposal specifically -- see creatingProposalFor's own
	// doc comment for why the two differ).
	CrossPins []core.ResolutionInput
	// BlueprintRef is "" for an ordinary resource; otherwise the
	// "<name>:<content_hash>" blueprint.ExpandCalls stamped this
	// resource's own creating create-node with (UBI-74 Slice 6). Real
	// resolved-time truth pulled from the resource's own creating
	// proposal, the grouping basis both Emit's dashed D2 container and
	// mdrender's own equivalent markdown section use.
	BlueprintRef string
}

// Walk gathers stack's own live resources from l, sorted by (type,
// name) -- the shared read Emit (D2) and mdrender's own markdown
// emitter both build on, so blueprint grouping/DependsOn/CrossPins are
// computed exactly once, never re-derived a second way per output
// format.
func Walk(l *core.Ledger, stack string) ([]Resource, error) {
	fleet, err := l.Fleet(stack)
	if err != nil {
		return nil, fmt.Errorf("diagram: walk %s: %w", stack, err)
	}

	sort.Slice(fleet, func(i, j int) bool {
		if fleet[i].Address.Type != fleet[j].Address.Type {
			return fleet[i].Address.Type < fleet[j].Address.Type
		}
		return fleet[i].Address.Name < fleet[j].Address.Name
	})

	proposals := map[string]*core.Proposal{}
	getProposal := func(id string) (*core.Proposal, error) {
		if id == "" {
			return nil, nil
		}
		if p, ok := proposals[id]; ok {
			return p, nil
		}
		p, err := l.Read(id)
		if err != nil {
			return nil, err
		}
		proposals[id] = p
		return p, nil
	}

	resources := make([]Resource, 0, len(fleet))
	for _, entry := range fleet {
		state, found, err := l.FoldState(entry.Address)
		if err != nil {
			return nil, fmt.Errorf("diagram: walk %s: %w", entry.Address, err)
		}
		if !found {
			return nil, fmt.Errorf("diagram: walk %s: reported live by Fleet but FoldState found nothing", entry.Address)
		}
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(state, &attrs); err != nil {
			return nil, fmt.Errorf("diagram: walk %s: decode state: %w", entry.Address, err)
		}

		p, err := getProposal(entry.ProposalID)
		if err != nil {
			return nil, fmt.Errorf("diagram: walk %s: read proposal %s: %w", entry.Address, entry.ProposalID, err)
		}

		// See creatingProposalFor's own doc comment (emit.go): Fleet's
		// entry.ProposalID is the latest TOUCHING proposal, correct for
		// CrossPins below, but wrong for DependsOn/BlueprintRef, both of
		// which only ever live on the address's own CREATE node.
		creating, err := creatingProposalFor(l, entry.Address)
		if err != nil {
			return nil, fmt.Errorf("diagram: walk %s: %w", entry.Address, err)
		}

		var dependsOn []string
		var crossPins []core.ResolutionInput
		var blueprintRef string
		if creating != nil {
			dependsOn = dependsOnFor(creating, entry.Address)
			blueprintRef, _ = blueprintSourceFor(creating, entry.Address)
		}
		if p != nil {
			addrStr := entry.Address.String()
			for _, in := range p.Resolution.Inputs {
				if in.Kind == "cross_stack_pin" && in.From == addrStr {
					crossPins = append(crossPins, in)
				}
			}
		}

		resources = append(resources, Resource{
			Addr:         entry.Address,
			Attrs:        attrs,
			DependsOn:    dependsOn,
			CrossPins:    crossPins,
			BlueprintRef: blueprintRef,
		})
	}

	return resources, nil
}
