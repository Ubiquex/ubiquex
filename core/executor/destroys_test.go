package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// --- happy path -----------------------------------------------------------

func TestShipDestroy_CleanApply(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied", st)
	}
	if len(ra.Reconciliation) != 2 {
		t.Fatalf("reconciliation = %+v, want exactly 2 entries (present_matches precheck, destroyed confirmation)", ra.Reconciliation)
	}
	if ra.Reconciliation[0].Outcome != "present_matches" {
		t.Fatalf("reconciliation[0] = %s, want present_matches", ra.Reconciliation[0].Outcome)
	}
	if ra.Reconciliation[1].Outcome != "destroyed" {
		t.Fatalf("reconciliation[1] = %s, want destroyed", ra.Reconciliation[1].Outcome)
	}
	if _, exists := fake.resources[addr.String()]; exists {
		t.Fatal("resource still present in the fake provider after a clean destroy")
	}

	// Idempotency: re-running ship must be a genuine no-op.
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("re-run: got %v, want ErrAlreadyApplied", err)
	}
}

// --- row 1: destroy target drifted since acceptance ------------------------

func TestShipDestroy_TargetDriftedSinceAcceptance_Refused(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	// A real, independent change since the destroy was signed away.
	fake.setState(addr.String(), fakeState(addr, "v2-drifted"))

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "failed" {
		t.Fatalf("outcome = %s, want failed", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourcePending {
		t.Fatalf("last state = %s, want pending -- refused before ever reaching in_flight", st)
	}
	if len(ra.Reconciliation) != 1 || ra.Reconciliation[0].Outcome != "present_drifted" {
		t.Fatalf("reconciliation = %+v, want exactly one present_drifted entry", ra.Reconciliation)
	}
	if len(ra.Errors) == 0 || ra.Errors[0].Classification != core.ErrorTerminal {
		t.Fatalf("errors = %+v, want a terminal classification", ra.Errors)
	}
	if _, exists := fake.resources[addr.String()]; !exists {
		t.Fatal("resource must still exist -- a drifted destroy target is refused, never destroyed")
	}
}

// TestShipDestroy_NormalizationNoise_NotRefused is UBI-63 session 5's own
// hermetic repro of a real, live divergence blocking the founder's actual
// cleanup: `ubx scan` on a role/policy reported clean seconds before `ubx
// terminate` on the EXACT SAME, untouched resources refused with "destroy
// target drifted since it was signed away" -- same resource, same real
// state, back to back, nothing touched in between. Root cause: this
// precheck compared core.ReadAndFingerprint's raw ObservedHash directly,
// a pipeline that never applies fix 1's own normalization (which lives
// entirely inside core.RunScan) -- so a pure null<->zero-value
// representation flip (real SDKv2-vintage provider read
// nondeterminism, the same "bug 4" shape as ever) that scan correctly
// waves through as "no drift" tripped this raw comparison instead.
// destroyDiffExplainedByNormalization now applies the SAME
// core.FilterNormalizationNoise filter RunScan's own verdict already
// does, so this reproduces the fix, not the bug.
func TestShipDestroy_NormalizationNoise_NotRefused(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "role-1"}

	// Adopted with tags recorded as null -- a real provider's own
	// create-time response can leave an unset Optional map attribute
	// null rather than {}.
	recorded := json.RawMessage(fmt.Sprintf(`{"id":%q,"name":"role-1","tags":null}`, addr.String()))
	fake.setState(addr.String(), recorded)
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil || res.Outcome != core.ScanNew {
		t.Fatalf("adopt: scan = %+v, err = %v", res, err)
	}
	p, err := core.GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatal(err)
	}

	// Nothing touches this resource -- but a later, separate read
	// returns tags:{} instead of null: pure read nondeterminism, not a
	// real change.
	fake.setState(addr.String(), json.RawMessage(fmt.Sprintf(`{"id":%q,"name":"role-1","tags":{}}`, addr.String())))

	// Confirm scan itself still says clean (fix 1) -- the baseline this
	// whole repro depends on.
	scanRes, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if scanRes.Outcome != core.ScanUnchanged {
		t.Fatalf("scan outcome = %v, want ScanUnchanged -- this is pure normalization noise", scanRes.Outcome)
	}

	// THE divergence: terminate on the exact same, untouched resource
	// must NOT be refused as drifted.
	state, found, err := l.FoldState(addr)
	if err != nil || !found {
		t.Fatalf("fold state: found=%v err=%v", found, err)
	}
	entry := core.DestroyEntry{Address: addr, State: state}
	destroyProposal := acceptDestroy(t, l, "payments", []core.DestroyEntry{entry}, []core.ResolutionInput{destroyTargetInput(t, addr, state)})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", destroyProposal)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied -- scan already confirmed this is normalization noise, not real drift", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if len(ra.Reconciliation) != 2 || ra.Reconciliation[0].Outcome != "present_matches" || ra.Reconciliation[1].Outcome != "destroyed" {
		t.Fatalf("reconciliation = %+v, want present_matches then destroyed", ra.Reconciliation)
	}
	if _, exists := fake.resources[addr.String()]; exists {
		t.Fatal("resource still present after what should have been a clean destroy")
	}
}

