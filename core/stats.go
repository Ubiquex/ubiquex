package core

import (
	"fmt"
	"time"
)

// StatsOptions filters Stats' own walk of the ledger. Stack narrows
// reporting to one stack (required for a remote per-stack store, the
// same convention every other read-only projection in this arc already
// holds); Since/Until window by Acceptance.AcceptedAt -- "give me stats
// for proposals that entered the ledger in this window," the natural
// reading of a ledger-history query, distinct from Resolution.ResolvedAt
// (when the proposal's own content was resolved, which TimeToDecision
// itself measures the gap from).
type StatsOptions struct {
	Stack string
	Since *time.Time
	Until *time.Time
}

// StatsResult is Stats' own report. Each drift_adopt event's own final
// outcome (tallied into DriftResolvedReverted/DriftResolvedAdopted/
// DriftOpen below) is one of three kinds -- see Stats' own doc comment
// for the full reasoning behind this specific, deliberately narrow,
// ledger-computable definition of "resolved" vs "still open":
//   - reverted: a drift_revert for the same address followed this
//     drift_adopt directly -- an explicit, deliberate close-out.
//   - adopted (superseded): a LATER drift-related proposal (almost
//     always another drift_adopt -- the resource drifted again) for the
//     same address followed this one -- never explicitly reverted, but
//     no longer the address's own current state either; counted as
//     resolved because the team's own real behavior was to keep
//     accepting the new reality each time, a legitimate, real resolution
//     path, not an oversight to paper over as "open" just because no
//     revert ever happened.
//   - open: this drift_adopt is the address's own most recent
//     drift-related touch -- accepted once, never explicitly revisited
//     since (no later drift_revert, no later re-drift).
type StatsResult struct {
	TotalProposals   int
	ByKind           map[ProposalKind]int
	AcceptanceMethod map[string]int // "local" | "pr_merge" -> count, every proposal kind
	DriftByStack     map[string]int // drift_adopt + drift_revert count, per stack

	// DriftEvents is the count of drift_adopt proposals only -- see
	// Stats' own doc comment for why drift_revert is deliberately NOT a
	// second "surfaced" event of its own, but the resolution mechanism
	// for a prior one.
	DriftEvents           int
	DriftResolvedReverted int
	DriftResolvedAdopted  int // DriftResolvedSuperseded, tallied
	DriftOpen             int

	// TimeToDecision is the mean gap between a drift proposal's own
	// Resolution.ResolvedAt and Acceptance.AcceptedAt, across every
	// drift_adopt/drift_revert proposal with both timestamps parseable
	// (TimeToDecisionSamples names exactly how many contributed -- never
	// silently averaged over a partial, unnamed subset).
	TimeToDecision        time.Duration
	TimeToDecisionSamples int

	// AttributedDriftAdopts/TotalDriftAdoptsForAttribution: attribution
	// coverage's own numerator/denominator, named separately rather than
	// pre-divided so a caller can render "N of M" as well as a
	// percentage, and so a zero-drift ledger renders as "no data," never
	// a misleading 0% or a divide-by-zero.
	AttributedDriftAdopts          int
	TotalDriftAdoptsForAttribution int
}

// DriftResolutionPct returns the signed-flow resolution percentage (0-100)
// -- reverted + superseded, over every drift_adopt event -- or (0, false)
// if there were none to compute it from.
func (r *StatsResult) DriftResolutionPct() (float64, bool) {
	if r.DriftEvents == 0 {
		return 0, false
	}
	resolved := r.DriftResolvedReverted + r.DriftResolvedAdopted
	return float64(resolved) / float64(r.DriftEvents) * 100, true
}

// AttributionCoveragePct returns the attribution coverage percentage
// (0-100), or (0, false) if there were no drift_adopt proposals to
// compute it from.
func (r *StatsResult) AttributionCoveragePct() (float64, bool) {
	if r.TotalDriftAdoptsForAttribution == 0 {
		return 0, false
	}
	return float64(r.AttributedDriftAdopts) / float64(r.TotalDriftAdoptsForAttribution) * 100, true
}

