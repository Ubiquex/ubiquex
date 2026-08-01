package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ubiquex/ubiquex/core"
)

// recorder is the single point of contact for every piece of state a
// shipChange walk shares across node goroutines (UBI-67 session 2) --
// rec.Resources, resultsByAddr, and the three outcome counters. Every
// method takes recorder's own mutex for its whole body, so two nodes'
// own recordTransition/recordError/persist calls (or any direct
// ResourceApply field write) can never interleave with each other, nor
// with a persist() call's own json.Marshal walking every node's
// *ResourceApply -- session 1's own empirically-proven finding (5 of 8
// concurrently-appended resource entries silently lost, plus repeated
// race-detector failures on rec.Resources and on a sibling ra's own
// fields read mid-mutation) is what this exists to make structurally
// impossible, not merely less likely.
//
// rec.Resources itself is never appended to during a concurrent walk --
// shipChange pre-allocates one *core.ResourceApply per node, in
// changeNodesOf's own topo order, before any goroutine starts (see
// shipChange's own doc comment) -- so the slice's own length/order is
// fixed and race-free by construction; only the ResourceApply values it
// points at, resultsByAddr, and the counters are mutated during the
// walk, and only ever through this type.
//
// shipDriftRevert (still fully serial -- drift_revert proposals are out
// of this ticket's scope, docs/executor.md's own "Serial execution in
// delta order" section) constructs one too, rather than a second,
// lock-free code path existing for the identical operations: an
// uncontended mutex lock/unlock is negligible overhead, and one
// mechanism instead of two that could quietly drift apart is worth that
// cost.
type recorder struct {
	mu  sync.Mutex
	l   *core.Ledger
	rec *core.ApplyRecord

	resultsByAddr map[string]json.RawMessage

	resourcesApplied      int64
	resourcesFailed       int64
	resourcesStillUnknown int64
}

// newRecorder builds a recorder over rec, seeded with seed (a caller-
// owned map -- copied, never aliased, so a caller's own subsequent
// mutation of its own seed map after this call can never race with the
// recorder's internal one).
func newRecorder(l *core.Ledger, rec *core.ApplyRecord, seed map[string]json.RawMessage) *recorder {
	m := make(map[string]json.RawMessage, len(seed))
	for k, v := range seed {
		m[k] = v
	}
	return &recorder{l: l, rec: rec, resultsByAddr: m}
}

// persist durably saves the current record -- the exact
// l.SaveApplyProgress(rec) call every ship*Node function already made
// directly before this session; now the one and only place that ever
// does, so a concurrent call from a sibling node's own goroutine can
// never overlap with it.
func (r *recorder) persist() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.l.SaveApplyProgress(r.rec); err != nil {
		return fmt.Errorf("ship: %w", err)
	}
	return nil
}

// transition mutates ra's own Transitions (recordTransition's existing
// behavior, unchanged) under the shared lock -- ra is exclusive to one
// node's own goroutine, but a DIFFERENT goroutine's concurrent persist()
// call walks every node's ra transitively via json.Marshal(rec), so this
// write must never happen unsynchronized against that read.
func (r *recorder) transition(ctx context.Context, ra *core.ResourceApply, state core.ResourceState, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recordTransition(ctx, ra, state, detail)
}

// recordErr mirrors transition, for recordError's own existing behavior.
// Named recordErr, not error, to avoid shadowing the builtin interface
// name at every call site.
func (r *recorder) recordErr(ctx context.Context, ra *core.ResourceApply, message string, class core.ErrorClassification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recordError(ctx, ra, message, class)
}

// mutate runs fn (an arbitrary direct write to ra's own fields --
// ProviderResult, Lookup, Reconciliation) under the shared lock. Every
// such write in shipCreate/shipModifyNode/shipDestroyNode/reconcileLoop/
// reconcileDestroyLoop goes through this now, for the identical reason
// transition/recordErr do: a concurrent persist() elsewhere reads these
// same fields.
func (r *recorder) mutate(ra *core.ResourceApply, fn func(*core.ResourceApply)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(ra)
}

// tally increments the one outcome counter matching state -- the locked
// counterpart to tallyOutcome's own switch, called from the same call
// sites tallyOutcome already was.
func (r *recorder) tally(state core.ResourceState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch state {
	case core.ResourceApplied:
		r.resourcesApplied++
	case core.ResourceFailed:
		r.resourcesFailed++
	case core.ResourceStillUnknown:
		r.resourcesStillUnknown++
	}
}

// counts returns the current outcome tallies -- read once, after every
// node has reached a terminal state (the scheduler's own join point),
// mirroring exactly what shipChange's local resourcesApplied/
// resourcesFailed/resourcesStillUnknown variables held directly before
// this session.
func (r *recorder) counts() (applied, failed, stillUnknown int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resourcesApplied, r.resourcesFailed, r.resourcesStillUnknown
}

// setResult records addr's own applied ProviderResult -- the locked
// counterpart of shipChange's own former "resultsByAddr[key] =
// ra.ProviderResult" line, called from the same completion point.
func (r *recorder) setResult(addr string, result json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resultsByAddr[addr] = result
}

// hasResult reports whether addr has a recorded (i.e. applied) result --
// the locked counterpart of shipChange's own former "resultsByAddr[dep]"
// membership check.
func (r *recorder) hasResult(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.resultsByAddr[addr]
	return ok
}

// snapshotResults returns a fresh, private copy of every result
// recorded so far -- taken once per node, synchronously, at the moment
// the scheduler decides that node's own dependencies are all resolved
// (so the entries this node's own config substitution needs are already
// final and will never change again) and dispatches its goroutine. The
// node's own goroutine then reads this COPY for the rest of its run
// (substituteComputed/substituteModificationComputed), needing no
// further synchronization at all -- simpler, and just as correct, as
// threading a locked accessor through every existing call site those two
// functions already have.
func (r *recorder) snapshotResults() map[string]json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := make(map[string]json.RawMessage, len(r.resultsByAddr))
	for k, v := range r.resultsByAddr {
		m[k] = v
	}
	return m
}