// --- row 2: kill -9 mid-destroy, before the call ----------------------------

func TestShipDestroy_KillBeforeCall_NeverLanded_RetriedNormally(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	ra := &core.ResourceApply{Address: addr}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(context.Background(), ra, core.ResourcePending, "")
	recordTransition(context.Background(), ra, core.ResourceInFlight, "") // exactly what a crash right before the call leaves behind -- no precheck reconciliation entry at all
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// Attempt 1 is left unsealed forever. Live state is unchanged -- the
	// call genuinely never happened.

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}
	gotRA := sealed.Resources[0]
	if st, _ := gotRA.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	if len(gotRA.Reconciliation) == 0 || gotRA.Reconciliation[0].Outcome != "failed" {
		t.Fatalf("reconciliation = %+v, want its first attempt to conclude failed (the call never happened)", gotRA.Reconciliation)
	}
	if _, exists := fake.resources[addr.String()]; !exists {
		t.Fatal("resource must still exist -- the call never actually happened")
	}
}

// --- row 3: kill -9 mid-destroy, after the call -----------------------------

func TestShipDestroy_KillAfterCall_AlreadyLanded_ResolvesDestroyed(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	ra := &core.ResourceApply{Address: addr}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(context.Background(), ra, core.ResourcePending, "")
	// The pre-attempt precheck actually ran and confirmed present_matches,
	// exactly as a real attempt 1 would have recorded before in_flight.
	ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "present_matches"})
	recordTransition(context.Background(), ra, core.ResourceInFlight, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// The real ApplyResourceChange call succeeded server-side before ubx
	// died -- the resource is genuinely already gone.
	fake.deleteState(addr.String())

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}
	gotRA := sealed.Resources[0]
	if st, _ := gotRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied -- must never be re-attempted once reconciliation confirms it already landed", st)
	}
	if len(gotRA.Reconciliation) == 0 || gotRA.Reconciliation[0].Outcome != "destroyed" {
		t.Fatalf("reconciliation = %+v, want destroyed -- disambiguated via attempt 1's own present_matches, folded across the parent chain", gotRA.Reconciliation)
	}
}

// --- row 4: timeout where the destroy actually landed -----------------------

func TestShipDestroy_TimeoutWhereLanded_ResolvesDestroyed(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)
	fake.scriptDestroyOutcome(addr.String(), context.DeadlineExceeded, true /* destroyLanded */)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied", st)
	}
	last := ra.Reconciliation[len(ra.Reconciliation)-1]
	if last.Outcome != "destroyed" {
		t.Fatalf("final reconciliation outcome = %s, want destroyed", last.Outcome)
	}
}

// --- row 5: timeout where it did not land -----------------------------------

func TestShipDestroy_TimeoutWhereNotLanded_ResolvesFailed(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)
	fake.scriptDestroyOutcome(addr.String(), context.DeadlineExceeded, false /* destroyLanded */)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	last := ra.Reconciliation[len(ra.Reconciliation)-1]
	if last.Outcome != "failed" {
		t.Fatalf("final reconciliation outcome = %s, want failed", last.Outcome)
	}
	if _, exists := fake.resources[addr.String()]; !exists {
		t.Fatal("resource must still exist -- the destroy never actually landed")
	}

	// Retried normally on the next ubx ship invocation.
	sealed2, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("retry ship: %v", err)
	}
	if st, _ := sealed2.Resources[0].LastState(); st != core.ResourceApplied {
		t.Fatalf("retry last state = %s, want applied", st)
	}
}

