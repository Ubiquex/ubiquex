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
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core"
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
	// plannedPrivate is nil for every kind this codebase shipped before
	// UBI-30's own correction (create/modify's "no separate plan phase"
	// shortcut needs none, verified empirically, UBI-26/27) -- a destroy
	// is the one exception: shipDestroyNode always threads through the
	// real bytes a matching PlanResourceChange call produced. Found
	// empirically, not assumed (UBI-30's own live AWS finale): calling
	// ApplyResourceChange for a destroy with plannedPrivate left nil
	// silently no-ops against a real, complex SDKv2 provider -- it returns
	// success without actually invoking the resource's own Delete function
	// at all, since the provider's internal shim uses PlannedPrivate,
	// not just "PlannedState is null," to recognize a genuine destroy.
	//
	// An error satisfying errors.As(err, *TerminalError) is never retried
	// within one ship invocation (docs/executor.md's "terminal"
	// classification: the provider gave a real, structured answer). Any
	// other error is treated as "retryable" and triggers reconcile-by-query
	// rather than an immediate failure.
	//
	// lookup is UBI-63 session 2's own addition: the resource's own
	// lookup key (core.DeriveLookupFromResult's own {"id": ..., ...}
	// shape), computed by the implementation, which has a concrete
	// schema in scope here even though this interface's own
	// resourceSchema parameter stays intentionally opaque (any) -- the
	// same core/provider zero-import boundary result's own doc comment
	// already established. A create whose real schema has an "id" that
	// alone doesn't round-trip back into a working re-read (found live:
	// aws_iam_role_policy_attachment, whose own ReadResource needs
	// "role"/"policy_arn" present too) needs those extra fields captured
	// here, at the one place a concrete schema is actually available --
	// never re-derived later from an already-too-narrow stored value.
	// May be nil (a legitimate "nothing beyond the default derivation"
	// answer, or an implementation that hasn't been updated for this)
	// -- callers fall back to their own default derivation in that case.
	ApplyResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, plannedState json.RawMessage, plannedPrivate []byte) (result json.RawMessage, lookup json.RawMessage, err error)

	// PlanResourceChange asks the provider to compute a real plan moving
	// resourceType's state from priorState toward proposedNewState,
	// returning the provider's own planned state and the opaque
	// plannedPrivate bytes a matching ApplyResourceChange call must carry
	// (UBI-30's own correction to docs/executor.md's original design --
	// see ApplyResourceChange's own doc comment above for why this call is
	// unconditionally required before a destroy's own Apply, not an
	// optional refinement).
	PlanResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, proposedNewState json.RawMessage) (plannedState json.RawMessage, plannedPrivate []byte, err error)
}

// ApplierPool supplies an Applier for a given provider (source, version)
// pair, launching it lazily on first request and reusing it for every
// subsequent request with the identical pair, for the lifetime of one
// `ubx ship` invocation -- docs/executor.md's own "Amendment (2026-07-18,
// UBI-43): multi-provider stacks" client pool. This is the one thing
// shipChange's own combined topo-walk needed to become multi-provider
// capable (docs/executor.md's own finding, confirmed by reading the
// actual code: the walk itself never changes shape or order, only which
// client answers each step). core/executor never launches a provider
// itself -- the existing provider-import-free boundary Applier already
// establishes, unchanged -- a concrete implementation backed by
// provider.Acquire/provider.Launch lives in cli/, the one place that
// already bridges both packages for Applier itself.
//
// Get also returns config -- docs/executor.md's own session-3 addendum
// named this gap explicitly rather than leaving it silently unsolved: a
// single global providerConfig threaded through every node regardless of
// provider is correct only for a single-provider stack (AWS's own region
// config is never going to be the right thing to hand a Helm/Kubernetes
// provider's own Configure call). Each pool entry now carries its own
// resolved config alongside its own Applier -- shipChange's own per-node
// loop reads both together, the same single pool.Get call it already
// makes.
type ApplierPool interface {
	// Get returns the Applier for source@version and its own resolved
	// provider_config (docs/architecture.md §Multi-provider stacks' own
	// per-provider configuration -- session 4's amendment). An error here
	// means the provider could not be launched at all (bad credentials, an
	// unreachable registry, a crashed handshake) -- shipChange classifies
	// this as a per-node terminal failure for every node needing that
	// specific provider, never a whole-walk abort (docs/executor.md's own
	// "Provider launch failure mid-walk" section, docs/multi-provider-
	// adversarial.md row 4).
	Get(ctx context.Context, source, version string) (Applier, json.RawMessage, error)
}

// SingleApplierPool wraps one already-launched Applier and its own fixed
// config into the trivial, always-succeeds ApplierPool a single-provider
// stack needs -- exactly today's `--provider`/`--source`/`--provider-config`
// CLI flow (docs/resolver.md's own staged retirement plan: single-provider
// stays fully functional, unchanged, once real `providers` config wiring
// lands). Get ignores its own source/version arguments entirely and always
// returns (app, config) -- there is only one provider in this pool, so
// there is nothing to route between.
func SingleApplierPool(app Applier, config json.RawMessage) ApplierPool {
	return singleApplierPool{app: app, config: config}
}

type singleApplierPool struct {
	app    Applier
	config json.RawMessage
}

func (p singleApplierPool) Get(context.Context, string, string) (Applier, json.RawMessage, error) {
	return p.app, p.config, nil
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
	// other than drift_revert or change -- v1's only two shippable kinds
	// (docs/executor.md -- Scope, extended by the UBI-27 amendment).
	ErrUnsupportedKind = errors.New("ship: only drift_revert or change proposals can be shipped")

	// ErrNotAccepted means Ship was asked to execute a proposal that was
	// never accepted (no Acceptance recorded).
	ErrNotAccepted = errors.New("ship: proposal has not been accepted")

	// ErrAlreadyApplied means every resource in the proposal already
	// reached "applied" in a prior attempt -- the idempotency contract's
	// simplest case (docs/executor.md): Ship is a genuine no-op, and writes
	// no new apply record at all.
	ErrAlreadyApplied = errors.New("ship: proposal is already fully applied")

	// ErrDependencyCycle means a change proposal's own delta.creates/
	// .modifies depends_on edges form a cycle -- should already be
	// unreachable by the time a proposal is accepted (core/resolver's own
	// topoSort would have refused to resolve it), so this is ship.go's own
	// defense against a corrupt or hand-tampered proposal file, never a
	// real resolver output. Never silently broken or arbitrarily ordered.
	ErrDependencyCycle = errors.New("ship: circular dependency between resources in this proposal")

	// ErrDependencyNotApplied means a $computed marker names a dependency
	// address that hasn't reached applied yet -- should already be
	// unreachable given ship's own "never attempt a resource ahead of its
	// depends_on" precondition; a real occurrence means the resolved
	// config's own $computed targets don't match its depends_on edges (a
	// resolver bug or a tampered proposal), not something to guess past.
	ErrDependencyNotApplied = errors.New("ship: a $computed marker's dependency has not applied")

	// ErrMalformedComputedMarker means a $computed marker's "from" pointer
	// isn't a well-formed "<stack>.<type>.<name>.<path>" address.
	ErrMalformedComputedMarker = errors.New("ship: malformed $computed marker")

	// ErrDependencyOutputMissing means a $computed marker's "from" path
	// doesn't exist anywhere in the dependency's own real applied result --
	// the schema promised it, but the provider's actual response doesn't
	// have it.
	ErrDependencyOutputMissing = errors.New("ship: dependency's applied output has no value at the referenced path")
)

// Retry/reconciliation budgets -- package vars, not constants, so tests can
// shrink them (same convention as core's lockWaitTimeout/lockRetryInterval).
var (
	// maxReconcileAttempts/reconcileRetryInterval are create/modify's own
	// reconcileLoop budget, unchanged -- a different problem from destroy's
	// own (below): a modify/create reconciles against a specific attribute
	// value, not presence itself, and this session's own live finding
	// (UBI-44/UBI-42) was specifically about destroy's absence-confirmation
	// lag, never measured or claimed here.
	maxReconcileAttempts   = 5
	reconcileRetryInterval = 20 * time.Millisecond

	// destroyReconcileBackoffSchedule replaces a single fixed interval for
	// destroy's own reconcile-by-query (UBI-42, co-scoped with UBI-44's own
	// universal post-destroy read-back, below): a real cloud provider's own
	// deletion-visibility lag (SQS's own documented ~60 seconds, confirmed
	// live, UBI-30's finale) can easily outlast a short fixed budget, and
	// the universal read-back now pays this cost on *every* destroy, not
	// just the rare ambiguous ones a fixed 100ms budget used to gate --
	// paying a uniformly-long fixed interval on every attempt would be its
	// own regression. A backoff schedule keeps the common case (a
	// synchronously-consistent provider -- confirmed live for GCP Pub/Sub,
	// UBI-44) resolving on the very first read, at essentially zero added
	// cost, while still reaching a real cloud's own slow tail: ten steps
	// summing to ~63.75s, comfortably past AWS's own ~60-second figure.
	destroyReconcileBackoffSchedule = []time.Duration{
		50 * time.Millisecond,
		200 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		15 * time.Second,
		15 * time.Second,
		15 * time.Second,
	}
	maxApplyAttemptsPerResource = 3
)

