// Package executor implements docs/executor.md's failure-state machine:
// ubx ship's v1 scope, shipping accepted drift_revert proposals. This
// package is hermetic -- it depends only on core and its own Applier
// interface, never on package provider directly. Provider wiring (a
// concrete Applier backed by a real tfplugin client) is explicit
// next-slice work (docs/plan.md's "Executor v1" wedge); this package is
// exercised here against a hermetic fake with scriptable failures, per
// docs/executor-adversarial.md's required-outcome program.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Applier is core/executor's own minimal view of "something that can read
// a resource's live state and apply a change to it." ReadResource (via the
// embedded core.StateReader) is reused unchanged for both freshness
// verification (core.VerifyFreshness) and this package's own
// reconcile-by-query step; ApplyResourceChange is the one new capability
// the executor needs beyond what core.StateReader already defines for
// scanning.
type Applier interface {
	core.StateReader

	// ApplyResourceChange asks the provider to move resourceType's state
	// from priorState to plannedState, returning the provider's post-apply
	// attributes. result is unredacted -- callers wiring a real provider
	// (session 3) are responsible for running it through provider.Redact
	// before it's written anywhere persistent (docs/schema.md's apply-record
	// amendment); this package never inspects or redacts it itself, the
	// same core/provider zero-import boundary UBI-23 established.
	//
	// An error satisfying errors.As(err, *TerminalError) is never retried
	// within one ship invocation (docs/executor.md's "terminal"
	// classification: the provider gave a real, structured answer). Any
	// other error is treated as "retryable" and triggers reconcile-by-query
	// rather than an immediate failure.
	ApplyResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, plannedState json.RawMessage) (result json.RawMessage, err error)
}

// TerminalError wraps a provider error that must not be retried within a
// single ship invocation -- docs/executor.md's "terminal" classification: a
// real, structured diagnostic (e.g. Severity ERROR) from
// ApplyResourceChange, as opposed to a transient or ambiguous failure. A
// concrete Applier wiring a real provider.Client (next slice) constructs
// this from an ERROR-severity Diagnostic; this package's own hermetic fake
// provider constructs it directly to simulate
// docs/executor-adversarial.md's row 6b.
type TerminalError struct{ Err error }

func (e *TerminalError) Error() string { return e.Err.Error() }
func (e *TerminalError) Unwrap() error { return e.Err }

var (
	// ErrUnsupportedKind means Ship was asked to execute a proposal kind
	// other than drift_revert -- v1's only supported kind (docs/executor.md
	// -- Scope).
	ErrUnsupportedKind = errors.New("ship: only drift_revert proposals can be shipped")

	// ErrNotAccepted means Ship was asked to execute a proposal that was
	// never accepted (no Acceptance recorded).
	ErrNotAccepted = errors.New("ship: proposal has not been accepted")

	// ErrAlreadyApplied means every resource in the proposal already
	// reached "applied" in a prior attempt -- the idempotency contract's
	// simplest case (docs/executor.md): Ship is a genuine no-op, and writes
	// no new apply record at all.
	ErrAlreadyApplied = errors.New("ship: proposal is already fully applied")
)

// Retry/reconciliation budgets -- package vars, not constants, so tests can
// shrink them (same convention as core's lockWaitTimeout/lockRetryInterval).
var (
	maxReconcileAttempts        = 5
	reconcileRetryInterval      = 20 * time.Millisecond
	maxApplyAttemptsPerResource = 3
)