// Stats folds the ledger into decision-flow metrics -- read-only, offline,
// no provider, entirely from data the ledger already has (docs/plan.md's
// own "every resolved drift appends to a ledger" framing).
//
// The thesis metric ("% of surfaced drifts resolved through the signed
// flow") deserves its own honest account, not a hand-wave: the ledger, by
// construction (core.Ledger.Append requires an id, which only the accept
// path ever assigns), contains ONLY accepted proposals -- there is no
// durable record anywhere in this system of a drift that was detected
// (`ubx scan`) but never accepted. A drift the tool was never told about
// at all, or one a human saw and simply never acted on, leaves no trace
// for an offline ledger fold to find -- confirmed by reading core.Ledger.
// Append, core/scan.go's own generators, and the LedgerStore interface in
// full before writing this function, not assumed. This means the TRUE
// month-6 thesis measurement (comparing against independently-known
// real-world drift counts) needs external ground truth this command
// cannot supply on its own -- named here explicitly rather than silently
// claiming a number this function structurally cannot produce.
//
// What IS honestly, meaningfully computable from ledger data alone, and
// what this function actually reports: walk every address's own ordered
// sequence of drift-related touches (drift_adopt/drift_revert, in chain
// order). Each drift_adopt is one surfaced-drift event, classified by
// what (if anything) followed it for that same address -- a drift_revert
// immediately after (an explicit, deliberate close-out, "resolved:
// reverted"), any other later drift-related touch (almost always the
// resource drifted again -- never explicitly reverted, but no longer the
// address's own current state either; "resolved: adopted/superseded,"
// the team's own practical "just accept the new reality each time"
// behavior, a real resolution path, not an oversight), or nothing later
// at all ("open": still the address's own current, unrevisited, accepted
// state).
//
// A drift_revert does NOT always belong to a preceding drift_adopt,
// confirmed by reading core/scan.go's own GenerateRevertProposal and
// docs/architecture.md's own "Revert path" section before assuming
// otherwise: `ubx scan --propose both` generates drift_adopt and
// drift_revert as ALTERNATIVE responses to the SAME freshly-detected
// drift, sharing one parent -- only one is ever accepted. A team that
// chooses "revert" outright, from the very first scan, never has an
// accepted drift_adopt for that instance at all. So a drift_revert only
// "belongs to" (and is folded into the resolution of) an immediately
// preceding drift_adopt for the same address; one that stands alone
// (nothing, or a non-drift_adopt touch, immediately before it) is its
// own independent surfaced-and-resolved-by-reverting event. Missing this
// case would silently undercount every drift a team resolved by
// reverting outright -- a real gap found by re-reading the real
// generator code rather than assuming "adopt then later revert" was the
// only real-world shape.
//
// A destroyed address counts in history (ByKind/TotalProposals tally
// every proposal unconditionally) but is excluded from "open"
// specifically: an open-ended drift_adopt (nothing else drift-related
// ever touched that address again) whose address was later SHIPPED-
// destroyed is neither genuinely open (there's nothing left to act on)
// nor meaningfully resolved -- it's moot, and contributes to neither
// bucket, nor to DriftEvents' own total. A shipped destroy is checked
// the same way FoldState's own tombstone fold does (shippedDestroyFold,
// gated on the resource's own most recent transition actually reaching
// applied), never assumed from Delta.Destroys' own presence alone.
func Stats(l *Ledger, opts StatsOptions) (*StatsResult, error) {
	chain, err := l.Chain()
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}

	result := &StatsResult{
		ByKind:           map[ProposalKind]int{},
		AcceptanceMethod: map[string]int{},
		DriftByStack:     map[string]int{},
	}

	type driftTouch struct {
		p          *Proposal
		chainIndex int
	}
	driftTouchesByAddr := map[Address][]driftTouch{}
	destroyedAtByAddr := map[Address][]int{} // chain indices of SHIPPED destroys, per address
	var timeToDecisionSum time.Duration

	for i, p := range chain {
		if opts.Stack != "" && p.Stack != opts.Stack {
			continue
		}
		if p.Acceptance == nil {
			// Every ledger entry is accepted by construction (Append
			// requires an id, which only the accept path assigns) --
			// this should never happen, but Stats never crashes over an
			// unexpected shape, matching every other command in this
			// arc's own posture.
			continue
		}
		if opts.Since != nil || opts.Until != nil {
			at, perr := time.Parse(time.RFC3339, p.Acceptance.AcceptedAt)
			if perr != nil {
				// Can't confirm window membership -- excluded rather
				// than guessed into or out of the window.
				continue
			}
			if opts.Since != nil && at.Before(*opts.Since) {
				continue
			}
			if opts.Until != nil && at.After(*opts.Until) {
				continue
			}
		}

		result.TotalProposals++
		result.ByKind[p.Kind]++
		result.AcceptanceMethod[p.Acceptance.Method]++

		// Destroyed addresses count in history (the tally above already
		// covers that unconditionally) but are excluded from "open" --
		// tracked regardless of kind, since a destroy is its own Delta
		// element, never itself drift_adopt/drift_revert.
		for j := range p.Delta.Destroys {
			addr := p.Delta.Destroys[j].Address
			_, shipped, ferr := l.shippedDestroyFold(p.ID, addr)
			if ferr != nil {
				return nil, fmt.Errorf("stats: %s: %w", addr, ferr)
			}
			if shipped {
				destroyedAtByAddr[addr] = append(destroyedAtByAddr[addr], i)
			}
		}

		if p.Kind != KindDriftAdopt && p.Kind != KindDriftRevert {
			continue
		}
		result.DriftByStack[p.Stack]++

		if dur, ok := decisionLatency(p); ok {
			timeToDecisionSum += dur
			result.TimeToDecisionSamples++
		}

		if p.Kind == KindDriftAdopt {
			result.TotalDriftAdoptsForAttribution++
			if hasRealAttribution(p) {
				result.AttributedDriftAdopts++
			}
		}

		for _, addr := range driftTargets(p) {
			driftTouchesByAddr[addr] = append(driftTouchesByAddr[addr], driftTouch{p: p, chainIndex: i})
		}
	}

	if result.TimeToDecisionSamples > 0 {
		result.TimeToDecision = timeToDecisionSum / time.Duration(result.TimeToDecisionSamples)
	}

	for addr, touches := range driftTouchesByAddr {
		for i, t := range touches {
			switch t.p.Kind {
			case KindDriftAdopt:
				switch {
				case i+1 < len(touches) && touches[i+1].p.Kind == KindDriftRevert:
					result.DriftEvents++
					result.DriftResolvedReverted++
				case i+1 < len(touches):
					result.DriftEvents++
					result.DriftResolvedAdopted++
				case destroyedAfter(destroyedAtByAddr[addr], t.chainIndex):
					// The address was destroyed after this drift_adopt,
					// with no later drift-related touch in between --
					// counted in history (ByKind/TotalProposals above
					// already did that unconditionally) but excluded
					// from "open": there's nothing left to act on, the
					// resource doesn't exist anymore, so it's moot
					// rather than either genuinely open or resolved.
				default:
					result.DriftEvents++
					result.DriftOpen++
				}
			case KindDriftRevert:
				// A revert immediately following a drift_adopt resolves
				// THAT adopt event -- already tallied above, via the
				// adopt's own "next touch" check. A revert is only its
				// own standalone surfaced-and-resolved event when it
				// does NOT directly follow one -- the real, common case
				// (`ubx scan --propose both` generates drift_adopt and
				// drift_revert as ALTERNATIVE responses to the SAME
				// detected drift, sharing one parent; only one is ever
				// accepted, so a team choosing "revert" from the start
				// never has an accepted drift_adopt for that instance at
				// all -- confirmed by reading core/scan.go's own
				// GenerateRevertProposal and docs/architecture.md's own
				// "Revert path" section before writing this, not
				// assumed). Missing this case would silently undercount
				// every drift a team resolved by reverting outright.
				if i == 0 || touches[i-1].p.Kind != KindDriftAdopt {
					result.DriftEvents++
					result.DriftResolvedReverted++
				}
			}
		}
	}

	return result, nil
}

