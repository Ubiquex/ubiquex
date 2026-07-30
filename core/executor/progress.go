package executor

import "context"

// ProgressEvent is one observable moment during a Ship run (UBI-61's own
// "progress narration" scope, docs/cli-output-spec.md's "the read-back
// verification line is mandatory"): a resource transition (pending ->
// in_flight -> applied/failed/...) or one iteration of a reconcile-by-
// query retry loop. Address is "" for nothing today (every event is
// resource-scoped) but left as a real field rather than assumed, so a
// future proposal-level event (e.g. "resolving dependency order") has
// somewhere to go without a breaking change.
type ProgressEvent struct {
	Address string
	Kind    string // "transition" | "reconcile_attempt"
	State   string // ResourceState string, only for Kind == "transition"
	Detail  string
	Attempt int // 1-based, only for Kind == "reconcile_attempt"
	Total   int // only for Kind == "reconcile_attempt"
}

// progressKey is an unexported context key type -- the standard Go
// idiom for a value nothing outside this package should ever be able to
// collide with or read directly.
type progressKey struct{}

// WithProgress attaches fn as ctx's own progress sink for the ship run
// this ctx flows through. A deliberate context-value design, not a new
// parameter on Ship/shipChange/shipDriftRevert/shipDestroyNode: every one
// of those already takes ctx, so this adds real per-resource/per-attempt
// observability with ZERO signature changes to any of them -- the
// mechanical cost lands entirely on recordTransition/recordError (which
// already run at every transition point) and the two reconcile loops
// (which already own their own attempt-counting loop), not on the
// call-graph shape itself. A caller that never calls WithProgress gets
// byte-identical behavior to before this existed -- every hermetic test
// in this package (which never does) is unaffected.
func WithProgress(ctx context.Context, fn func(ProgressEvent)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// emitProgress is a no-op whenever ctx carries no progress sink (the
// overwhelmingly common case: every existing caller, every existing
// test) -- real emission only happens for a ctx a caller explicitly
// built via WithProgress.
func emitProgress(ctx context.Context, ev ProgressEvent) {
	if fn, ok := ctx.Value(progressKey{}).(func(ProgressEvent)); ok && fn != nil {
		fn(ev)
	}
}