// Ship executes an accepted drift_revert proposal's delta.modifies, one
// resource at a time in canonical (stack, type, name) order (docs/executor.md
// -- "Serial execution in delta order"), producing and sealing exactly one
// new core.ApplyRecord, chained to this proposal's own prior attempts (if
// any). See docs/executor.md for the full state machine this implements.
func Ship(ctx context.Context, l *core.Ledger, app Applier, providerSource string, providerConfig json.RawMessage, p *core.Proposal) (*core.ApplyRecord, error) {
	if p.Kind != core.KindDriftRevert {
		return nil, fmt.Errorf("%w: got %s", ErrUnsupportedKind, p.Kind)
	}
	if p.Acceptance == nil {
		return nil, ErrNotAccepted
	}

	modifies := sortedModifies(p.Delta.Modifies)

	attempts, err := l.ApplyAttempts(p.ID)
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}

	if allAlreadyApplied(attempts, modifies) {
		return nil, ErrAlreadyApplied
	}

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}

	startedAt := nowRFC3339()
	var resourcesApplied, resourcesFailed, resourcesStillUnknown int64
	halted := false

	persist := func() error {
		if err := l.SaveApplyProgress(rec); err != nil {
			return fmt.Errorf("ship: %w", err)
		}
		return nil
	}

	for _, m := range modifies {
		ra := &core.ResourceApply{Address: m.Target}
		rec.Resources = append(rec.Resources, ra)
		hist := foldResourceHistory(attempts, m.Target)

		if hist.hasState && hist.lastState == core.ResourceApplied {
			recordTransition(ra, core.ResourceApplied, "already applied in a prior attempt")
			resourcesApplied++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if halted {
			recordTransition(ra, core.ResourcePending, "")
			recordError(ra, "a prior resource in this attempt failed freshness verification -- refusing remaining resources rather than proceeding past it", core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		lookup, haveLookup := lookupFor(p, m.Target)

		// Reconcile first if the last thing we know about this resource is
		// unresolved (in_flight/unknown_post_timeout/still_unknown from a
		// prior, possibly-crashed attempt) -- docs/executor.md's idempotency
		// contract: never a blind re-apply on top of an unresolved unknown.
		if hist.hasState && needsReconciliation(hist.lastState) {
			recordTransition(ra, core.ResourcePending, "")
			if !haveLookup {
				recordError(ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
				resourcesFailed++
				if err := persist(); err != nil {
					return nil, err
				}
				continue
			}
			outcome := reconcileLoop(ctx, app, m.Target, lookup, m, providerSource, providerConfig, ra)
			tallyOutcome(outcome, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		recordTransition(ra, core.ResourcePending, "")

		if hist.attemptsInFlight >= maxApplyAttemptsPerResource {
			recordError(ra, fmt.Sprintf("retry budget exhausted after %d attempt(s)", hist.attemptsInFlight), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if err := core.VerifyFreshness(ctx, app, m.Target, providerSource, providerConfig, p); err != nil {
			if errors.Is(err, core.ErrStaleObservation) {
				recordError(ra, err.Error(), core.ErrorTerminal)
				halted = true
			} else {
				recordError(ra, err.Error(), core.ErrorRetryable)
			}
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if !haveLookup {
			recordError(ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		observed, _, err := core.ReadAndFingerprint(ctx, app, m.Target, providerSource, providerConfig, lookup)
		if err != nil {
			recordError(ra, fmt.Sprintf("read prior state: %v", err), core.ErrorRetryable)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}
		planned, err := core.ApplyAfter(observed, m)
		if err != nil {
			recordError(ra, fmt.Sprintf("construct planned state: %v", err), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}
		_, resourceSchemas, err := app.Schema(ctx)
		if err != nil {
			recordError(ra, fmt.Sprintf("fetch schema: %v", err), core.ErrorRetryable)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// THE invariant (docs/executor.md): in_flight is durably persisted
		// before the risky ApplyResourceChange call, never after.
		recordTransition(ra, core.ResourceInFlight, "")
		if err := persist(); err != nil {
			return nil, fmt.Errorf("ship: persist in_flight: %w", err)
		}

		result, applyErr := app.ApplyResourceChange(ctx, resourceSchemas[m.Target.Type], m.Target.Type, observed, planned)

		if applyErr == nil {
			ra.ProviderResult = result
			recordTransition(ra, core.ResourceApplied, "")
			resourcesApplied++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		var terminal *TerminalError
		if errors.As(applyErr, &terminal) {
			recordError(ra, terminal.Error(), core.ErrorTerminal)
			recordTransition(ra, core.ResourceFailed, "")
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// Retryable/ambiguous: the RPC didn't resolve into a clear answer
		// (docs/executor.md -- ResourceUnknownPostTimeout, reality is asked,
		// not assumed).
		recordError(ra, applyErr.Error(), core.ErrorRetryable)
		recordTransition(ra, core.ResourceUnknownPostTimeout, "")
		if err := persist(); err != nil {
			return nil, err
		}
		outcome := reconcileLoop(ctx, app, m.Target, lookup, m, providerSource, providerConfig, ra)
		tallyOutcome(outcome, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
		if err := persist(); err != nil {
			return nil, err
		}
	}

	outcome := "applied"
	switch {
	case resourcesApplied == 0 && (resourcesFailed > 0 || resourcesStillUnknown > 0):
		outcome = "failed"
	case resourcesFailed > 0 || resourcesStillUnknown > 0 || halted:
		outcome = "partially_applied"
	}

	sealed, err := l.SealApply(rec, core.ApplySummary{
		Outcome:               outcome,
		StartedAt:             startedAt,
		FinishedAt:            nowRFC3339(),
		ResourcesApplied:      resourcesApplied,
		ResourcesFailed:       resourcesFailed,
		ResourcesStillUnknown: resourcesStillUnknown,
	})
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}
	return sealed, nil
}

// reconcileLoop repeatedly reads addr's live state (reconcile-by-query,
// docs/executor.md) and compares it against mod's before/after dot-paths,
// up to maxReconcileAttempts times: a match against every "after" value
// means the change landed (applied); a match against every "before" value
// means it never did (failed); anything else is inconclusive, retried
// after reconcileRetryInterval. Exhausting the budget without a conclusive
// answer resolves still_unknown. Every attempt -- conclusive or not -- is
// appended to ra.Reconciliation; the final transition is always recorded
// before returning.
func reconcileLoop(ctx context.Context, app Applier, addr core.Address, lookup json.RawMessage, mod core.Modification, providerSource string, providerConfig json.RawMessage, ra *core.ResourceApply) core.ResourceState {
	for attempt := 0; attempt < maxReconcileAttempts; attempt++ {
		observed, _, err := core.ReadAndFingerprint(ctx, app, addr, providerSource, providerConfig, lookup)
		at := nowRFC3339()
		if err != nil {
			ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "inconclusive", Detail: err.Error()})
			time.Sleep(reconcileRetryInterval)
			continue
		}

		verdict, err := reconciliationVerdict(observed, mod)
		if err != nil {
			ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "inconclusive", Detail: err.Error()})
			time.Sleep(reconcileRetryInterval)
			continue
		}
		ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: verdict})

		switch verdict {
		case "applied":
			recordTransition(ra, core.ResourceApplied, "confirmed by reconciliation")
			return core.ResourceApplied
		case "failed":
			recordTransition(ra, core.ResourceFailed, "confirmed by reconciliation: change never landed")
			return core.ResourceFailed
		}
		time.Sleep(reconcileRetryInterval)
	}
	recordTransition(ra, core.ResourceStillUnknown, "reconciliation exhausted its retry budget without a conclusive answer")
	return core.ResourceStillUnknown
}

// reconciliationVerdict compares a freshly observed resource state against
// a Modification's recorded before/after dot-paths: "applied" if every
// "after" path matches, "failed" if every "before" path matches instead,
// "inconclusive" otherwise (docs/executor.md's reconcile-by-query rule).
func reconciliationVerdict(observed json.RawMessage, mod core.Modification) (string, error) {
	var state map[string]interface{}
	if err := json.Unmarshal(observed, &state); err != nil {
		return "", fmt.Errorf("decode observed state: %w", err)
	}

	if matchesAll(state, mod.After) {
		return "applied", nil
	}
	if matchesAll(state, mod.Before) {
		return "failed", nil
	}
	return "inconclusive", nil
}

func matchesAll(state map[string]interface{}, want map[string]json.RawMessage) bool {
	if len(want) == 0 {
		return false
	}
	for path, raw := range want {
		var wantVal interface{}
		if err := json.Unmarshal(raw, &wantVal); err != nil {
			return false
		}
		gotVal, ok := dotGet(state, path)
		if !ok || !reflect.DeepEqual(gotVal, wantVal) {
			return false
		}
	}
	return true
}

// dotGet reads the value at a dot-notation path from a decoded JSON state
// map -- the read-side mirror of core's own dotSet convention
// (docs/schema.md: Modification.Before/After are dot-notation keyed). ok
// is false if any segment of the path is missing or not an object.
func dotGet(m map[string]interface{}, path string) (v interface{}, ok bool) {
	parts := strings.Split(path, ".")
	cur := interface{}(m)
	for _, part := range parts {
		mm, isMap := cur.(map[string]interface{})
		if !isMap {
			return nil, false
		}
		v, ok = mm[part]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return v, true
}

// resourceHistory is what Ship already knows about one resource address
// from every apply attempt recorded so far, sealed or not.
type resourceHistory struct {
	lastState        core.ResourceState
	hasState         bool
	attemptsInFlight int // number of historical attempts that actually issued an ApplyResourceChange call for this address
}

func foldResourceHistory(attempts []*core.ApplyRecord, addr core.Address) resourceHistory {
	var h resourceHistory
	target := addr.String()
	for _, a := range attempts {
		for _, ra := range a.Resources {
			if ra.Address.String() != target {
				continue
			}
			if st, ok := ra.LastState(); ok {
				h.lastState = st
				h.hasState = true
			}
			for _, t := range ra.Transitions {
				if t.State == core.ResourceInFlight {
					h.attemptsInFlight++
					break // count once per attempt, regardless of how many transitions it recorded
				}
			}
		}
	}
	return h
}

func allAlreadyApplied(attempts []*core.ApplyRecord, modifies []core.Modification) bool {
	for _, m := range modifies {
		h := foldResourceHistory(attempts, m.Target)
		if !(h.hasState && h.lastState == core.ResourceApplied) {
			return false
		}
	}
	return true
}

func needsReconciliation(s core.ResourceState) bool {
	switch s {
	case core.ResourceInFlight, core.ResourceUnknownPostTimeout, core.ResourceStillUnknown:
		return true
	default:
		return false
	}
}

// sortedModifies returns a copy of mods sorted by (stack, type, name) --
// the same canonical order docs/schema.md's ratified hashing rules already
// define for delta.modifies. This is a real, independent sort, not an
// assumption about stored array order: core.canonicalProposalBytes only
// sorts a transient decoded copy when computing a proposal's hash, never
// mutating the Proposal struct's own field -- see docs/executor.md's
// "Serial execution in delta order" for the full reasoning.
func sortedModifies(mods []core.Modification) []core.Modification {
	sorted := make([]core.Modification, len(mods))
	copy(sorted, mods)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Target, sorted[j].Target
		if a.Stack != b.Stack {
			return a.Stack < b.Stack
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Name < b.Name
	})
	return sorted
}

func lookupFor(p *core.Proposal, addr core.Address) (json.RawMessage, bool) {
	target := addr.String()
	for _, in := range p.Resolution.Inputs {
		if in.Resource == target {
			return in.Lookup, len(in.Lookup) > 0
		}
	}
	return nil, false
}

func recordTransition(ra *core.ResourceApply, state core.ResourceState, detail string) {
	ra.Transitions = append(ra.Transitions, core.Transition{State: state, At: nowRFC3339(), Detail: detail})
}

func recordError(ra *core.ResourceApply, message string, class core.ErrorClassification) {
	ra.Errors = append(ra.Errors, core.ErrorRecord{At: nowRFC3339(), Message: message, Classification: class})
}

func tallyOutcome(state core.ResourceState, applied, failed, stillUnknown *int64) {
	switch state {
	case core.ResourceApplied:
		*applied++
	case core.ResourceFailed:
		*failed++
	case core.ResourceStillUnknown:
		*stillUnknown++
	}
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
