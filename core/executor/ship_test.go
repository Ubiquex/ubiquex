package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// --- fake Applier -----------------------------------------------------

// fakeApplier is a hermetic, in-memory Applier: resources are keyed by
// their own "id" attribute (present in every observed/planned state this
// test package constructs), never by argument threading, mirroring how a
// real tfplugin provider identifies a resource from within its own state
// rather than a side-channel parameter.
type fakeApplier struct {
	mu        sync.Mutex
	resources map[string]json.RawMessage
	scripts   map[string][]applyStep
}

type applyStep struct {
	err     error           // if set, ApplyResourceChange returns this error
	landsAs json.RawMessage // if set, the resource's live state is mutated to this value as a side effect, even though err is also returned -- simulates a provider that committed the change server-side despite the RPC itself failing/timing out
}

func newFakeApplier() *fakeApplier {
	return &fakeApplier{resources: map[string]json.RawMessage{}, scripts: map[string][]applyStep{}}
}

func (f *fakeApplier) Schema(ctx context.Context) (any, map[string]any, error) {
	return struct{}{}, map[string]any{"fake_widget": struct{}{}}, nil
}

func (f *fakeApplier) Configure(ctx context.Context, providerSchema any, config json.RawMessage) error {
	return nil
}

func (f *fakeApplier) ReadResource(ctx context.Context, resourceSchema any, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := extractID(currentState)
	if !ok {
		return nil, fmt.Errorf("fake: currentState has no id: %s", currentState)
	}
	state, ok := f.resources[id]
	if !ok {
		return nil, nil
	}
	cp := make(json.RawMessage, len(state))
	copy(cp, state)
	return cp, nil
}

func (f *fakeApplier) ApplyResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, plannedState json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := extractID(plannedState)
	if !ok {
		return nil, fmt.Errorf("fake: plannedState has no id: %s", plannedState)
	}
	if steps, ok := f.scripts[id]; ok && len(steps) > 0 {
		step := steps[0]
		f.scripts[id] = steps[1:]
		if step.landsAs != nil {
			f.resources[id] = step.landsAs
		}
		if step.err != nil {
			return nil, step.err
		}
	}
	f.resources[id] = plannedState
	return plannedState, nil
}

func (f *fakeApplier) setState(id string, state json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resources[id] = state
}

func (f *fakeApplier) scriptApplyError(id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{err: err})
}

// scriptApplyTimeoutButLanded schedules the next ApplyResourceChange call
// for id to return err while ALSO landing landsAs server-side as a side
// effect -- simulating a provider that committed a change before/while the
// RPC itself failed or timed out from ubx's own point of view (rows 1/2 of
// docs/executor-adversarial.md).
func (f *fakeApplier) scriptApplyTimeoutButLanded(id string, err error, landsAs json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{err: err, landsAs: landsAs})
}

func extractID(state json.RawMessage) (string, bool) {
	var m map[string]interface{}
	if err := json.Unmarshal(state, &m); err != nil {
		return "", false
	}
	id, ok := m["id"].(string)
	return id, ok
}

// --- fixture construction, via the real scan/generate/accept pipeline --

func lookupJSON(addr core.Address) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q}`, addr.String()))
}

func fakeState(addr core.Address, value string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"value":%q}`, addr.String(), value))
}

// adoptFake seeds fake with addr's initial state and adopts it into l via
// the real RunScan -> GenerateProposal -> Accept pipeline (never
// hand-constructed), so every fixture in this file exercises exactly the
// pipeline a real ubx scan/accept would.
func adoptFake(t *testing.T, l *core.Ledger, fake *fakeApplier, addr core.Address, value string) {
	t.Helper()
	fake.setState(addr.String(), fakeState(addr, value))
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("adopt %s: scan: %v", addr, err)
	}
	if res.Outcome != core.ScanNew {
		t.Fatalf("adopt %s: expected ScanNew, got %v", addr, res.Outcome)
	}
	p, err := core.GenerateProposal(l, addr.Stack, res)
	if err != nil {
		t.Fatalf("adopt %s: generate: %v", addr, err)
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("adopt %s: accept: %v", addr, err)
	}
}