// debugDelayAfterInFlight/debugDelayAfterApplySuccess, when non-zero, sleep
// for that long at two distinct points bracketing the one risky call this
// whole package exists to make safe: immediately after in_flight is
// durably persisted but before ApplyResourceChange is ever called (row 4,
// docs/executor-adversarial.md -- "never landed"), and immediately after
// ApplyResourceChange returns successfully but before the applied
// transition/result are persisted (row 5 -- "already landed"). These are
// deliberate verification seams, not production knobs -- same convention
// as this codebase's other UBX_*-prefixed test-only env vars
// (FAKEPROVIDER_MODE, UBX_CONFORMANCE_LIVE, UBX_GITHUB_API_BASE_URL): they
// make a real `kill -9` at either exact point reproducible on demand
// (UBI-26's live adversarial verification) rather than a matter of chance
// timing against a real network call. Both are zero (no delay -- the only
// behavior anyone running `ubx ship` for real ever sees) unless
// UBX_SHIP_DEBUG_DELAY_AFTER_INFLIGHT/UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS
// is explicitly set to a parseable Go duration (e.g. "3s").
var (
	debugDelayAfterInFlight     = parseDebugDelay("UBX_SHIP_DEBUG_DELAY_AFTER_INFLIGHT")
	debugDelayAfterApplySuccess = parseDebugDelay("UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS")

	// debugDelayBetweenChangeResources (UBI-27) sleeps in shipChange's own
	// loop, after one resource's own processing is fully durably persisted
	// (applied, failed, or blocked) and before the next resource in topo
	// order is even looked at -- a reliable, reproducible window for a real
	// `kill -9` to land exactly "between one resource applying for real and
	// the next one starting," the multi-resource counterpart to
	// debugDelayAfterInFlight/debugDelayAfterApplySuccess's own
	// single-resource windows. UBX_SHIP_DEBUG_DELAY_BETWEEN_RESOURCES,
	// same convention (zero/unset -- no delay -- in every real `ubx ship`).
	debugDelayBetweenChangeResources = parseDebugDelay("UBX_SHIP_DEBUG_DELAY_BETWEEN_RESOURCES")
)

func parseDebugDelay(envVar string) time.Duration {
	d, _ := time.ParseDuration(os.Getenv(envVar))
	return d
}

// Ship executes an accepted proposal -- drift_revert's delta.modifies, or
// (UBI-27, extended UBI-30) a change proposal's delta.creates,
// delta.modifies, and delta.destroys together -- dispatching to the
// shape-appropriate implementation. See docs/executor.md for the full
// state machine both share.
//
// pool is docs/executor.md's own "Amendment (2026-07-18, UBI-43):
// multi-provider stacks" client pool -- a change proposal's own combined
// walk (shipChange) routes each node to its own recorded provider (and
// that provider's own config, session 4's own amendment) via pool.Get;
// a drift_revert is single-provider by construction (it predates this
// whole concept, and core/scan.go never records a provider on its own
// Modification entries), so shipDriftRevert keeps taking one plain
// Applier and its own config, resolved from pool here, once, exactly as
// SingleApplierPool's own callers already expect. providerConfig no
// longer exists as a Ship-level parameter -- it comes exclusively from
// the pool now, per provider, never a single value assumed correct for
// every node.
func Ship(ctx context.Context, l *core.Ledger, pool ApplierPool, providerSource string, p *core.Proposal) (*core.ApplyRecord, error) {
	switch p.Kind {
	case core.KindDriftRevert:
		app, providerConfig, err := pool.Get(ctx, providerSource, "")
		if err != nil {
			return nil, fmt.Errorf("ship: %w", err)
		}
		return shipDriftRevert(ctx, l, app, providerSource, providerConfig, p)
	case core.KindChange:
		return shipChange(ctx, l, pool, providerSource, p)
	default:
		return nil, fmt.Errorf("%w: got %s", ErrUnsupportedKind, p.Kind)
	}
}