// --- row 6: destroy of an already-absent resource ---------------------------

func TestShipDestroy_AlreadyAbsent_ResolvesWithoutInFlight(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)
	// Removed out-of-band before ubx ever attempted anything.
	fake.deleteState(addr.String())

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied", st)
	}
	for _, tr := range ra.Transitions {
		if tr.State == core.ResourceInFlight {
			t.Fatal("an already-absent destroy target must never reach in_flight")
		}
	}
	if len(ra.Reconciliation) != 1 || ra.Reconciliation[0].Outcome != "already_absent" {
		t.Fatalf("reconciliation = %+v, want exactly one already_absent entry", ra.Reconciliation)
	}
}

// --- row 8: mixed create+destroy proposal ordering --------------------------

// TestShipDestroy_MixedProposal_ReversedOrder proves docs/executor.md's own
// "one combined topological walk, not two" claim directly: one proposal
// creates a replacement resource, repoints a dependent modify at it, and
// destroys the original -- all three interleaved via depends_on, never
// three separately-ordered phases. Order is observed through the fake's own
// side effects (a create assigns a fresh id; a destroy removes the address
// from fake.resources), not just the returned node order.
func TestShipDestroy_MixedProposal_ReversedOrder(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	originalAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "original"}
	dependentAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "dependent"}
	newAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "new"}

	adoptFake(t, l, fake, originalAddr, "original-v1")
	adoptFake(t, l, fake, dependentAddr, "points-at-original")

	originalState, _, err := l.FoldState(originalAddr)
	if err != nil {
		t.Fatalf("fold original: %v", err)
	}

	// create(new): no dependency.
	createNew := changeCreateJSON(t, newAddr, `{"value":"new-resource"}`)
	// modify(dependent): depends_on create(new) -- must repoint only after
	// the replacement exists.
	dependentMod := core.Modification{
		Target:    dependentAddr,
		After:     map[string]json.RawMessage{"value": json.RawMessage(`"points-at-new"`)},
		DependsOn: []string{newAddr.String()},
	}
	// destroy(original): depends_on modify(dependent) -- the reverse edge:
	// only safe once the dependent has repointed away.
	destroyOriginal := core.DestroyEntry{
		Address:   originalAddr,
		State:     originalState,
		DependsOn: []string{dependentAddr.String()},
	}

	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "replace original with new"},
		Delta: core.Delta{
			Creates:  []json.RawMessage{createNew},
			Modifies: []core.Modification{dependentMod},
			Destroys: []core.DestroyEntry{destroyOriginal},
		},
		Resolution: core.Resolution{
			ResolvedAt: nowRFC3339(),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: dependentAddr.String(), ObservedHash: mustHash(t, mustFold(t, l, dependentAddr)), Lookup: lookupJSON(dependentAddr)},
				destroyTargetInput(t, originalAddr, originalState),
			},
		},
		CostDelta:   core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{Creates: 1, Modifies: 1, Destroys: 1},
		Status:      core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", accepted)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied: %+v", sealed.Summary.Outcome, sealed.Resources)
	}

	// The combined walk must have executed create(new) and modify(dependent)
	// BEFORE destroy(original) -- verified by the fact all three succeeded
	// at all: destroy(original) would have been blocked (missing dependency)
	// had modify(dependent) not already completed, and modify(dependent)
	// would have been blocked had create(new) not already completed.
	for _, ra := range sealed.Resources {
		if st, _ := ra.LastState(); st != core.ResourceApplied {
			t.Fatalf("resource %s did not apply: %+v", ra.Address, ra)
		}
	}
	if _, exists := fake.resources[originalAddr.String()]; exists {
		t.Fatal("original must be destroyed")
	}
	if _, exists := fake.resources[dependentAddr.String()]; !exists {
		t.Fatal("dependent must still exist, only modified")
	}
}

// shrinkDestroyReconcileBackoff swaps in a short, fixed-shape backoff
// schedule for the duration of one test (same convention as
// core's own lockWaitTimeout/lockRetryInterval seams) -- the real
// production schedule spans ~64 real seconds to reach AWS's own
// documented ~60-second SQS deletion-visibility lag (UBI-42); a hermetic
// test proving the retry mechanism itself needs none of that real wall-
// clock time.
func shrinkDestroyReconcileBackoff(t *testing.T) {
	t.Helper()
	orig := destroyReconcileBackoffSchedule
	destroyReconcileBackoffSchedule = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { destroyReconcileBackoffSchedule = orig })
}