// driftFake mutates addr's live state out-of-band (simulating a real
// change outside ubx) and returns the ScanResult confirming drift.
func driftFake(t *testing.T, l *core.Ledger, fake *fakeApplier, addr core.Address, newValue string) *core.ScanResult {
	t.Helper()
	fake.setState(addr.String(), fakeState(addr, newValue))
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("drift %s: scan: %v", addr, err)
	}
	if res.Outcome != core.ScanDrifted {
		t.Fatalf("drift %s: expected ScanDrifted, got %v", addr, res.Outcome)
	}
	return res
}

// acceptRevert builds and accepts one combined drift_revert proposal
// covering every given (already-drifted) scan result -- merging
// GenerateRevertProposal's own single-resource output, the same way a
// resolver merging independent reverts into one proposal would (legal per
// docs/schema.md: "at least one delta.modifies entry," no upper bound).
func acceptRevert(t *testing.T, l *core.Ledger, stack string, results ...*core.ScanResult) *core.Proposal {
	t.Helper()
	var modifies []core.Modification
	var inputs []core.ResolutionInput
	for _, res := range results {
		p, err := core.GenerateRevertProposal(l, stack, res)
		if err != nil {
			t.Fatalf("generate revert: %v", err)
		}
		modifies = append(modifies, p.Delta.Modifies...)
		inputs = append(inputs, p.Resolution.Inputs...)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	combined := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Kind:          core.KindDriftRevert,
		Intent:        core.Intent{Summary: "test revert"},
		Delta:         core.Delta{Modifies: modifies},
		Resolution:    core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: inputs},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   core.BlastRadius{Modifies: int64(len(modifies))},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, combined)
	if err != nil {
		t.Fatalf("accept revert: %v", err)
	}
	return accepted
}

// singleResourceRevert is the common one-resource fixture: adopted at
// "v1", drifted to "v2", accepted drift_revert restoring back to "v1".
func singleResourceRevert(t *testing.T) (*core.Ledger, *fakeApplier, core.Address, *core.Proposal) {
	t.Helper()
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "api-cache"}
	adoptFake(t, l, fake, addr, "v1")
	res := driftFake(t, l, fake, addr, "v2")
	p := acceptRevert(t, l, "payments", res)
	return l, fake, addr, p
}

// --- happy path ---------------------------------------------------------

func TestShip_SingleResource_AppliesCleanly(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if !sealed.Sealed() {
		t.Fatal("expected a sealed record")
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %q, want applied", sealed.Summary.Outcome)
	}
	if sealed.Summary.ResourcesApplied != 1 {
		t.Fatalf("resources_applied = %d, want 1", sealed.Summary.ResourcesApplied)
	}
	if sealed.Attempt != 1 || sealed.Parent != "" {
		t.Fatalf("attempt = %d parent = %q, want 1/\"\"", sealed.Attempt, sealed.Parent)
	}

	live, err := fake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addr))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(live, &m)
	if m["value"] != "v1" {
		t.Fatalf("live value = %v, want v1 (restored)", m["value"])
	}
}