// destroyedAfter reports whether any of destroyIndices (an address's own
// SHIPPED destroy chain-positions) falls after chainIndex.
func destroyedAfter(destroyIndices []int, chainIndex int) bool {
	for _, idx := range destroyIndices {
		if idx > chainIndex {
			return true
		}
	}
	return false
}

// driftTargets returns every address a drift_adopt/drift_revert proposal
// touches -- today's own generators (core/scan.go) only ever produce one
// per proposal, but this reads Delta.Modifies in full rather than
// assuming index 0, defensively.
func driftTargets(p *Proposal) []Address {
	addrs := make([]Address, 0, len(p.Delta.Modifies))
	for i := range p.Delta.Modifies {
		addrs = append(addrs, p.Delta.Modifies[i].Target)
	}
	return addrs
}

// decisionLatency is p's own Acceptance.AcceptedAt minus Resolution.
// ResolvedAt -- ok is false if either timestamp fails to parse as
// RFC3339, never a guessed/zero duration silently folded into an
// average.
func decisionLatency(p *Proposal) (time.Duration, bool) {
	resolvedAt, err := time.Parse(time.RFC3339, p.Resolution.ResolvedAt)
	if err != nil {
		return 0, false
	}
	acceptedAt, err := time.Parse(time.RFC3339, p.Acceptance.AcceptedAt)
	if err != nil {
		return 0, false
	}
	return acceptedAt.Sub(resolvedAt), true
}

// hasRealAttribution reports whether p's own intent.sources names at
// least one real cloudtrail/gcp_audit actor (as opposed to an
// unattributed placeholder -- cloudtrail_unattributed/
// gcp_audit_unattributed -- or no attribution source at all).
func hasRealAttribution(p *Proposal) bool {
	for _, s := range p.Intent.Sources {
		if (s.Kind == "cloudtrail" || s.Kind == "gcp_audit") && s.ActorARN != "" {
			return true
		}
	}
	return false
}