// --- UBI-44: a clean ApplyResourceChange response is never sufficient ------

// TestShipDestroy_LyingApplySuccess_NeverResolvesDestroyed proves
// docs/executor.md's own UBI-44 amendment directly: a provider that
// reports a clean destroy success (nil error, the correct "null"
// NewState) while never actually deleting the resource -- the exact shape
// found live against a real google_pubsub_topic, confirmed via Cloud Audit
// Logs showing zero real DeleteTopic calls -- must never resolve
// "destroyed". The post-destroy read-back (universal now, not just after
// an ambiguous Apply) catches it every time.
func TestShipDestroy_LyingApplySuccess_NeverResolvesDestroyed(t *testing.T) {
	shrinkDestroyReconcileBackoff(t) // a permanent lie burns the whole budget every time -- keep it hermetic-fast
	l, fake, addr, p := singleResourceDestroy(t)
	fake.scriptLyingDestroy(addr.String())

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "failed" {
		t.Fatalf("outcome = %s, want failed -- a lying provider must never produce a clean applied summary", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	last := ra.Reconciliation[len(ra.Reconciliation)-1]
	if last.Outcome != "provider_reported_success_but_present" {
		t.Fatalf("final reconciliation outcome = %s, want provider_reported_success_but_present -- a materially more serious finding than an ordinary ambiguous failure", last.Outcome)
	}
	if _, exists := fake.resources[addr.String()]; !exists {
		t.Fatal("resource must still exist -- the provider's own claimed success was never real")
	}

	// The resource is still present and still matches -- a re-run must
	// retry the destroy normally, exactly like any other failed attempt,
	// never treat the false "destroyed" claim as final.
	fake.scriptLyingDestroy(addr.String()) // the lie persists on a genuine, unfixed provider
	sealed2, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("retry ship: %v", err)
	}
	if st, _ := sealed2.Resources[0].LastState(); st != core.ResourceFailed {
		t.Fatalf("retry last state = %s, want failed again -- still lying", st)
	}
}

// TestShipDestroy_CleanApply_NoUnnecessaryRetries proves the universal
// read-back's own cost claim: a synchronously-consistent provider (the
// ordinary, honest case -- confirmed live for GCP Pub/Sub, UBI-44) still
// resolves "destroyed" on the very first post-destroy read, with no added
// retries or sleeps, so the fix's own cost is paid only by a genuine slow
// or lying tail, never by the common case. TestShipDestroy_CleanApply
// already asserts the exact reconciliation shape; this test asserts the
// same thing framed explicitly around the new universal path's own cost.
func TestShipDestroy_CleanApply_NoUnnecessaryRetries(t *testing.T) {
	l, fake, _, p := singleResourceDestroy(t)

	start := time.Now()
	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("ship took %s -- the honest, synchronously-consistent case must resolve on the very first read-back, no backoff sleep", elapsed)
	}
	ra := sealed.Resources[0]
	if len(ra.Reconciliation) != 2 {
		t.Fatalf("reconciliation = %+v, want exactly 2 entries (present_matches precheck, destroyed confirmation) -- no extra retries", ra.Reconciliation)
	}
}

// TestShipDestroy_DelayedAbsence_ResolvesDestroyed proves UBI-42's own
// co-scoped fix: a destroy that genuinely lands, but whose absence isn't
// visible on the read side for a few real reads (bounded, eventual-
// consistency lag -- SQS's own ~60-second figure, UBI-30, is the real
// motivating case), still correctly resolves "destroyed" once
// destroyReconcileBackoffSchedule's own retries reach it, rather than
// timing out into a false "still_unknown"/"failed" the way the
// pre-UBI-42 100ms budget would have.
func TestShipDestroy_DelayedAbsence_ResolvesDestroyed(t *testing.T) {
	shrinkDestroyReconcileBackoff(t)
	l, fake, addr, p := singleResourceDestroy(t)
	fake.scriptDelayedAbsence(addr.String(), 3) // present for 3 more reads, then gone

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied -- a genuine, if delayed, destroy must still resolve destroyed", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied", st)
	}
	last := ra.Reconciliation[len(ra.Reconciliation)-1]
	if last.Outcome != "destroyed" {
		t.Fatalf("final reconciliation outcome = %s, want destroyed", last.Outcome)
	}
	// present_matches precheck, 3 inconclusive-but-present retries, destroyed.
	if len(ra.Reconciliation) != 5 {
		t.Fatalf("reconciliation = %+v, want exactly 5 entries -- the delayed reads must actually have been retried, not skipped or short-circuited", ra.Reconciliation)
	}
}