func TestShip_MultiResource_SerialDeltaOrder(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addrB := core.Address{Stack: "payments", Type: "fake_widget", Name: "b"}
	addrA := core.Address{Stack: "payments", Type: "fake_widget", Name: "a"}
	// Adopt/drift in reverse-alphabetical order on purpose -- Ship must
	// still execute in canonical (stack, type, name) order regardless of
	// the order proposals/modifies were built in.
	adoptFake(t, l, fake, addrB, "v1")
	adoptFake(t, l, fake, addrA, "v1")
	resB := driftFake(t, l, fake, addrB, "v2")
	resA := driftFake(t, l, fake, addrA, "v2")
	p := acceptRevert(t, l, "payments", resB, resA)

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" || sealed.Summary.ResourcesApplied != 2 {
		t.Fatalf("summary = %+v", sealed.Summary)
	}
	if len(sealed.Resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(sealed.Resources))
	}
	if sealed.Resources[0].Address.Name != "a" || sealed.Resources[1].Address.Name != "b" {
		t.Fatalf("execution order = %s, %s -- want a, b (canonical order)",
			sealed.Resources[0].Address.Name, sealed.Resources[1].Address.Name)
	}
}

// --- preconditions -------------------------------------------------------

func TestShip_RejectsUnsupportedKind(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "api-cache"}
	adoptFake(t, l, fake, addr, "v1")
	res := driftFake(t, l, fake, addr, "v2")
	p, err := core.GenerateProposal(l, "payments", res) // drift_adopt, not drift_revert
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := Ship(context.Background(), l, fake, "", nil, accepted); !errors.Is(err, ErrUnsupportedKind) {
		t.Fatalf("err = %v, want ErrUnsupportedKind", err)
	}
}

func TestShip_RejectsUnacceptedProposal(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "api-cache"}
	adoptFake(t, l, fake, addr, "v1")
	res := driftFake(t, l, fake, addr, "v2")
	p, err := core.GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Deliberately never accepted -- p.Acceptance is nil.
	if _, err := Ship(context.Background(), l, fake, "", nil, p); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("err = %v, want ErrNotAccepted", err)
	}
}

// --- idempotency ---------------------------------------------------------

func TestShip_AlreadyFullyApplied_IsANoOp(t *testing.T) {
	l, fake, _, p := singleResourceRevert(t)
	if _, err := Ship(context.Background(), l, fake, "", nil, p); err != nil {
		t.Fatalf("first ship: %v", err)
	}
	if _, err := Ship(context.Background(), l, fake, "", nil, p); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("second ship: err = %v, want ErrAlreadyApplied", err)
	}
	attempts, err := l.ApplyAttempts(p.ID)
	if err != nil {
		t.Fatalf("apply attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (no new attempt written for a no-op)", len(attempts))
	}
}

// --- idempotency: per-resource retry budget (docs/executor.md) ----------

func TestShip_RetryBudgetExhausted_RefusesFurtherAttempts(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)

	for i := 0; i < maxApplyAttemptsPerResource; i++ {
		fake.scriptApplyError(addr.String(), &TerminalError{Err: errors.New("invalid attribute value")})
		sealed, err := Ship(context.Background(), l, fake, "", nil, p)
		if err != nil {
			t.Fatalf("ship attempt %d: %v", i+1, err)
		}
		if st, _ := sealed.Resources[0].LastState(); st != core.ResourceFailed {
			t.Fatalf("attempt %d: last state = %s, want failed", i+1, st)
		}
	}

	// The budget is now exhausted -- a further ship must refuse to issue
	// another ApplyResourceChange call at all, rather than retry forever.
	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship after budget exhausted: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourcePending {
		t.Fatalf("last state = %s, want pending (refused before any call)", st)
	}
	foundBudgetError := false
	for _, e := range ra.Errors {
		if e.Classification == core.ErrorTerminal {
			foundBudgetError = true
		}
	}
	if !foundBudgetError {
		t.Fatalf("errors = %+v, want a terminal retry-budget error", ra.Errors)
	}
}

// --- docs/executor-adversarial.md row 6a/6b: error taxonomy -------------