// shipDriftRevert executes an accepted drift_revert proposal's
// delta.modifies, one resource at a time in canonical (stack, type, name)
// order (docs/executor.md -- "Serial execution in delta order"), producing
// and sealing exactly one new core.ApplyRecord, chained to this proposal's
// own prior attempts (if any). This is Ship's own pre-UBI-27 body, renamed
// but otherwise unchanged -- see docs/executor.md for the full state
// machine this implements.
func shipDriftRevert(ctx context.Context, l *core.Ledger, app Applier, providerSource string, providerConfig json.RawMessage, p *core.Proposal) (*core.ApplyRecord, error) {
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
			recordTransition(ctx, ra, core.ResourceApplied, "already applied in a prior attempt")
			resourcesApplied++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if halted {
			recordTransition(ctx, ra, core.ResourcePending, "")
			recordError(ctx, ra, "a prior resource in this attempt failed freshness verification -- refusing remaining resources rather than proceeding past it", core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// docs/executor.md -- "Redacted after values are declined, not
		// applied": the ledger holds a salted hash, not the real secret
		// material, so ubx can never construct a live apply from it. This
		// is checked before any read/freshness/apply work at all, and
		// declines the whole resource (never a partial apply of just the
		// non-redacted paths) -- ApplyResourceChange is a single whole-state
		// operation, not an independent per-attribute one.
		if paths := redactedAfterPaths(m); len(paths) > 0 {
			recordTransition(ctx, ra, core.ResourcePending, "")
			recordError(ctx, ra, fmt.Sprintf(
				"declined: after value(s) at %s are redacted -- the ledger holds a salted hash, not the real secret material, and ubx will never construct a live apply from it; use `ubx revert-plan` for this resource's manual reconciliation steps instead",
				strings.Join(paths, ", ")), core.ErrorTerminal)
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
			recordTransition(ctx, ra, core.ResourcePending, "")
			if !haveLookup {
				recordError(ctx, ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
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

		recordTransition(ctx, ra, core.ResourcePending, "")

		if hist.attemptsInFlight >= maxApplyAttemptsPerResource {
			recordError(ctx, ra, fmt.Sprintf("retry budget exhausted after %d attempt(s)", hist.attemptsInFlight), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if err := core.VerifyFreshness(ctx, app, m.Target, providerSource, providerConfig, p); err != nil {
			if errors.Is(err, core.ErrStaleObservation) {
				recordError(ctx, ra, err.Error(), core.ErrorTerminal)
				halted = true
			} else {
				recordError(ctx, ra, err.Error(), core.ErrorRetryable)
			}
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		if !haveLookup {
			recordError(ctx, ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		observed, _, err := core.ReadAndFingerprint(ctx, app, m.Target, providerSource, providerConfig, lookup)
		if err != nil {
			recordError(ctx, ra, fmt.Sprintf("read prior state: %v", err), core.ErrorRetryable)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}
		planned, err := core.ApplyAfter(observed, m)
		if err != nil {
			recordError(ctx, ra, fmt.Sprintf("construct planned state: %v", err), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}
		_, resourceSchemas, err := app.Schema(ctx)
		if err != nil {
			recordError(ctx, ra, fmt.Sprintf("fetch schema: %v", err), core.ErrorRetryable)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// THE invariant (docs/executor.md): in_flight is durably persisted
		// before the risky ApplyResourceChange call, never after.
		recordTransition(ctx, ra, core.ResourceInFlight, "")
		if err := persist(); err != nil {
			return nil, fmt.Errorf("ship: persist in_flight: %w", err)
		}
		if debugDelayAfterInFlight > 0 {
			time.Sleep(debugDelayAfterInFlight)
		}

		result, _, applyErr := app.ApplyResourceChange(ctx, resourceSchemas[m.Target.Type], m.Target.Type, observed, planned, nil)

		if applyErr == nil {
			if debugDelayAfterApplySuccess > 0 {
				time.Sleep(debugDelayAfterApplySuccess)
			}
			ra.ProviderResult = result
			recordTransition(ctx, ra, core.ResourceApplied, "")
			resourcesApplied++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		var terminal *TerminalError
		if errors.As(applyErr, &terminal) {
			recordError(ctx, ra, terminal.Error(), core.ErrorTerminal)
			recordTransition(ctx, ra, core.ResourceFailed, "")
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// Retryable/ambiguous: the RPC didn't resolve into a clear answer
		// (docs/executor.md -- ResourceUnknownPostTimeout, reality is asked,
		// not assumed).
		recordError(ctx, ra, applyErr.Error(), core.ErrorRetryable)
		recordTransition(ctx, ra, core.ResourceUnknownPostTimeout, "")
		if err := persist(); err != nil {
			return nil, err
		}
		outcome := reconcileLoop(ctx, app, m.Target, lookup, m, providerSource, providerConfig, ra)
		tallyOutcome(outcome, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
		if err := persist(); err != nil {
			return nil, err
		}
	}

	return sealOutcome(l, rec, startedAt, resourcesApplied, resourcesFailed, resourcesStillUnknown, halted)
}

// sealOutcome computes a completed ship loop's overall outcome (applied |
// partially_applied | failed) and seals rec -- the shared tail both
// shipDriftRevert and shipChange use, so the outcome-classification rule
// itself can never drift between the two shippable kinds.
func sealOutcome(l *core.Ledger, rec *core.ApplyRecord, startedAt string, resourcesApplied, resourcesFailed, resourcesStillUnknown int64, halted bool) (*core.ApplyRecord, error) {
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
		emitProgress(ctx, ProgressEvent{
			Address: addr.String(), Kind: "reconcile_attempt",
			Attempt: attempt + 1, Total: maxReconcileAttempts,
			Detail: "verifying via read-back",
		})
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
			recordTransition(ctx, ra, core.ResourceApplied, "confirmed by reconciliation")
			return core.ResourceApplied
		case "failed":
			recordTransition(ctx, ra, core.ResourceFailed, "confirmed by reconciliation: change never landed")
			return core.ResourceFailed
		}
		time.Sleep(reconcileRetryInterval)
	}
	recordTransition(ctx, ra, core.ResourceStillUnknown, "reconciliation exhausted its retry budget without a conclusive answer")
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

	if matchesRestoreTarget(state, mod) {
		return "applied", nil
	}
	if matchesOriginalDrift(state, mod) {
		return "failed", nil
	}
	return "inconclusive", nil
}

// matchesRestoreTarget reports whether state matches the FULL restore
// target a shipped revert should produce -- not just mod.After's own
// dot-paths. Found live (UBI-26 session 4, docs/reliability-report.md): a
// revert whose only change is removing an attribute added out-of-band has
// an EMPTY After map (nothing to add back, see core.ApplyAfter's own
// Before-only-path-deletion logic) -- the original version of this check
// only ever compared against After, so it could never conclude "applied"
// for a pure-deletion revert, even when reconciliation was reading a live
// state that had, in fact, already been correctly corrected. Fixed to also
// require every Before-only path (something being deleted, not changed) be
// genuinely ABSENT from the observed state.
func matchesRestoreTarget(state map[string]interface{}, mod core.Modification) bool {
	if len(mod.After) == 0 && len(mod.Before) == 0 {
		return false // nothing recorded to check against -- never conclusive
	}
	for path, raw := range mod.After {
		var want interface{}
		if err := json.Unmarshal(raw, &want); err != nil {
			return false
		}
		got, ok := dotGet(state, path)
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	for path := range mod.Before {
		if _, ok := mod.After[path]; ok {
			continue // a genuine value change, already checked above
		}
		if _, present := dotGet(state, path); present {
			return false // should have been deleted by the revert, but isn't
		}
	}
	return true
}

// matchesOriginalDrift reports whether state still matches the ORIGINAL
// drifted values -- the revert never landed at all. The symmetric
// counterpart to matchesRestoreTarget: an After-only path (something the
// revert should have newly added back) must still be absent for this to
// hold, the same "pure addition" case in reverse.
func matchesOriginalDrift(state map[string]interface{}, mod core.Modification) bool {
	if len(mod.Before) == 0 {
		return false
	}
	for path, raw := range mod.Before {
		var want interface{}
		if err := json.Unmarshal(raw, &want); err != nil {
			return false
		}
		got, ok := dotGet(state, path)
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	for path := range mod.After {
		if _, ok := mod.Before[path]; ok {
			continue
		}
		if _, present := dotGet(state, path); present {
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
	lastState          core.ResourceState
	hasState           bool
	attemptsInFlight   int             // number of historical attempts that actually issued an ApplyResourceChange call for this address
	lastProviderResult json.RawMessage // most recent non-empty ProviderResult ever recorded for this address, across all attempts (UBI-27: a killed-and-restarted ship recovers a dependency's real output from here, not just from what the current invocation itself computed)

	// lastReconciliationOutcome is the most recent Reconciliation[].Outcome
	// ever recorded for this address, across all attempts (UBI-30,
	// docs/executor.md's "shipping destroys" amendment) -- a destroy's own
	// disambiguator: a post-timeout not-found read resolves "destroyed"
	// only when this was "present_matches" immediately beforehand, folded
	// across the parent attempt chain so a kill -9 between the destroy
	// landing and its result being recorded still resolves correctly on
	// the next attempt, never falling back to treating a bare not-found as
	// fresh "already_absent" just because THIS attempt never ran its own
	// pre-check. Meaningless for a create/modify address (its own
	// Reconciliation Outcomes are "applied"/"failed"/"inconclusive"); only
	// shipDestroyNode ever reads this field.
	lastReconciliationOutcome string

	// lastApplyClaimedSuccess folds forward whether the most recent
	// transition INTO ResourceUnknownPostTimeout for this address (UBI-44)
	// was reached because ApplyResourceChange itself reported success (a
	// non-empty Detail, "provider reported a successful destroy...") or
	// because the RPC was merely ambiguous (empty Detail, unchanged from
	// before this amendment). Lets a resumed reconciliation (after a
	// kill -9 between recording unknown_post_timeout and finishing the
	// read-back) still record the correct, honestly-worded outcome instead
	// of silently defaulting to the ambiguous-RPC wording.
	lastApplyClaimedSuccess bool
}

// foldResourceHistory folds addr's own ProviderResult forward as
// "the most recent non-empty one ever recorded," NOT "whatever the latest
// attempt's own entry says" -- a later attempt that finds this address
// already applied only ever records a pass-through confirmation
// transition (see the "already applied in a prior attempt" branches
// below), with no ProviderResult of its own, so naively preferring the
// latest attempt would silently lose the real value the FIRST attempt
// that actually applied it captured.
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
			if len(ra.ProviderResult) > 0 {
				h.lastProviderResult = ra.ProviderResult
			}
			for _, rec := range ra.Reconciliation {
				h.lastReconciliationOutcome = rec.Outcome // last one wins -- attempts and each one's own Reconciliation slice are both walked in chronological order
			}
			for _, t := range ra.Transitions {
				if t.State == core.ResourceInFlight {
					h.attemptsInFlight++
					break // count once per attempt, regardless of how many transitions it recorded
				}
			}
			for _, t := range ra.Transitions {
				if t.State == core.ResourceUnknownPostTimeout {
					h.lastApplyClaimedSuccess = t.Detail != "" // last one wins, same convention as lastReconciliationOutcome above
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

// allAddressesApplied is allAlreadyApplied's own generalization for
// shipChange, whose resources aren't all Modifications with a .Target --
// creates and modifies share one plain []core.Address view here instead.
func allAddressesApplied(attempts []*core.ApplyRecord, addrs []core.Address) bool {
	for _, addr := range addrs {
		h := foldResourceHistory(attempts, addr)
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

// --- shipping change proposals (UBI-27) -------------------------------

// createNode is core/executor's own typed view of a kind:"change"
// proposal's Delta.Creates entries (docs/schema.md's "Amendment: intent
// files and resolved change proposals") -- distinct from adoption's own
// state-keyed shape sharing the same []json.RawMessage slot. Only ever
// unmarshaled when p.Kind == core.KindChange.
type createNode struct {
	Stack     string            `json:"stack"`
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	Provider  *core.ProviderRef `json:"provider,omitempty"`
	Config    json.RawMessage   `json:"config"`
	DependsOn []string          `json:"depends_on,omitempty"`
}

// changeNode is one delta.creates, delta.modifies, or (UBI-30)
// delta.destroys entry of a kind:"change" proposal, prepared for
// shipChange's own combined, dependency-ordered walk. Exactly one of
// create/modify/destroy is non-nil.
//
// provider is docs/executor.md's own "Amendment (2026-07-18, UBI-43):
// multi-provider stacks" input -- which pool entry this node's own Apply/
// PlanResourceChange calls route through, copied from whichever of
// create/modify/destroy is set. Nil for a proposal resolved before this
// amendment (core/resolver always populates it going forward) -- nil is
// never treated as an error, only as "this node's own provider is
// whatever the invocation's own providerSource/providerConfig already
// name" (docs/schema.md's own "no schema_version bump" reasoning,
// applied here identically), resolved by shipChange's own loop, not
// assumed by the caller.
type changeNode struct {
	addr      core.Address
	dependsOn []string
	provider  *core.ProviderRef
	create    *createNode
	modify    *core.Modification
	destroy   *core.DestroyEntry
}

// changeNodesOf decodes p's own delta.creates/.modifies/.destroys into one
// combined, dependency-ordered walk list -- re-deriving the execution
// order from each entry's own depends_on field (Address.String() form)
// rather than trusting stored array order, generalizing "ubx ship
// independently applies its own sort" (docs/executor.md's "Serial
// execution in delta order," written for drift_revert's fixed canonical
// sort) to a real dependency graph, since a change proposal's resources
// can genuinely depend on each other. Root-visit order among resources
// with no dependency relation to each other is their own addresses'
// alphabetical order -- deterministic and reproducible across runs, the
// same tie-break convention sortedModifies already uses.
//
// "Creates forward, destroys reversed" (docs/executor.md's "Amendment
// (UBI-30): shipping destroys") is not a second walk -- delta.destroys
// entries extend this exact same byAddr map, keyed by their own
// depends_on, which docs/resolver.md's own orphan-protection walk
// populates with the REVERSE edge set (which surviving/same-batch
// resources this destroy must wait for) rather than the forward set a
// create/modify's own depends_on carries. One topoSortAddresses call over
// the combined graph is what makes the interleaving correct.
func changeNodesOf(p *core.Proposal) ([]*changeNode, error) {
	total := len(p.Delta.Creates) + len(p.Delta.Modifies) + len(p.Delta.Destroys)
	byAddr := make(map[string]*changeNode, total)
	keys := make([]string, 0, total)

	for _, raw := range p.Delta.Creates {
		var cn createNode
		if err := json.Unmarshal(raw, &cn); err != nil {
			return nil, fmt.Errorf("decode create node: %w", err)
		}
		addr := core.Address{Stack: cn.Stack, Type: cn.Type, Name: cn.Name}
		key := addr.String()
		byAddr[key] = &changeNode{addr: addr, dependsOn: cn.DependsOn, provider: cn.Provider, create: &cn}
		keys = append(keys, key)
	}
	for i := range p.Delta.Modifies {
		m := &p.Delta.Modifies[i]
		key := m.Target.String()
		byAddr[key] = &changeNode{addr: m.Target, dependsOn: m.DependsOn, provider: m.Provider, modify: m}
		keys = append(keys, key)
	}
	for i := range p.Delta.Destroys {
		d := &p.Delta.Destroys[i]
		key := d.Address.String()
		byAddr[key] = &changeNode{addr: d.Address, dependsOn: d.DependsOn, provider: d.Provider, destroy: d}
		keys = append(keys, key)
	}

	sort.Strings(keys)
	topoOrder, err := topoSortAddresses(keys, func(key string) []string { return byAddr[key].dependsOn })
	if err != nil {
		return nil, err
	}

	out := make([]*changeNode, 0, len(topoOrder))
	for _, key := range topoOrder {
		out = append(out, byAddr[key])
	}
	return out, nil
}

// topoSortAddresses orders addrs so every address appears after every
// address its own edges point at -- the same DFS path/inProgress/done
// pattern core/resolver's own topoSort uses (core/executor doesn't import
// core/resolver; this is a small, independent instance of the same
// general algorithm, not a shared dependency). edgesOf(addr) must return
// only addresses also present in addrs; a cycle is ErrDependencyCycle,
// naming the full cycle path -- never silently broken or arbitrarily
// ordered. In practice a cycle here means a corrupt or hand-tampered
// proposal file: core/resolver's own topoSort would already have refused
// to resolve one at resolve time.
func topoSortAddresses(addrs []string, edgesOf func(addr string) []string) ([]string, error) {
	const (
		unvisited = iota
		inProgress
		done
	)
	state := make(map[string]int, len(addrs))
	var order []string
	var path []string

	var visit func(addr string) error
	visit = func(addr string) error {
		switch state[addr] {
		case done:
			return nil
		case inProgress:
			cycleStart := 0
			for i, p := range path {
				if p == addr {
					cycleStart = i
					break
				}
			}
			cycle := append(append([]string{}, path[cycleStart:]...), addr)
			return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(cycle, " -> "))
		}
		state[addr] = inProgress
		path = append(path, addr)
		for _, dep := range edgesOf(addr) {
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[addr] = done
		order = append(order, addr)
		return nil
	}

	for _, a := range addrs {
		if err := visit(a); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// computedMarkerKey mirrors core/resolver's own "$computed" marker key
// (docs/schema.md's value-encoding conventions). core/executor and
// core/resolver don't import each other -- this string is the wire-format
// convention both independently conform to, the same pattern
// provider/redact.go's redactedMarkerKey already establishes for
// "$redacted".
const computedMarkerKey = "$computed"

// containsComputedMarker reports whether v (an already-decoded JSON
// value) carries a {"$computed": {...}} marker anywhere within it, at
// any depth -- mirrors core/resolver's own containsMarker (refs.go),
// scoped to $computed alone since that's the only marker
// substituteComputed ever resolves. Gates substituteComputed's own
// string-leaf handling, below: an ordinary string that merely happens to
// parse as JSON but carries no marker is returned byte-for-byte
// untouched, never re-serialized just because it happened to decode.
func containsComputedMarker(v interface{}) bool {
	switch t := v.(type) {
	case map[string]interface{}:
		if len(t) == 1 {
			if _, ok := t[computedMarkerKey]; ok {
				return true
			}
		}
		for _, vv := range t {
			if containsComputedMarker(vv) {
				return true
			}
		}
		return false
	case []interface{}:
		for _, vv := range t {
			if containsComputedMarker(vv) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// substituteComputed walks v's JSON tree (already decoded-generic) and
// replaces every {"$computed": {"from": "<stack>.<type>.<name>.<path>"}}
// marker with the concrete value read (via core.DotGet) from
// resultsByAddr[<address>] at <path> -- docs/executor.md's amendment:
// "applied outputs feed into dependents' PlannedState, mid-walk." Every
// dependency address a marker names MUST already be a key of
// resultsByAddr by the time this runs (shipChange's own topo order + "no
// resource ships ahead of its own depends_on" precondition guarantees
// it) -- ErrDependencyNotApplied if not, never silently left as the
// marker itself or guessed at.
//
// UBI-63 session 2's own "deferred materialization" amendment: a config
// attribute the provider schema types as a plain string but treats as
// nested JSON (an IAM policy document) can carry a $computed marker one
// level down, inside that string's own decoded structure -- the signed
// proposal carries this as a TEMPLATE (core/resolver's own
// unsafeToEmbedSecret no longer refuses a JSON-embedded $computed, only
// $secret, docs/resolver.md). The string case below decodes, recurses
// through this same function (substitution reaches an embedded marker no
// differently than a top-level one), and re-encodes -- symmetric with
// core/resolver's own resolveStringValue/containsMarker round trip at
// resolve time. A string that merely happens to parse as JSON but
// carries no $computed marker anywhere is returned completely untouched,
// byte for byte -- this is not a general "reformat every JSON-shaped
// string" pass, only a template-fill one.
func substituteComputed(v interface{}, resultsByAddr map[string]json.RawMessage) (interface{}, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		if len(t) == 1 {
			if inner, ok := t[computedMarkerKey].(map[string]interface{}); ok {
				from, _ := inner["from"].(string)
				addr, path, err := splitComputedFrom(from)
				if err != nil {
					return nil, err
				}
				result, known := resultsByAddr[addr]
				if !known {
					return nil, fmt.Errorf("%w: %s", ErrDependencyNotApplied, addr)
				}
				val, found, err := core.DotGet(result, path)
				if err != nil {
					return nil, fmt.Errorf("resolve %s: %w", from, err)
				}
				if !found {
					return nil, fmt.Errorf("%w: %s", ErrDependencyOutputMissing, from)
				}
				var out interface{}
				if err := json.Unmarshal(val, &out); err != nil {
					return nil, fmt.Errorf("resolve %s: decode: %w", from, err)
				}
				return out, nil
			}
		}
		out := make(map[string]interface{}, len(t))
		for k, vv := range t {
			rv, err := substituteComputed(vv, resultsByAddr)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, vv := range t {
			rv, err := substituteComputed(vv, resultsByAddr)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case string:
		var decoded interface{}
		if err := json.Unmarshal([]byte(t), &decoded); err != nil || !containsComputedMarker(decoded) {
			return t, nil
		}
		substituted, err := substituteComputed(decoded, resultsByAddr)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(substituted)
		if err != nil {
			return nil, fmt.Errorf("re-encode JSON-embedded template: %w", err)
		}
		return string(b), nil
	default:
		return v, nil
	}
}

// substituteModificationComputed returns a copy of m with every $computed
// marker in After's own dot-path values substituted per substituteComputed
// -- never mutating the proposal's own Modification in place (the
// original, marker-bearing After is what a future `ubx why`/re-resolve
// must keep reading; only ship's own transient PlannedState construction
// and reconciliation-verdict comparison need the substituted, concrete
// form).
func substituteModificationComputed(m core.Modification, resultsByAddr map[string]json.RawMessage) (core.Modification, error) {
	if len(m.After) == 0 {
		return m, nil
	}
	out := m
	out.After = make(map[string]json.RawMessage, len(m.After))
	for path, raw := range m.After {
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return core.Modification{}, fmt.Errorf("after[%q]: %w", path, err)
		}
		sv, err := substituteComputed(v, resultsByAddr)
		if err != nil {
			return core.Modification{}, fmt.Errorf("after[%q]: %w", path, err)
		}
		b, err := json.Marshal(sv)
		if err != nil {
			return core.Modification{}, fmt.Errorf("after[%q]: %w", path, err)
		}
		out.After[path] = b
	}
	return out, nil
}

// splitComputedFrom splits a $computed marker's "from" pointer
// ("<stack>.<type>.<name>.<path>") into its address portion and attribute
// path -- the same SplitN(s, ".", 4) convention core/resolver's own
// splitRefTarget uses for $ref/$cross targets.
func splitComputedFrom(s string) (addr, path string, err error) {
	parts := strings.SplitN(s, ".", 4)
	if len(parts) != 4 {
		return "", "", fmt.Errorf("%w: %q", ErrMalformedComputedMarker, s)
	}
	return strings.Join(parts[:3], "."), parts[3], nil
}

// shipChange executes an accepted kind:"change" proposal's delta.creates,
// delta.modifies, and (UBI-30) delta.destroys together, in real dependency
// order (changeNodesOf's own topo-sort) -- docs/executor.md's UBI-27
// amendment, extended by its own "Amendment (UBI-30): shipping destroys."
// A resource whose own depends_on names another resource in this same
// proposal is never attempted until that dependency has reached applied
// -- checked against both this invocation's own progress and every prior
// attempt's durably recorded result, so a `ubx ship` killed between one
// resource applying and the next starting recovers the completed one's
// REAL output on re-run (foldResourceHistory's own lastProviderResult),
// rather than re-creating it or losing the value a dependent needs. This
// is what makes "creates forward, destroys reversed" one interleaved walk
// rather than two: a destroy's own depends_on (docs/resolver.md's
// orphan-protection walk, the reverse edge set) is followed by exactly
// the same precondition check as a create's or modify's forward one.
//
// Unlike shipDriftRevert's single "halted" flag (one stale resource blocks
// every resource after it in a fixed canonical order), a blocked resource
// here only blocks its OWN dependents -- an independent resource elsewhere
// in the same proposal still proceeds. sealOutcome is always called with
// halted=false for this reason; "partially_applied" still correctly
// surfaces via resourcesFailed/resourcesStillUnknown alone.
func shipChange(ctx context.Context, l *core.Ledger, pool ApplierPool, providerSource string, p *core.Proposal) (*core.ApplyRecord, error) {
	if p.Acceptance == nil {
		return nil, ErrNotAccepted
	}

	nodes, err := changeNodesOf(p)
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}

	attempts, err := l.ApplyAttempts(p.ID)
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}

	addrs := make([]core.Address, 0, len(nodes))
	for _, n := range nodes {
		addrs = append(addrs, n.addr)
	}
	if allAddressesApplied(attempts, addrs) {
		return nil, ErrAlreadyApplied
	}

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		return nil, fmt.Errorf("ship: %w", err)
	}

	startedAt := nowRFC3339()
	persist := func() error {
		if err := l.SaveApplyProgress(rec); err != nil {
			return fmt.Errorf("ship: %w", err)
		}
		return nil
	}

	// Seed resultsByAddr from every node already fully applied in a PRIOR
	// attempt -- the recovery path a kill-and-restart depends on: a later
	// attempt's own pass-through confirmation never re-records
	// ProviderResult (see foldResourceHistory's own comment), so this must
	// come from history, not from this invocation's own (so far empty)
	// progress. Gated on hist.lastState alone, not on lastProviderResult
	// being non-empty (UBI-30 fix): a destroy has no ProviderResult at
	// all, by design (nothing to store once a resource is gone), but its
	// completion must still satisfy anything depends_on-ing it (a same-batch
	// mutual destroy, or a destroy of a destroy's own dependent's dependent)
	// the exact same way a create/modify's completion does. Missing this
	// case wrongly re-blocked a fully-destroyed dependency forever on
	// every subsequent `ubx ship` re-run -- found by this session's own
	// hermetic "re-ship after partial destroy" test, not assumed correct.
	resultsByAddr := make(map[string]json.RawMessage, len(nodes))
	for _, n := range nodes {
		hist := foldResourceHistory(attempts, n.addr)
		if hist.hasState && hist.lastState == core.ResourceApplied {
			resultsByAddr[n.addr.String()] = hist.lastProviderResult
		}
	}

	var resourcesApplied, resourcesFailed, resourcesStillUnknown int64

	for _, n := range nodes {
		key := n.addr.String()
		ra := &core.ResourceApply{Address: n.addr}
		rec.Resources = append(rec.Resources, ra)
		hist := foldResourceHistory(attempts, n.addr)

		if hist.hasState && hist.lastState == core.ResourceApplied {
			recordTransition(ctx, ra, core.ResourceApplied, "already applied in a prior attempt")
			resourcesApplied++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		var missingDep string
		for _, dep := range n.dependsOn {
			if _, ok := resultsByAddr[dep]; !ok {
				missingDep = dep
				break
			}
		}
		if missingDep != "" {
			recordTransition(ctx, ra, core.ResourcePending, "")
			recordError(ctx, ra, fmt.Sprintf("blocked: dependency %s has not applied -- refusing to ship a resource ahead of what it depends on", missingDep), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		// Route this node to its own recorded provider's pool entry
		// (docs/executor.md's own "Amendment (UBI-43): multi-provider
		// stacks" client pool) -- nil (a proposal resolved before this
		// amendment) falls back to the invocation's own providerSource,
		// version "", exactly what SingleApplierPool's single entry
		// already answers regardless of the pair asked for. A launch
		// failure here is a per-node terminal error, never a whole-walk
		// abort (docs/multi-provider-adversarial.md row 4): this node
		// fails, `continue` lets every other node -- including ones
		// against a different, already-launched-fine provider -- proceed
		// in its own turn, unaffected.
		provSource, provVersion := providerSource, ""
		if n.provider != nil {
			provSource, provVersion = n.provider.Source, n.provider.Version
		}
		app, providerConfig, err := pool.Get(ctx, provSource, provVersion)
		if err != nil {
			recordTransition(ctx, ra, core.ResourcePending, "")
			recordError(ctx, ra, fmt.Sprintf("provider unavailable: %v", err), core.ErrorTerminal)
			resourcesFailed++
			if err := persist(); err != nil {
				return nil, err
			}
			continue
		}

		var stepErr error
		switch {
		case n.create != nil:
			stepErr = shipCreate(ctx, app, providerConfig, n.create, ra, resultsByAddr, hist, persist, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
		case n.destroy != nil:
			stepErr = shipDestroyNode(ctx, app, provSource, providerConfig, p, *n.destroy, ra, hist, persist, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
		default:
			stepErr = shipModifyNode(ctx, app, provSource, providerConfig, p, *n.modify, ra, resultsByAddr, hist, persist, &resourcesApplied, &resourcesFailed, &resourcesStillUnknown)
		}
		if stepErr != nil {
			return nil, stepErr
		}
		if st, ok := ra.LastState(); ok && st == core.ResourceApplied {
			resultsByAddr[key] = ra.ProviderResult
		}
		if debugDelayBetweenChangeResources > 0 {
			time.Sleep(debugDelayBetweenChangeResources)
		}
	}

	sealed, err := sealOutcome(l, rec, startedAt, resourcesApplied, resourcesFailed, resourcesStillUnknown, false)
	if err != nil {
		return nil, err
	}
	reconcileSameBatchEffects(ctx, l, pool, providerSource, p, nodes, sealed)
	return sealed, nil
}

// reconcileSameBatchEffects re-observes every resource this same
// shipChange batch just applied that at least one OTHER node in the same
// batch actually depends_on -- an independent node nothing depends on can
// only ever be re-reading its own still-accurate snapshot, so it's
// skipped entirely rather than spending an unneeded extra pool.Get/
// ReadResource round trip on the overwhelmingly common case (most
// batches have no cross-effect at all). Once the whole dependency-
// ordered walk has finished, this records any resulting difference on a
// depended-on resource as an auto-accepted drift_adopt (UBI-63 session
// 3): a real, live finding showed a LATER same-batch
// dependent's own apply (an aws_iam_role_policy_attachment, last in a
// 5-resource chain) has a real, factual effect on an EARLIER resource's
// own observable state (attachment_count, managed_policy_arns) that the
// earlier resource's own apply-time snapshot could never have captured,
// since it was taken before the dependent even existed. Without this, the
// very next `ubx scan` reports that effect as drift, even though nothing
// outside this ship ever touched the resource.
//
// This is deliberately NOT normalization (core.explainedByNormalization,
// UBI-63 session 3's other half): the values genuinely changed (0 -> 1,
// [] -> [a real ARN]) and that change is worth recording -- just not
// something a human needs to separately review and re-approve, since the
// batch that caused it was already approved as one unit. Recording it as
// drift_adopt (the same mechanism `ubx scan --propose` already uses for
// "the world changed outside the ledger") reuses one well-understood
// mechanism rather than inventing a new ledger concept -- it folds
// forward through core.Ledger.FoldState exactly like any other adopted
// drift, so the resource's own recorded baseline is what's actually true
// by the time this ship call returns.
//
// Best-effort throughout: rec is already sealed and returned regardless
// of what this finds, so a failure to re-observe or record any one
// resource here never fails (or even surfaces from) the ship itself --
// this is a self-healing addition to an already-correct, already-durable
// outcome, not a correctness-critical step.
func reconcileSameBatchEffects(ctx context.Context, l *core.Ledger, pool ApplierPool, providerSource string, p *core.Proposal, nodes []*changeNode, rec *core.ApplyRecord) {
	byAddr := make(map[string]*core.ResourceApply, len(rec.Resources))
	for _, ra := range rec.Resources {
		byAddr[ra.Address.String()] = ra
	}
	// dependedOn: only a resource some OTHER node in this same batch
	// actually declares depends_on can plausibly have been mutated by a
	// same-batch sibling's own apply -- an independent node (nothing
	// depends on it) can only ever be re-reading its own, still-accurate
	// snapshot, so skipping it entirely avoids a wasted extra
	// pool.Get/ReadResource round trip for the overwhelmingly common
	// case (most batches have no such cross-effect at all).
	dependedOn := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		for _, dep := range n.dependsOn {
			dependedOn[dep] = true
		}
	}
	for _, n := range nodes {
		if n.destroy != nil {
			continue // gone -- nothing left to re-observe
		}
		if !dependedOn[n.addr.String()] {
			continue // nothing in this batch could have mutated it
		}
		ra, ok := byAddr[n.addr.String()]
		if !ok {
			continue
		}
		if st, ok := ra.LastState(); !ok || st != core.ResourceApplied {
			continue
		}
		lookup := ra.Lookup
		if len(lookup) == 0 {
			lookup, _ = lookupFor(p, n.addr)
		}
		if len(lookup) == 0 {
			continue // nothing to re-read this resource by
		}
		provSource, provVersion := providerSource, ""
		if n.provider != nil {
			provSource, provVersion = n.provider.Source, n.provider.Version
		}
		app, providerConfig, err := pool.Get(ctx, provSource, provVersion)
		if err != nil {
			continue
		}
		res, err := core.RunScan(ctx, app, l, core.ScanRequest{
			Address:        n.addr,
			ProviderConfig: providerConfig,
			CurrentState:   lookup,
			ProviderSource: provSource,
		})
		if err != nil || res.Outcome != core.ScanDrifted {
			continue
		}
		reconcileProposal, err := core.GenerateProposal(l, p.Stack, res)
		if err != nil {
			continue
		}
		reconcileProposal.Intent.Summary = fmt.Sprintf(
			"%s's own real, same-batch side effect from shipping %s (post-chain re-observation, UBI-63)",
			n.addr, p.ID)
		_, _ = core.Accept(l, reconcileProposal)
	}
}

// shipCreate ships one delta.creates entry: PriorState is the literal
// JSON "null" (a genuine from-scratch create, docs/executor.md's
// amendment -- never an empty/absent input, which encodeDynamicValue's
// own "defaults to {}" convenience would otherwise turn into an all-null
// OBJECT rather than the true top-level null a real provider's Apply
// needs to recognize "this doesn't exist yet"). PlannedState is cn.Config
// with every $computed marker substituted for its dependency's real,
// already-applied value; any of cn.Config's OWN schema-Computed
// attributes it never set at all are left for provider/ctyvalue.go's
// encodeUnknownAwareDynamicValue to mark Unknown, not resolved here.
//
// A real, load-bearing bug found live-testing this against AWS for the
// first time (UBI-27's own live finale): a create never goes through
// core.ReadAndFingerprint/core.VerifyFreshness at all (there is nothing
// to read yet), and those are the ONLY places drift_revert's own modify
// path ever calls Applier.Configure -- so a create-only change proposal
// reached ApplyResourceChange against a real provider that had NEVER been
// configured (no region, no credentials). Against terraform-provider-aws
// this didn't surface as a clean error: the request killed the provider
// subprocess outright, surfacing to ubx as a bare transport EOF
// ("rpc error: code = Unavailable desc = error reading from server:
// EOF"), indistinguishable at first glance from a genuine crash-mid-call.
// Configure is called explicitly here, every time, before the first
// create's schema is even used -- the same repeated-Configure pattern
// core.readAndFingerprint's own callers already establish for modify
// (VerifyFreshness and ReadAndFingerprint each call it again), confirmed
// safe there against a real provider across UBI-26's own live sessions.
func shipCreate(ctx context.Context, app Applier, providerConfig json.RawMessage, cn *createNode, ra *core.ResourceApply, resultsByAddr map[string]json.RawMessage, hist resourceHistory, persist func() error, resourcesApplied, resourcesFailed, resourcesStillUnknown *int64) error {
	recordTransition(ctx, ra, core.ResourcePending, "")

	if hist.attemptsInFlight >= maxApplyAttemptsPerResource {
		recordError(ctx, ra, fmt.Sprintf("retry budget exhausted after %d attempt(s)", hist.attemptsInFlight), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	var raw interface{}
	if err := json.Unmarshal(cn.Config, &raw); err != nil {
		recordError(ctx, ra, fmt.Sprintf("decode config: %v", err), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}
	substituted, err := substituteComputed(raw, resultsByAddr)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("resolve dependency output: %v", err), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}
	planned, err := json.Marshal(substituted)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("construct planned state: %v", err), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	providerSchema, resourceSchemas, err := app.Schema(ctx)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("fetch schema: %v", err), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	}
	if err := app.Configure(ctx, providerSchema, providerConfig); err != nil {
		recordError(ctx, ra, fmt.Sprintf("configure provider: %v", err), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	}

	// THE invariant (docs/executor.md): in_flight is durably persisted
	// before the risky ApplyResourceChange call, never after.
	recordTransition(ctx, ra, core.ResourceInFlight, "")
	if err := persist(); err != nil {
		return fmt.Errorf("ship: persist in_flight: %w", err)
	}
	if debugDelayAfterInFlight > 0 {
		time.Sleep(debugDelayAfterInFlight)
	}

	result, lookup, applyErr := app.ApplyResourceChange(ctx, resourceSchemas[cn.Type], cn.Type, json.RawMessage("null"), planned, nil)

	if applyErr == nil {
		if debugDelayAfterApplySuccess > 0 {
			time.Sleep(debugDelayAfterApplySuccess)
		}
		ra.ProviderResult = result
		// UBI-29 (docs/schema.md's own amendment): recorded explicitly,
		// right here, the moment this create is real -- never left for a
		// future reader to derive on demand (the same "persist a lookup
		// key, don't re-derive it at need-time" lesson Slice 3 already
		// established for resolution.inputs[].lookup). This is what makes
		// the resource discoverable by core.Ledger.Fleet/FoldState/
		// ProposalsForAddress/LastObservedHash afterward; core/executor
		// itself needs to know nothing about any of those readers.
		//
		// UBI-63 session 2: prefer the Applier's own lookup (computed
		// with a real, concrete schema in scope -- see ApplyResourceChange's
		// own doc comment) over the id-only default derivation here,
		// which core/executor's own provider-import-free boundary means
		// it can never do any better than on its own.
		ra.Lookup = lookup
		if len(ra.Lookup) == 0 {
			ra.Lookup = core.DeriveLookupFromResult(result, nil)
		}
		recordTransition(ctx, ra, core.ResourceApplied, "")
		*resourcesApplied++
		return persist()
	}

	var terminal *TerminalError
	if errors.As(applyErr, &terminal) {
		recordError(ctx, ra, terminal.Error(), core.ErrorTerminal)
		recordTransition(ctx, ra, core.ResourceFailed, "")
		*resourcesFailed++
		return persist()
	}

	// Retryable/ambiguous: unlike a modify (which can reconcile-by-query
	// against the resource's own already-known lookup key), a create that
	// fails ambiguously has no identity to look anything up by yet -- the
	// resource may or may not exist in the real provider. This is a real,
	// named v1 limitation, not silently swept: it's left unknown_post_timeout
	// and NOT retried automatically within this invocation; a human must
	// resolve it (e.g. a real `aws` CLI check for whatever identity the
	// provider might have assigned) before the next `ubx ship` can safely
	// proceed past it. core/resolver's own $computed dependents downstream
	// of this resource stay correctly blocked (shipChange's own
	// missing-dependency check) until that happens.
	recordError(ctx, ra, applyErr.Error(), core.ErrorRetryable)
	recordTransition(ctx, ra, core.ResourceUnknownPostTimeout, "")
	*resourcesStillUnknown++
	return persist()
}

// shipModifyNode ships one delta.modifies entry within a change proposal
// -- the same freshness/read/apply/reconcile shape shipDriftRevert's own
// per-resource logic already uses, extended only to substitute any
// $computed marker in m.After before core.ApplyAfter/reconciliation ever
// see it (m itself, and the proposal's own recorded content, are never
// mutated -- substituteModificationComputed returns a copy).
func shipModifyNode(ctx context.Context, app Applier, providerSource string, providerConfig json.RawMessage, p *core.Proposal, m core.Modification, ra *core.ResourceApply, resultsByAddr map[string]json.RawMessage, hist resourceHistory, persist func() error, resourcesApplied, resourcesFailed, resourcesStillUnknown *int64) error {
	if paths := redactedAfterPaths(m); len(paths) > 0 {
		recordTransition(ctx, ra, core.ResourcePending, "")
		recordError(ctx, ra, fmt.Sprintf(
			"declined: after value(s) at %s are redacted -- the ledger holds a salted hash, not the real secret material, and ubx will never construct a live apply from it; use `ubx revert-plan` for this resource's manual reconciliation steps instead",
			strings.Join(paths, ", ")), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	lookup, haveLookup := lookupFor(p, m.Target)

	if hist.hasState && needsReconciliation(hist.lastState) {
		recordTransition(ctx, ra, core.ResourcePending, "")
		if !haveLookup {
			recordError(ctx, ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
			*resourcesFailed++
			return persist()
		}
		substitutedMod, err := substituteModificationComputed(m, resultsByAddr)
		if err != nil {
			recordError(ctx, ra, fmt.Sprintf("resolve dependency output: %v", err), core.ErrorTerminal)
			*resourcesFailed++
			return persist()
		}
		outcome := reconcileLoop(ctx, app, m.Target, lookup, substitutedMod, providerSource, providerConfig, ra)
		tallyOutcome(outcome, resourcesApplied, resourcesFailed, resourcesStillUnknown)
		return persist()
	}

	recordTransition(ctx, ra, core.ResourcePending, "")

	if hist.attemptsInFlight >= maxApplyAttemptsPerResource {
		recordError(ctx, ra, fmt.Sprintf("retry budget exhausted after %d attempt(s)", hist.attemptsInFlight), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	if err := core.VerifyFreshness(ctx, app, m.Target, providerSource, providerConfig, p); err != nil {
		if errors.Is(err, core.ErrStaleObservation) {
			recordError(ctx, ra, err.Error(), core.ErrorTerminal)
		} else {
			recordError(ctx, ra, err.Error(), core.ErrorRetryable)
		}
		*resourcesFailed++
		return persist()
	}

	if !haveLookup {
		recordError(ctx, ra, "no resolution.inputs lookup key recorded for this resource", core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	observed, _, err := core.ReadAndFingerprint(ctx, app, m.Target, providerSource, providerConfig, lookup)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("read prior state: %v", err), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	}

	substitutedMod, err := substituteModificationComputed(m, resultsByAddr)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("resolve dependency output: %v", err), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	planned, err := core.ApplyAfter(observed, substitutedMod)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("construct planned state: %v", err), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}
	_, resourceSchemas, err := app.Schema(ctx)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("fetch schema: %v", err), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	}

	recordTransition(ctx, ra, core.ResourceInFlight, "")
	if err := persist(); err != nil {
		return fmt.Errorf("ship: persist in_flight: %w", err)
	}
	if debugDelayAfterInFlight > 0 {
		time.Sleep(debugDelayAfterInFlight)
	}

	result, _, applyErr := app.ApplyResourceChange(ctx, resourceSchemas[m.Target.Type], m.Target.Type, observed, planned, nil)

	if applyErr == nil {
		if debugDelayAfterApplySuccess > 0 {
			time.Sleep(debugDelayAfterApplySuccess)
		}
		ra.ProviderResult = result
		recordTransition(ctx, ra, core.ResourceApplied, "")
		*resourcesApplied++
		return persist()
	}

	var terminal *TerminalError
	if errors.As(applyErr, &terminal) {
		recordError(ctx, ra, terminal.Error(), core.ErrorTerminal)
		recordTransition(ctx, ra, core.ResourceFailed, "")
		*resourcesFailed++
		return persist()
	}

	recordError(ctx, ra, applyErr.Error(), core.ErrorRetryable)
	recordTransition(ctx, ra, core.ResourceUnknownPostTimeout, "")
	if err := persist(); err != nil {
		return err
	}
	outcome := reconcileLoop(ctx, app, m.Target, lookup, substitutedMod, providerSource, providerConfig, ra)
	tallyOutcome(outcome, resourcesApplied, resourcesFailed, resourcesStillUnknown)
	return persist()
}

// shipDestroyNode ships one delta.destroys entry within a change proposal
// -- docs/executor.md's "Amendment (UBI-30): shipping destroys." Three
// things distinguish this from shipCreate/shipModifyNode, all reused
// mechanisms extended rather than new ones invented:
//
//  1. A three-way freshness precheck, not binary: present-and-matching
//     (proceed), present-but-drifted (refuse -- the operator never
//     reviewed this exact state), or already-absent (short-circuit
//     straight to a terminal success, never reaching in_flight, never
//     calling ApplyResourceChange at all). Recorded as a Reconciliation
//     entry (docs/schema.md's "Reconciliation, reused" section) one step
//     earlier than Reconciliation's only prior use (post-timeout).
//  2. Wire mechanics: PriorState is the freshly re-read live state (never
//     the resolve-time delta.destroys[].state snapshot directly, which
//     may be stale by ship time); PlannedState is the literal JSON "null"
//     -- the real tfplugin convention for "destroy this," which
//     cli/stateadapter.go's own Config==PlannedState convention already
//     carries through with zero changes needed there.
//  3. A post-timeout reconcile-by-query must distinguish "destroyed" from
//     "already_absent" -- a bare not-found read is ambiguous in isolation,
//     disambiguated by whether the immediately preceding observation
//     (this attempt's own precheck, or a prior attempt's, folded via
//     hist.lastReconciliationOutcome) confirmed the target was present and
//     matching. See reconcileDestroyLoop.
func shipDestroyNode(ctx context.Context, app Applier, providerSource string, providerConfig json.RawMessage, p *core.Proposal, entry core.DestroyEntry, ra *core.ResourceApply, hist resourceHistory, persist func() error, resourcesApplied, resourcesFailed, resourcesStillUnknown *int64) error {
	lookup, expectedHash, haveTarget := destroyTargetFor(p, entry.Address)

	// Reconcile first if the last thing we know about this resource is
	// unresolved (in_flight/unknown_post_timeout/still_unknown from a
	// prior, possibly-crashed attempt) -- the same idempotency contract
	// creates/modifies already use: never a blind re-attempt on top of an
	// unresolved unknown. A destroy that reached in_flight in a prior
	// attempt skips straight here, never re-running its own precheck --
	// hist.lastReconciliationOutcome (folded across that prior attempt)
	// is what still lets reconcileDestroyLoop disambiguate correctly.
	if hist.hasState && needsReconciliation(hist.lastState) {
		recordTransition(ctx, ra, core.ResourcePending, "")
		if !haveTarget {
			recordError(ctx, ra, "no resolution.inputs destroy_target lookup recorded for this resource", core.ErrorTerminal)
			*resourcesFailed++
			return persist()
		}
		priorPresentMatches := hist.lastReconciliationOutcome == "present_matches"
		outcome := reconcileDestroyLoop(ctx, app, entry.Address, lookup, providerSource, providerConfig, priorPresentMatches, hist.lastApplyClaimedSuccess, ra)
		tallyOutcome(outcome, resourcesApplied, resourcesFailed, resourcesStillUnknown)
		return persist()
	}

	recordTransition(ctx, ra, core.ResourcePending, "")

	if hist.attemptsInFlight >= maxApplyAttemptsPerResource {
		recordError(ctx, ra, fmt.Sprintf("retry budget exhausted after %d attempt(s)", hist.attemptsInFlight), core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	if !haveTarget {
		recordError(ctx, ra, "no resolution.inputs destroy_target lookup recorded for this resource", core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}

	observed, hash, readErr := core.ReadAndFingerprint(ctx, app, entry.Address, providerSource, providerConfig, lookup)
	at := nowRFC3339()
	switch {
	case readErr != nil && errors.Is(readErr, core.ErrResourceUnreadable):
		// Already absent -- a legitimate, low-friction terminal success,
		// never a refusal: there is nothing left to lose that the operator
		// didn't already implicitly ask to lose. Never reaches in_flight,
		// never calls ApplyResourceChange.
		ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "already_absent"})
		recordTransition(ctx, ra, core.ResourceApplied, "already absent before this attempt")
		*resourcesApplied++
		return persist()
	case readErr != nil:
		recordError(ctx, ra, fmt.Sprintf("freshness recheck: %v", readErr), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	case hash != expectedHash:
		// Present, but drifted from what was signed away -- refused,
		// exactly like "Stale detected mid-partial-apply," generalized:
		// destroying state the operator never actually reviewed defeats
		// the whole reason delta.destroys[].state is carried inline in the
		// proposal at all.
		ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "present_drifted"})
		recordError(ctx, ra, "destroy target drifted since it was signed away -- refusing to destroy state that was never reviewed", core.ErrorTerminal)
		*resourcesFailed++
		return persist()
	}
	ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "present_matches"})

	_, resourceSchemas, err := app.Schema(ctx)
	if err != nil {
		recordError(ctx, ra, fmt.Sprintf("fetch schema: %v", err), core.ErrorRetryable)
		*resourcesFailed++
		return persist()
	}

	// A real PlanResourceChange call, unconditionally, before the risky
	// Apply -- UBI-30's own correction to this section's original design.
	// Verified empirically (docs/executor.md's own amendment), not assumed
	// safe: skipping this (PlannedState="null", PlannedPrivate left empty)
	// silently no-ops against a real, complex SDKv2 provider -- it reports
	// success without actually deleting anything, because the provider's
	// internal shim needs the real PlannedPrivate bytes only a genuine
	// Plan call produces to recognize this as a destroy at all. Plan
	// itself is read-only by protocol contract (a real `terraform plan`
	// never mutates infrastructure), so this runs BEFORE the in_flight
	// transition below -- a Plan failure means the risky call never
	// happens at all, and the resource correctly never leaves `pending`.
	_, plannedPrivate, planErr := app.PlanResourceChange(ctx, resourceSchemas[entry.Address.Type], entry.Address.Type, observed, json.RawMessage("null"))
	if planErr != nil {
		var terminal *TerminalError
		if errors.As(planErr, &terminal) {
			recordError(ctx, ra, terminal.Error(), core.ErrorTerminal)
		} else {
			recordError(ctx, ra, planErr.Error(), core.ErrorRetryable)
		}
		*resourcesFailed++
		return persist()
	}

	// THE invariant (docs/executor.md): in_flight is durably persisted
	// before the risky ApplyResourceChange call, never after.
	recordTransition(ctx, ra, core.ResourceInFlight, "")
	if err := persist(); err != nil {
		return fmt.Errorf("ship: persist in_flight: %w", err)
	}
	if debugDelayAfterInFlight > 0 {
		time.Sleep(debugDelayAfterInFlight)
	}

	// PlannedState (and, per cli/stateadapter.go's own Config==PlannedState
	// convention, Config too) is the literal JSON "null" -- the real
	// tfplugin signal for "destroy this," PriorState non-null (docs/executor.md's
	// own amendment). plannedPrivate is threaded straight from the
	// PlanResourceChange call above, opaque, never inspected here.
	_, _, applyErr := app.ApplyResourceChange(ctx, resourceSchemas[entry.Address.Type], entry.Address.Type, observed, json.RawMessage("null"), plannedPrivate)

	if applyErr == nil {
		if debugDelayAfterApplySuccess > 0 {
			time.Sleep(debugDelayAfterApplySuccess)
		}
		// UBI-44: a clean ApplyResourceChange response is never sufficient
		// on its own -- found live, against a real google_pubsub_topic,
		// against real GCP: the provider returned this exact shape (no
		// error, a genuine top-level null NewState) while Cloud Audit Logs
		// showed zero DeleteTopic calls ever made. The same mandatory
		// post-destroy read-back an ambiguous Apply result already
		// required (below) now runs unconditionally here too -- the read-
		// back earns the destroyed verdict; the provider's own reported
		// success never does, by itself. applyClaimedSuccess=true lets
		// reconcileDestroyLoop distinguish "the provider actively lied"
		// from "the RPC itself was merely ambiguous" in what it records.
		recordTransition(ctx, ra, core.ResourceUnknownPostTimeout, "provider reported a successful destroy -- verifying via a post-destroy read-back before recording it as destroyed")
		if err := persist(); err != nil {
			return err
		}
		outcome := reconcileDestroyLoop(ctx, app, entry.Address, lookup, providerSource, providerConfig, true, true, ra)
		tallyOutcome(outcome, resourcesApplied, resourcesFailed, resourcesStillUnknown)
		return persist()
	}

	var terminal *TerminalError
	if errors.As(applyErr, &terminal) {
		// A real, structured ERROR diagnostic is the provider's own honest
		// negative answer -- this document's error taxonomy already trusts
		// it without a read-back (docs/executor.md's UBI-44 amendment: the
		// asymmetry is specifically about trusting a rosy answer, not a
		// downbeat one). No reconciliation needed here.
		recordError(ctx, ra, terminal.Error(), core.ErrorTerminal)
		recordTransition(ctx, ra, core.ResourceFailed, "")
		*resourcesFailed++
		return persist()
	}

	// Retryable/ambiguous: the RPC didn't resolve into a clear answer.
	recordError(ctx, ra, applyErr.Error(), core.ErrorRetryable)
	recordTransition(ctx, ra, core.ResourceUnknownPostTimeout, "")
	if err := persist(); err != nil {
		return err
	}
	outcome := reconcileDestroyLoop(ctx, app, entry.Address, lookup, providerSource, providerConfig, true, false, ra)
	tallyOutcome(outcome, resourcesApplied, resourcesFailed, resourcesStillUnknown)
	return persist()
}

// reconcileDestroyLoop repeatedly reads addr's live state (reconcile-by-query)
// -- universally now (UBI-44), not just after an ambiguous Apply result --
// up to len(destroyReconcileBackoffSchedule) times, distinguishing
// "destroyed" from "already_absent"/"failed" per docs/executor.md's own
// amendment:
//
//   - A not-found read (core.ErrResourceUnreadable) resolves "destroyed"
//     -- but ONLY when priorPresentMatches is true, i.e. the immediately
//     preceding observation (this attempt's own precheck, or a prior
//     attempt's, via hist.lastReconciliationOutcome) confirmed the target
//     was present and matching before any attempt touched it. A bare
//     not-found read is otherwise inconclusive, not "already_absent" --
//     this package has no way to know whether the absence predates this
//     destroy attempt entirely.
//   - A successful read (the target is still reachable) resolves "failed"
//     -- presence itself, not a specific attribute's old value, is what
//     proves the call never landed (unlike a modify's reconciliation,
//     which compares specific before/after dot-paths). If the provider
//     had itself claimed success (applyClaimedSuccess), this is recorded
//     as "provider_reported_success_but_present" instead of a plain
//     "failed" -- a materially more serious finding (the provider lied),
//     not an ordinary ambiguous-RPC failure.
//   - Any other read failure is inconclusive, retried per
//     destroyReconcileBackoffSchedule.
//
// Exhausting the budget without a conclusive answer resolves
// still_unknown. Every attempt is appended to ra.Reconciliation; the final
// transition is always recorded before returning.
// applyClaimedSuccess (UBI-44) distinguishes, purely in what gets recorded,
// why this loop is running at all: true means ApplyResourceChange itself
// reported success (docs/executor.md's own amendment -- a clean response is
// never sufficient by itself, so this read-back is what actually earns the
// destroyed verdict); false means the RPC was merely ambiguous (a
// retryable error, or a resumed unresolved state folded from history). Both
// still resolve identically on a genuine not-found read (destroyed) or an
// exhausted budget (still_unknown) -- only the "still present" case's own
// wording differs, since "the provider actively lied" is a materially more
// serious finding than "the RPC hiccuped and we're not sure."
func reconcileDestroyLoop(ctx context.Context, app Applier, addr core.Address, lookup json.RawMessage, providerSource string, providerConfig json.RawMessage, priorPresentMatches, applyClaimedSuccess bool, ra *core.ResourceApply) core.ResourceState {
	for attempt := 0; attempt < len(destroyReconcileBackoffSchedule); attempt++ {
		// docs/cli-output-spec.md's own mandatory narration line: a
		// provider that already claimed success (the common, honest case
		// this backoff schedule exists for -- real eventual-consistency
		// lag, not a lie) gets the exact phrasing the spec names; any
		// other reconcile reason still narrates, just without implying a
		// claim that was never made.
		detail := "verifying via read-back"
		if applyClaimedSuccess {
			detail = "provider reported success -- verifying via read-back"
		}
		emitProgress(ctx, ProgressEvent{
			Address: addr.String(), Kind: "reconcile_attempt",
			Attempt: attempt + 1, Total: len(destroyReconcileBackoffSchedule),
			Detail: detail,
		})
		_, _, err := core.ReadAndFingerprint(ctx, app, addr, providerSource, providerConfig, lookup)
		at := nowRFC3339()
		switch {
		case err != nil && errors.Is(err, core.ErrResourceUnreadable):
			if priorPresentMatches {
				ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "destroyed"})
				recordTransition(ctx, ra, core.ResourceApplied, "confirmed by reconciliation")
				return core.ResourceApplied
			}
			ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{
				At: at, Outcome: "inconclusive",
				Detail: "target unreadable, but no prior confirmed-present observation to attribute it to this attempt",
			})
		case err != nil:
			ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "inconclusive", Detail: err.Error()})
		default:
			if !applyClaimedSuccess {
				// Unchanged: an ambiguous RPC result plus a present read is
				// immediately conclusive -- presence itself proves the call
				// never landed, no reason to wait further.
				ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: at, Outcome: "failed"})
				recordTransition(ctx, ra, core.ResourceFailed, "confirmed by reconciliation: destroy never landed")
				return core.ResourceFailed
			}
			// UBI-42/44: the provider itself claimed success, so a present
			// read isn't immediately conclusive the way it is above -- real
			// eventual-consistency lag (SQS's own ~60s figure) can look
			// identical to a lie until the budget's own tail is reached.
			// Only the final attempt, still present, earns the definitive
			// "the provider lied" verdict; every earlier one just retries.
			if attempt == len(destroyReconcileBackoffSchedule)-1 {
				detail := "the provider reported a successful destroy, but a post-destroy read-back found the resource still present after the full retry budget -- the delete never actually happened"
				ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{
					At: at, Outcome: "provider_reported_success_but_present",
					Detail: detail,
				})
				// Unlike the ambiguous-RPC path (whose own recordError already
				// ran in shipDestroyNode before this loop), Apply itself
				// reported no error here -- record one now, so ubx ship's
				// human report (printShipReport) shows *why*, not just "failed"
				// with nothing underneath it.
				recordError(ctx, ra, detail, core.ErrorTerminal)
				recordTransition(ctx, ra, core.ResourceFailed, "provider reported success but a post-destroy read-back found the resource still present")
				return core.ResourceFailed
			}
			ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{
				At: at, Outcome: "inconclusive",
				Detail: "still present after a reported successful destroy -- retrying to allow for real propagation lag before concluding the provider's claim was false",
			})
		}
		time.Sleep(destroyReconcileBackoffSchedule[attempt])
	}
	recordTransition(ctx, ra, core.ResourceStillUnknown, "reconciliation exhausted its retry budget without a conclusive answer")
	return core.ResourceStillUnknown
}

// destroyTargetFor looks up entry's own resolution.inputs["destroy_target"]
// -- the lookup key and observed_hash core.Validate already requires be
// present for every delta.destroys entry (docs/schema.md's "Amendment:
// destroys"), never derived at need-time.
func destroyTargetFor(p *core.Proposal, addr core.Address) (lookup json.RawMessage, observedHash string, ok bool) {
	target := addr.String()
	for _, in := range p.Resolution.Inputs {
		if in.Kind == "destroy_target" && in.Resource == target {
			return in.Lookup, in.ObservedHash, len(in.Lookup) > 0
		}
	}
	return nil, "", false
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

// redactedAfterPaths returns every dot-path in mod.After whose value is a
// $redacted marker (core.IsRedactedValue) -- sorted, for a deterministic
// error message. A drift_revert whose restore target is itself redacted
// can never be shipped automatically (docs/executor.md, docs/schema.md's
// apply-record amendment): the ledger never held the real material to
// begin with.
func redactedAfterPaths(mod core.Modification) []string {
	var paths []string
	for path, raw := range mod.After {
		if core.IsRedactedValue(raw) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func recordTransition(ctx context.Context, ra *core.ResourceApply, state core.ResourceState, detail string) {
	ra.Transitions = append(ra.Transitions, core.Transition{State: state, At: nowRFC3339(), Detail: detail})
	emitProgress(ctx, ProgressEvent{Address: ra.Address.String(), Kind: "transition", State: string(state), Detail: detail})
}

func recordError(ctx context.Context, ra *core.ResourceApply, message string, class core.ErrorClassification) {
	ra.Errors = append(ra.Errors, core.ErrorRecord{At: nowRFC3339(), Message: message, Classification: class})
	emitProgress(ctx, ProgressEvent{Address: ra.Address.String(), Kind: "error", Detail: message})
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
