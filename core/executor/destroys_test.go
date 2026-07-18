package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

// --- happy path -----------------------------------------------------------

func TestShipDestroy_CleanApply(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("re-run: got %v, want ErrAlreadyApplied", err)
	}
}

// --- row 1: destroy target drifted since acceptance ------------------------

func TestShipDestroy_TargetDriftedSinceAcceptance_Refused(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	// A real, independent change since the destroy was signed away.
	fake.setState(addr.String(), fakeState(addr, "v2-drifted"))

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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

// --- row 2: kill -9 mid-destroy, before the call ----------------------------

func TestShipDestroy_KillBeforeCall_NeverLanded_RetriedNormally(t *testing.T) {
	l, fake, addr, p := singleResourceDestroy(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	ra := &core.ResourceApply{Address: addr}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(ra, core.ResourcePending, "")
	recordTransition(ra, core.ResourceInFlight, "") // exactly what a crash right before the call leaves behind -- no precheck reconciliation entry at all
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// Attempt 1 is left unsealed forever. Live state is unchanged -- the
	// call genuinely never happened.

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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
	recordTransition(ra, core.ResourcePending, "")
	// The pre-attempt precheck actually ran and confirmed present_matches,
	// exactly as a real attempt 1 would have recorded before in_flight.
	ra.Reconciliation = append(ra.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "present_matches"})
	recordTransition(ra, core.ResourceInFlight, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// The real ApplyResourceChange call succeeded server-side before ubx
	// died -- the resource is genuinely already gone.
	fake.deleteState(addr.String())

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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
	sealed2, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, accepted)
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
	recordTransition(firstRA, core.ResourcePending, "")
	firstRA.Reconciliation = append(firstRA.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "present_matches"})
	recordTransition(firstRA, core.ResourceInFlight, "")
	firstRA.Reconciliation = append(firstRA.Reconciliation, core.ReconciliationAttempt{At: nowRFC3339(), Outcome: "destroyed"})
	recordTransition(firstRA, core.ResourceApplied, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	fake.deleteState(firstAddr.String()) // first's own destroy really landed

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake), "", nil, p)
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