func TestShip_RetryableError_TriggersReconciliation_ResolvesFailed(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	fake.scriptApplyError(addr.String(), errors.New("connection reset"))
	// Live state stays at the drifted value ("v2") -- the change never landed.

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if len(sealed.Resources) != 1 {
		t.Fatalf("resources = %d", len(sealed.Resources))
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	if len(ra.Reconciliation) == 0 {
		t.Fatal("expected reconciliation to have run for a retryable error")
	}
	if sealed.Summary.Outcome != "failed" {
		t.Fatalf("outcome = %q, want failed", sealed.Summary.Outcome)
	}
}

func TestShip_TerminalError_FailsImmediately_NoReconciliation(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	fake.scriptApplyError(addr.String(), &TerminalError{Err: errors.New("invalid attribute value")})

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	if len(ra.Reconciliation) != 0 {
		t.Fatalf("reconciliation = %+v, want none -- a terminal error must never be reconciled/retried within this attempt", ra.Reconciliation)
	}
	if len(ra.Errors) != 1 || ra.Errors[0].Classification != core.ErrorTerminal {
		t.Fatalf("errors = %+v, want exactly one terminal error", ra.Errors)
	}
}

// row 2/3: timeout where the change did/didn't land, resolved via
// reconciliation trusting live reality over the RPC's own apparent outcome.

func TestShip_TimeoutWhereChangeLanded_ResolvesApplied(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	// The provider commits the restore server-side but the RPC itself
	// times out from ubx's own point of view -- reality has already moved
	// by the time reconciliation gets a chance to look.
	fake.scriptApplyTimeoutButLanded(addr.String(), context.DeadlineExceeded, fakeState(addr, "v1"))

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied (reconciliation must trust live reality over the timeout)", st)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %q, want applied", sealed.Summary.Outcome)
	}
}

func TestShip_TimeoutWhereChangeDidNotLand_ResolvesFailed(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	fake.scriptApplyError(addr.String(), context.DeadlineExceeded)
	// Live state stays at "v2" (the drifted value) -- it never landed.

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
}

// --- row 4/5: crash recovery, simulated by hand-constructing the exact
// on-disk state a real crash would leave, then re-running Ship ----------

func TestShip_CrashBetweenInFlightWriteAndCall_NeverLanded_RetriedAsFailed(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	ra := &core.ResourceApply{Address: addr}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(ra, core.ResourcePending, "")
	recordTransition(ra, core.ResourceInFlight, "") // exactly what a crash right before the call leaves behind
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// Attempt 1 is left unsealed forever -- never touched again.

	// Live state is unchanged ("v2") -- the call genuinely never happened.
	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}
	if sealed.Parent != "" {
		t.Fatalf("parent = %q, want \"\" (attempt 1 was never sealed, nothing to chain to)", sealed.Parent)
	}
	gotRA := sealed.Resources[0]
	if st, _ := gotRA.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	if len(gotRA.Reconciliation) == 0 {
		t.Fatal("expected the re-run to reconcile the crashed in_flight resource before deciding anything")
	}
}

func TestShip_CrashBetweenCallAndResultWrite_AlreadyLanded_ResolvesApplied(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	ra := &core.ResourceApply{Address: addr}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(ra, core.ResourcePending, "")
	recordTransition(ra, core.ResourceInFlight, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// The real ApplyResourceChange call succeeded server-side before ubx
	// died -- live state already shows the restored value.
	fake.setState(addr.String(), fakeState(addr, "v1"))

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}
	gotRA := sealed.Resources[0]
	if st, _ := gotRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied -- must never be re-applied a second time once reconciliation confirms it already landed", st)
	}
}

// --- row 7: stale detected mid-partial-apply -----------------------------

