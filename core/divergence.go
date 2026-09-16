package core

import "fmt"

// divergence.go names one idea: a resource whose current state was set
// deliberately to something the stack's declared source does not describe.
//
// # Why this is not called "restore"
//
// Restoring to an earlier head is the case that made the gap visible, and
// it is one instance rather than the concept. A stack's declared source
// says what a resource should be. Most proposals move a resource TOWARD
// that. A few deliberately move it away, and afterwards the declaration
// and the ledger disagree on purpose.
//
// Two kinds do this today:
//
//	restore       puts a resource back to an earlier head's own value
//	drift_revert  puts a resource back to what the ledger already believed
//
// Both leave the same residue, and it is the residue that matters: the
// next `ubx plan` reads the declaration, sees live state differs, and
// proposes changing it back. Correctly, by its own rules, having no way to
// know the difference was chosen. So a restore someone shipped on purpose
// gets quietly undone by the next routine plan, and nothing in that plan
// says the difference was deliberate.
//
// Naming this for `restore` would have meant building it again the first
// time someone noticed drift_revert has the identical problem. The
// mechanism is named for the property both share.
//
// # What it deliberately does not claim
//
// Not "this plan would undo a restore". That is a strong claim and it
// cannot be made safely: a plan touching one attribute of a diverged
// resource is not necessarily reversing the divergence, and after enough
// later changes the claim becomes a guess.
//
// What it claims instead is always true and leaves the conclusion to the
// reader: the most recent change to this resource was a <kind>, and here
// is which one. An overclaiming marker on a legitimate move-forward is
// exactly how people learn to skip a warning, and this one has to survive
// being read many times.

// DivergenceKind names why a resource's state departs from the declared
// source. A string rather than a ProposalKind: a restore is an ordinary
// kind:change proposal distinguished only by its own intent source, so the
// two vocabularies genuinely differ.
type DivergenceKind string

const (
	// DivergenceRestore: a proposal that reproduced an earlier head's own
	// shape. Identified by its intent source, not its kind.
	DivergenceRestore DivergenceKind = "restore"
	// DivergenceDriftRevert: a proposal that put a resource back to what
	// the ledger already believed, against observed drift.
	DivergenceDriftRevert DivergenceKind = "drift_revert"
)

// Divergence is one deliberate departure from the declared source.
type Divergence struct {
	// ProposalID is the proposal that established it.
	ProposalID string
	Kind       DivergenceKind
	// TargetHead is populated for DivergenceRestore only: the head that
	// proposal reproduced. Empty otherwise.
	TargetHead string
	// ResolvedAt is when that proposal was resolved, so a reader can tell
	// a divergence from ten minutes ago from one from last quarter. Those
	// two deserve different amounts of attention and the mechanism should
	// not pretend otherwise.
	ResolvedAt string
}

// DivergenceOf reports whether a proposal deliberately moved a resource
// away from what the declared source describes.
//
// Reads the proposal itself rather than any recorded flag. Nothing new is
// stored for this: a restore already records {kind: "restore", ref: <head>}
// in its intent sources as evidence, and a drift_revert already says so in
// its own proposal kind. The information was there; nothing looked.
func DivergenceOf(p *Proposal) (Divergence, bool) {
	if p == nil {
		return Divergence{}, false
	}
	if p.Kind == KindDriftRevert {
		return Divergence{
			ProposalID: p.ID,
			Kind:       DivergenceDriftRevert,
			ResolvedAt: p.Resolution.ResolvedAt,
		}, true
	}
	for _, s := range p.Intent.Sources {
		if s.Kind != string(DivergenceRestore) {
			continue
		}
		return Divergence{
			ProposalID: p.ID,
			Kind:       DivergenceRestore,
			TargetHead: s.Ref,
			ResolvedAt: p.Resolution.ResolvedAt,
		}, true
	}
	return Divergence{}, false
}

// LastDivergence reports whether the state an address holds right now was
// set by a deliberate departure from the declared source.
//
// Built on LastContributor rather than on a scan for the most recent
// restore, and the difference is the whole point. A restore that was
// superseded by an ordinary change is no longer what the resource holds,
// so warning about it would be wrong. Only a divergence that is still the
// current truth is worth saying anything about.
func (l *Ledger) LastDivergence(addr Address) (Divergence, bool, error) {
	p, found, err := l.LastContributor(addr)
	if err != nil {
		return Divergence{}, false, fmt.Errorf("last divergence: %w", err)
	}
	if !found {
		return Divergence{}, false, nil
	}
	d, ok := DivergenceOf(p)
	return d, ok, nil
}
