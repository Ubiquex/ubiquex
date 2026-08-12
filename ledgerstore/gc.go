package ledgerstore

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

// OrphanedProposals returns every proposal object's own ID that exists in
// the store (a real proposals/<id>.prop.json object) but is NOT reachable
// from the current head chain -- the real, harmless-but-accumulating
// leftover a crash between WriteProposalIfAbsent and AdvanceHead leaves
// behind (core/ledger.go's own Append: the proposal object lands, the
// head never advances past it, and unless the exact same proposal is
// later retried -- core/ledger.go's own "interrupted append" resume,
// which completes AdvanceHead for a matching ID -- the object stays
// permanently orphaned). UBI-57.
//
// The reachability walk deliberately starts from GENESIS, never from
// Head()'s own best-effort cached hint: a stale or wrong hint here would
// silently truncate the reachable set, making a genuinely-chained early
// proposal look orphaned -- the one mistake this function must never
// make. Head()'s own hint-shortcut exists purely as a read-path cost
// optimization; nothing about correctness may ever depend on it, and
// this function doesn't touch it at all.
//
// Only proposals/ is ever considered -- heads/ (the edges themselves),
// head-hint, lock, and salt are never candidates, and this function
// never even lists them: heads/ edges ARE the reachability signal, not a
// deletion candidate; head-hint is a re-derivable cache; lock guards
// in-flight operations; salt is permanent redaction state. applies/ is
// also out of scope -- an apply record only ever exists for a proposal
// that was already validly chained at the time it was written, so it
// can't independently orphan the way a proposal can (a real, deliberate
// scope line, not an oversight).
func (s *Store) OrphanedProposals(ctx context.Context) ([]string, error) {
	reachable, err := s.reachableProposalIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("ledgerstore: walk reachable chain: %w", err)
	}
	reachableSet := make(map[string]bool, len(reachable))
	for _, id := range reachable {
		reachableSet[id] = true
	}

	iter := s.bucket.List(&blob.ListOptions{Prefix: proposalsPrefix})
	var orphans []string
	for {
		obj, err := iter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ledgerstore: list proposals: %w", err)
		}
		id, ok := proposalIDFromKey(obj.Key)
		if !ok {
			// Listed under proposalsPrefix but doesn't match the real
			// proposal-key shape -- never guess this is safe to delete;
			// skip it and let a human look, the same "never a silent
			// best-effort" posture docs/sdk.md's own row 5 already holds
			// the rest of this codebase to.
			continue
		}
		if !reachableSet[id] {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	return orphans, nil
}

// reachableProposalIDs walks every heads/ edge from genesis forward,
// collecting each proposal ID visited along the way -- the complete
// reachable set for this store's own single chain (Store is always
// already scoped to exactly one stack, see Store's own doc comment).
func (s *Store) reachableProposalIDs(ctx context.Context) ([]string, error) {
	var ids []string
	current := ""
	for {
		next, found, err := s.readEdge(ctx, current)
		if err != nil {
			return nil, err
		}
		if !found {
			return ids, nil
		}
		ids = append(ids, next)
		current = next
	}
}

// proposalIDFromKey extracts id from a real proposals/<id>.prop.json
// object key, or reports ok=false if key doesn't match that exact shape.
func proposalIDFromKey(key string) (id string, ok bool) {
	rest, ok := strings.CutPrefix(key, proposalsPrefix)
	if !ok {
		return "", false
	}
	id, ok = strings.CutSuffix(rest, ".prop.json")
	return id, ok
}

// DeleteProposal permanently removes one orphaned proposal object.
// Callers are responsible for having already confirmed id is genuinely
// unreachable (via OrphanedProposals) -- this method does not itself
// re-derive reachability, keeping the actual destructive operation
// simple and fast. `ubx store gc`'s own CLI flow re-runs
// OrphanedProposals immediately before deleting anything, and only
// deletes ids that appear in BOTH the originally-displayed dry-run list
// AND that fresh, immediately-prior recomputation -- never trusting a
// single, possibly-stale snapshot for a destructive operation, and never
// deleting something the operator was never shown.
//
// A NotFound delete is not an error -- another concurrent gc run (or a
// completed interrupted-append resume that has since re-chained this
// exact id, the one case an "orphan" can stop being one) may have
// already resolved it; DeleteProposal's own job is "make sure this
// specific id is gone," which is already true.
func (s *Store) DeleteProposal(ctx context.Context, id string) error {
	err := s.bucket.Delete(ctx, proposalKey(id))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil
	}
	return err
}