func TestShip_StaleDetectedMidPartialApply_HaltsRemainingResources(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr1 := core.Address{Stack: "payments", Type: "fake_widget", Name: "a"}
	addr2 := core.Address{Stack: "payments", Type: "fake_widget", Name: "b"}
	adoptFake(t, l, fake, addr1, "v1")
	adoptFake(t, l, fake, addr2, "v1")
	res1 := driftFake(t, l, fake, addr1, "v2")
	res2 := driftFake(t, l, fake, addr2, "v2")
	p := acceptRevert(t, l, "payments", res1, res2)

	// After acceptance but before ship, addr2 drifts a SECOND time --
	// reality moved again since this proposal was resolved.
	fake.setState(addr2.String(), fakeState(addr2, "v3-surprise"))

	sealed, err := Ship(context.Background(), l, fake, "", nil, p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "partially_applied" {
		t.Fatalf("outcome = %q, want partially_applied", sealed.Summary.Outcome)
	}
	var raA, raB *core.ResourceApply
	for _, r := range sealed.Resources {
		switch r.Address.Name {
		case "a":
			raA = r
		case "b":
			raB = r
		}
	}
	if st, _ := raA.LastState(); st != core.ResourceApplied {
		t.Fatalf("a's last state = %s, want applied -- real progress must stand", st)
	}
	if st, _ := raB.LastState(); st == core.ResourceApplied || st == core.ResourceInFlight {
		t.Fatalf("b's last state = %s, must never reach in_flight/applied once stale", st)
	}
	foundTerminal := false
	for _, e := range raB.Errors {
		if e.Classification == core.ErrorTerminal {
			foundTerminal = true
		}
	}
	if !foundTerminal {
		t.Fatalf("b's errors = %+v, want a terminal error recording the staleness", raB.Errors)
	}

	// b's live state (v3-surprise) must be untouched -- refused, not bulldozed.
	live, _ := fake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addr2))
	var m map[string]interface{}
	json.Unmarshal(live, &m)
	if m["value"] != "v3-surprise" {
		t.Fatalf("b's live value = %v, want untouched v3-surprise", m["value"])
	}
}

// --- row 8: double ship invocation racing --------------------------------

func TestShip_ConcurrentInvocations_NeverCollideOnAttemptNumber(t *testing.T) {
	l, fake, _, p := singleResourceRevert(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	attempts := make([]int64, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sealed, err := Ship(context.Background(), l, fake, "", nil, p)
			errs[i] = err
			if sealed != nil {
				attempts[i] = sealed.Attempt
			}
		}(i)
	}
	wg.Wait()

	// One of the two either applied it (attempt 1) or found it already
	// applied (ErrAlreadyApplied, ordinary idempotency); either way,
	// neither ever collides on the same attempt number, and every attempt
	// file left on disk parses cleanly.
	oks := 0
	for i, err := range errs {
		if err == nil {
			oks++
		} else if !errors.Is(err, ErrAlreadyApplied) {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	if oks == 0 {
		t.Fatal("expected at least one goroutine to actually ship the proposal")
	}
	if attempts[0] != 0 && attempts[1] != 0 && attempts[0] == attempts[1] {
		t.Fatalf("both goroutines report attempt %d -- collided", attempts[0])
	}

	all, err := l.ApplyAttempts(p.ID)
	if err != nil {
		t.Fatalf("apply attempts: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one apply attempt on disk")
	}
	for _, a := range all {
		if !a.Sealed() {
			t.Fatalf("attempt %d left unsealed after both goroutines finished", a.Attempt)
		}
	}
}

// --- row 9: apply record corrupted/truncated on re-run -------------------

func TestShip_CorruptApplyRecord_RefusesToGuess(t *testing.T) {
	l, fake, _, p := singleResourceRevert(t)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	// Hand-corrupt the attempt file, simulating a disk fault or a write
	// interrupted below writeApplyFile's own temp+rename durability.
	corruptPath := filepath.Join(l.Dir(), "ledger", "applies", fmt.Sprintf("%s.attempt-%d.apply.json", p.ID, 1))
	if err := os.WriteFile(corruptPath, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}

	if _, err := Ship(context.Background(), l, fake, "", nil, p); !errors.Is(err, core.ErrCorruptApplyRecord) {
		t.Fatalf("err = %v, want ErrCorruptApplyRecord", err)
	}
}
