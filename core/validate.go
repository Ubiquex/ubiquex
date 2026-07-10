package core

import (
	"errors"
	"fmt"
)

// ErrInvalidProposal wraps every propose-time validation failure from
// Validate.
var ErrInvalidProposal = errors.New("invalid proposal")

// Validate enforces propose-time structural rules from docs/schema.md that
// go beyond what the Go type system captures on its own:
//
//   - every Delta.Modifies entry must have a matching Resolution.Inputs
//     entry with a non-empty ObservedHash, so a proposal's claimed "before"
//     is provable against what was actually observed, not just asserted;
//   - kind-specific rules — currently just KindAdoption, which must be
//     record-only (docs/schema.md: all-zero blast_radius, no
//     modifies/destroys).
//
// Accept calls Validate before hashing: a proposal that fails validation
// must never make it into the ledger, and shouldn't spend a hash on
// content already known to violate these rules.
func Validate(p *Proposal) error {
	if err := validateModifiesHaveResolutionInputs(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProposal, err)
	}
	if err := validateKind(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProposal, err)
	}
	return nil
}

func validateModifiesHaveResolutionInputs(p *Proposal) error {
	observedHash := make(map[string]string, len(p.Resolution.Inputs))
	for _, in := range p.Resolution.Inputs {
		if in.Resource != "" {
			observedHash[in.Resource] = in.ObservedHash
		}
	}
	for _, m := range p.Delta.Modifies {
		addr := m.Target.String()
		hash, ok := observedHash[addr]
		if !ok {
			return fmt.Errorf("delta.modifies entry for %q has no matching resolution.inputs entry "+
				"(docs/schema.md requires one, with an observed_hash, for every modified resource)", addr)
		}
		if hash == "" {
			return fmt.Errorf("resolution.inputs entry for %q has an empty observed_hash", addr)
		}
	}
	return nil
}

func validateKind(p *Proposal) error {
	switch p.Kind {
	case KindAdoption:
		if p.BlastRadius != (BlastRadius{}) {
			return fmt.Errorf("adoption proposals must have all-zero blast_radius, got %+v", p.BlastRadius)
		}
		if len(p.Delta.Modifies) != 0 {
			return errors.New("adoption proposals must not have delta.modifies entries (record-only)")
		}
		if len(p.Delta.Destroys) != 0 {
			return errors.New("adoption proposals must not have delta.destroys entries (record-only)")
		}
	}
	return nil
}
