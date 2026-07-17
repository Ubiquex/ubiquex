package core

import (
	"encoding/json"
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
//   - every Delta.Destroys entry must have a matching Resolution.Inputs
//     entry of kind "destroy_target" with a non-empty ObservedHash AND
//     Lookup (docs/schema.md — "Amendment: destroys", UBI-30) — a destroy
//     target needs a lookup key the way a modify's own "before" doesn't,
//     since the executor's freshness recheck and reconcile-by-query have
//     no other way to find the resource again;
//   - kind-specific rules: KindAdoption must be record-only (docs/schema.md:
//     all-zero blast_radius, no modifies/destroys). KindDriftAdopt is also
//     record-only against the cloud (all-zero blast_radius, no destroys) but
//     — unlike adoption — is expected to carry delta.modifies, since its
//     whole point is recording an already-happened change (see core/scan.go,
//     GenerateProposal).
//
// Accept calls Validate before hashing: a proposal that fails validation
// must never make it into the ledger, and shouldn't spend a hash on
// content already known to violate these rules.
func Validate(p *Proposal) error {
	if err := validateModifiesHaveResolutionInputs(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProposal, err)
	}
	if err := validateDestroysHaveResolutionInputs(p); err != nil {
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

// validateDestroysHaveResolutionInputs enforces docs/schema.md's
// "Amendment: destroys" (UBI-30): every delta.destroys entry needs its own
// dedicated resolution.inputs entry, kind "destroy_target" specifically
// (not just any entry sharing its Resource string, unlike modifies' looser
// check above) — carrying both a non-empty ObservedHash (the same
// "provable against what was actually observed" guarantee modifies already
// has) and a non-empty Lookup (required, not merely omitempty the way it
// is for a live_state entry: the executor's freshness recheck and
// reconcile-by-query have no other way to find the resource again).
func validateDestroysHaveResolutionInputs(p *Proposal) error {
	type destroyEvidence struct {
		observedHash string
		lookup       json.RawMessage
	}
	evidence := make(map[string]destroyEvidence, len(p.Resolution.Inputs))
	for _, in := range p.Resolution.Inputs {
		if in.Kind != "destroy_target" || in.Resource == "" {
			continue
		}
		evidence[in.Resource] = destroyEvidence{observedHash: in.ObservedHash, lookup: in.Lookup}
	}
	for _, d := range p.Delta.Destroys {
		addr := d.Address.String()
		ev, ok := evidence[addr]
		if !ok {
			return fmt.Errorf("delta.destroys entry for %q has no matching resolution.inputs entry with kind \"destroy_target\" "+
				"(docs/schema.md requires one, with an observed_hash and lookup, for every destroy target)", addr)
		}
		if ev.observedHash == "" {
			return fmt.Errorf("resolution.inputs destroy_target entry for %q has an empty observed_hash", addr)
		}
		if len(ev.lookup) == 0 {
			return fmt.Errorf("resolution.inputs destroy_target entry for %q has an empty lookup", addr)
		}
	}
	return nil
}

func validateKind(p *Proposal) error {
	switch p.Kind {
	case KindAdoption:
		if err := requireZeroBlastRadius(p); err != nil {
			return err
		}
		if len(p.Delta.Modifies) != 0 {
			return errors.New("adoption proposals must not have delta.modifies entries (record-only)")
		}
		if len(p.Delta.Destroys) != 0 {
			return errors.New("adoption proposals must not have delta.destroys entries (record-only)")
		}
	case KindDriftAdopt:
		if err := requireZeroBlastRadius(p); err != nil {
			return err
		}
		if len(p.Delta.Destroys) != 0 {
			return errors.New("drift_adopt proposals must not have delta.destroys entries (record-only)")
		}
	case KindDriftRevert:
		if err := validateDriftRevert(p); err != nil {
			return err
		}
	case KindChange:
		if err := validateChange(p); err != nil {
			return err
		}
	}
	return nil
}

// validateChange enforces docs/schema.md's "Amendment: intent files and
// resolved change proposals" (UBI-27) together with its later "Amendment:
// destroys" (UBI-30): a change proposal's blast radius is real (like
// drift_revert's — accepting one is a decision to actually change cloud),
// across all three delta arrays now, not just creates/modifies. Destroys
// were forbidden unconditionally for a change proposal from UBI-27 until
// this session (docs/resolver.md's own v1 scope line, superseded by
// docs/resolver.md's "Amendment (UBI-30): destroys") — that restriction is
// gone; a change proposal may now legitimately carry delta.destroys.
func validateChange(p *Proposal) error {
	if want := int64(len(p.Delta.Creates)); p.BlastRadius.Creates != want {
		return fmt.Errorf("change proposals must have blast_radius.creates == len(delta.creates) (%d), got %d", want, p.BlastRadius.Creates)
	}
	if want := int64(len(p.Delta.Modifies)); p.BlastRadius.Modifies != want {
		return fmt.Errorf("change proposals must have blast_radius.modifies == len(delta.modifies) (%d), got %d", want, p.BlastRadius.Modifies)
	}
	if want := int64(len(p.Delta.Destroys)); p.BlastRadius.Destroys != want {
		return fmt.Errorf("change proposals must have blast_radius.destroys == len(delta.destroys) (%d), got %d", want, p.BlastRadius.Destroys)
	}
	return nil
}

// validateDriftRevert enforces docs/schema.md's "Amendment: drift_revert
// proposals": drift_revert is the one kind whose blast_radius is real, not
// all-zero -- accepting it is a decision to actually change cloud (see
// docs/architecture.md's Revert path), not a record of something that
// already happened.
func validateDriftRevert(p *Proposal) error {
	if len(p.Delta.Creates) != 0 {
		return errors.New("drift_revert proposals must not have delta.creates entries")
	}
	if len(p.Delta.Destroys) != 0 {
		return errors.New("drift_revert proposals must not have delta.destroys entries")
	}
	if len(p.Delta.Modifies) == 0 {
		return errors.New("drift_revert proposals must have at least one delta.modifies entry")
	}
	if p.BlastRadius.Creates != 0 || p.BlastRadius.Destroys != 0 {
		return fmt.Errorf("drift_revert proposals must have zero blast_radius.creates/destroys, got %+v", p.BlastRadius)
	}
	if want := int64(len(p.Delta.Modifies)); p.BlastRadius.Modifies != want {
		return fmt.Errorf("drift_revert proposals must have blast_radius.modifies == len(delta.modifies) (%d), got %d "+
			"-- accepting a revert is a decision to change cloud, unlike adopt/drift_adopt's record-only zero blast radius",
			want, p.BlastRadius.Modifies)
	}
	return nil
}

func requireZeroBlastRadius(p *Proposal) error {
	if p.BlastRadius != (BlastRadius{}) {
		return fmt.Errorf("%s proposals must have all-zero blast_radius, got %+v", p.Kind, p.BlastRadius)
	}
	return nil
}
