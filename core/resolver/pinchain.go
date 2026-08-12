package resolver

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/core"
)

// PinHop is one link in a walked cross-stack pin chain -- WalkPinChain's
// own return shape.
type PinHop struct {
	// Resource is the address, in the CHAIN'S OWN current stack, whose
	// resolution recorded this pin -- p's own address for the first hop,
	// the previous hop's own neighbor proposal's address for every hop
	// after that.
	Resource string
	// LedgerDir is the neighbor's own resolved address (a git-directory
	// path or a store URI alike, exactly as core.ResolutionInput.LedgerDir
	// already carries it).
	LedgerDir string
	// PinnedHead is the neighbor's head this hop's own proposal recorded
	// at ITS resolve time.
	PinnedHead string
	// CurrentHead is the neighbor's real, current head, fetched fresh
	// when the walk ran.
	CurrentHead string
	// Stale is CurrentHead != PinnedHead -- this hop's OWN direct
	// staleness, independent of any other hop in the chain (see
	// WalkPinChain's own doc comment for why this never cascades into a
	// blocking verdict).
	Stale bool
}

// WalkPinChain walks every cross-stack pin recorded in p, then recurses
// into each neighbor's own proposal AT ITS PINNED HEAD to discover
// whether THAT proposal itself records any further cross-stack pins,
// continuing until a hop has none -- UBI-57 Part 2's own real,
// informational multi-hop trace (stack A pins B, which itself pins C).
//
// Deliberately NEVER used to decide whether p itself may ship --
// VerifyPins' own blocking staleness check stays exactly as it already
// was, per-pair only: checked directly before this was built (not
// assumed) that the actual current code only ever reads p's own
// resolution.inputs, never a neighbor's, and that no design doc anywhere
// in this codebase claims staleness should cascade -- docs/
// resolver-adversarial.md's own honest framing names this as an
// acknowledged, not-yet-covered question, never a settled "it
// cascades" claim. Founder confirmed, in-conversation, this should stay
// visibility-only: WalkPinChain exists purely to render the full picture
// to a human (ubx why/ubx addresses) -- an indirect ancestor having
// moved is real, useful information, never itself a reason to refuse a
// ship.
//
// A cycle guard (seen, keyed by LedgerDir+PinnedHead) stops a walk that
// would otherwise loop forever against a real, if malformed,
// pin-back-to-itself configuration -- returns the chain walked so far
// rather than erroring, since a cycle is a real, renderable finding in
// its own right (docs/sdk.md's own row 5 "never a silent best-effort"
// posture still holds: the cycle itself is worth surfacing, just not by
// crashing the walk).
func WalkPinChain(ctx context.Context, p *core.Proposal) ([]PinHop, error) {
	var hops []PinHop
	seen := map[string]bool{}
	var walk func(pp *core.Proposal) error
	walk = func(pp *core.Proposal) error {
		for _, in := range pp.Resolution.Inputs {
			if in.Kind != "cross_stack_pin" {
				continue
			}
			key := in.LedgerDir + "@" + in.PinnedHead
			if seen[key] {
				continue
			}
			seen[key] = true

			neighbor, closeNeighbor, err := core.OpenRef(ctx, in.LedgerDir)
			if err != nil {
				return fmt.Errorf("walk pin chain for %s: %w", in.Resource, err)
			}
			currentHead, headErr := neighbor.Head()
			if headErr != nil {
				closeNeighbor()
				return fmt.Errorf("walk pin chain for %s: %w", in.Resource, headErr)
			}
			hops = append(hops, PinHop{
				Resource:    in.Resource,
				LedgerDir:   in.LedgerDir,
				PinnedHead:  in.PinnedHead,
				CurrentHead: currentHead,
				Stale:       currentHead != in.PinnedHead,
			})

			// Recurse into the neighbor's OWN proposal at the pinned head
			// -- what B itself had pinned at the exact moment A recorded
			// this hop. A pinned head that no longer resolves to a real
			// proposal is a genuinely different, real finding (this
			// project's own append-only posture means a real head, once
			// recorded, should always still be readable) -- surfaced as a
			// real error, never silently swallowed into an empty chain.
			pinnedProp, readErr := neighbor.Read(in.PinnedHead)
			closeNeighbor()
			if readErr != nil {
				return fmt.Errorf("walk pin chain for %s: read pinned proposal %s at %s: %w", in.Resource, in.PinnedHead, in.LedgerDir, readErr)
			}
			if err := walk(pinnedProp); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(p); err != nil {
		return nil, err
	}
	return hops, nil
}