func mustFold(t *testing.T, l *core.Ledger, addr core.Address) json.RawMessage {
	t.Helper()
	state, found, err := l.FoldState(addr)
	if err != nil || !found {
		t.Fatalf("fold %s: found=%v err=%v", addr, found, err)
	}
	return state
}

func mustHash(t *testing.T, state json.RawMessage) string {
	t.Helper()
	h, err := core.ObservedHash(state)
	if err != nil {
		t.Fatalf("observed hash: %v", err)
	}
	return h
}

// --- row 10: re-ship after partial destroy ----------------------------------

// TestShipDestroy_ReShipAfterPartialDestroy proves the existing
// partially_applied/idempotency machinery, unchanged, correctly covers a
// mixed destroy proposal too: one resource destroys cleanly, the process
// is killed before the second (dependency-ordered) resource's own turn
// begins, and a re-run resumes correctly -- never re-attempting the first,
// never skipping the second.
func TestShipDestroy_ReShipAfterPartialDestroy(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	firstAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "first"}
	secondAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "second"}
	adoptFake(t, l, fake, firstAddr, "v1")
	adoptFake(t, l, fake, secondAddr, "v1")
	firstState := mustFold(t, l, firstAddr)
	secondState := mustFold(t, l, secondAddr)

	// second depends on first (mutual destroy ordering): first must go
	// last is wrong here -- pick first depends on nothing, second
	// depends_on first, so first ships before second.
	destroys := []core.DestroyEntry{
		{Address: firstAddr, State: firstState},
		{Address: secondAddr, State: secondState, DependsOn: []string{firstAddr.String()}},
	}
	inputs := []core.ResolutionInput{
		destroyTargetInput(t, firstAddr, firstState),
		destroyTargetInput(t, secondAddr, secondState),
	}
	p := acceptDestroy(t, l, "payments", destroys, inputs)

	// Simulate: first destroyed cleanly for real (via a real Ship call),
	// but the process was killed before second's own turn even began --
	// modeled directly, since Ship() itself always runs to completion in a
	// single test process; the on-disk shape left behind is identical to a
	// real kill at that exact point.
	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	firstRA := &core.ResourceApply{Address: firstAddr}
	rec.Resources = append(rec.Resources, firstRA)
	recordTransition(context.Background(), firstRA, core.ResourcePending, "")
	firstRA.Reconciliation = append(firstRA.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "present_matches"})
	recordTransition(context.Background(), firstRA, core.ResourceInFlight, "")
	firstRA.Reconciliation = append(firstRA.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "destroyed"})
	recordTransition(context.Background(), firstRA, core.ResourceApplied, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	fake.deleteState(firstAddr.String()) // first's own destroy really landed

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}

	var firstFinal, secondFinal *core.ResourceApply
	for _, ra := range sealed.Resources {
		switch ra.Address {
		case firstAddr:
			firstFinal = ra
		case secondAddr:
			secondFinal = ra
		}
	}
	if firstFinal == nil || secondFinal == nil {
		t.Fatalf("expected both resources represented in attempt 2, got %+v", sealed.Resources)
	}
	if st, _ := firstFinal.LastState(); st != core.ResourceApplied {
		t.Fatalf("first state = %s, want applied (already done, never re-attempted)", st)
	}
	if len(firstFinal.Transitions) != 1 {
		t.Fatalf("first should show only a pass-through confirmation in attempt 2, got %+v", firstFinal.Transitions)
	}
	if st, _ := secondFinal.LastState(); st != core.ResourceApplied {
		t.Fatalf("second state = %s, want applied -- resumed correctly, not skipped", st)
	}
	if _, exists := fake.resources[secondAddr.String()]; exists {
		t.Fatal("second must actually be destroyed for real in this re-run")
	}
}
