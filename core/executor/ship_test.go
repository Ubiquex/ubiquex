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
	mu             sync.Mutex
	resources      map[string]json.RawMessage
	scripts        map[string][]applyStep
	createCounter  int
	createScripts  map[string][]applyStep // keyed by a create's own "value" field -- no id exists yet at script time to key by, unlike scripts above
	readsRemaining map[string]int         // UBI-44/42: id -> ReadResource calls remaining before it reports absent, for scriptDelayedAbsence
}

type applyStep struct {
	err                 error           // if set, ApplyResourceChange returns this error
	landsAs             json.RawMessage // modify/create: if set, the resource's live state is mutated to this value as a side effect, even though err is also returned -- simulates a provider that committed the change server-side despite the RPC itself failing/timing out
	destroyLanded       bool            // destroy (UBI-30): if true, the resource is actually deleted server-side as a side effect, even though err is also returned -- the destroy-specific counterpart to landsAs (a destroy has no "value" to land as, only gone-or-not)
	lyingDestroy        bool            // destroy (UBI-44): if true, ApplyResourceChange reports a clean success (nil error, the correct "null" NewState) while the resource stays present -- the exact shape found live against a real google_pubsub_topic: no error, no diagnostics, and nothing actually deleted.
	delayedAbsenceReads int             // destroy (UBI-42): if > 0, ApplyResourceChange reports a clean success and the destroy genuinely lands eventually, but the resource keeps reading back present for exactly this many further ReadResource calls first -- a real, bounded eventual-consistency lag, distinct from lyingDestroy's permanent lie.
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
	// UBI-44/42: genuine, bounded eventual-consistency lag -- the resource
	// stays observably present for a fixed number of reads after its own
	// destroy landed, then reports absent, simulating a real cloud
	// provider's own deletion-visibility window (SQS's own ~60s figure,
	// UBI-30) rather than an instantaneous, always-consistent fake.
	if remaining, scripted := f.readsRemaining[id]; scripted {
		if remaining > 0 {
			f.readsRemaining[id] = remaining - 1
		} else {
			delete(f.readsRemaining, id)
			delete(f.resources, id)
		}
	}
	state, ok := f.resources[id]
	if !ok {
		return nil, nil
	}
	cp := make(json.RawMessage, len(state))
	copy(cp, state)
	return cp, nil
}

// PlanResourceChange satisfies executor.Applier (UBI-30, docs/executor.md's
// own correction). The fake never needs real diff-tracking -- it has no
// internal SDKv2 shim to fool the way a real provider's own Delete
// function needs a genuine Plan to recognize a destroy -- but returns a
// fixed, non-empty plannedPrivate marker so ApplyResourceChange's own
// destroy branch below can assert it was actually threaded through,
// proving shipDestroyNode really calls this first rather than skipping
// straight to Apply (the exact regression UBI-30's own live AWS finale
// found: skipping Plan silently no-ops a real destroy).
func (f *fakeApplier) PlanResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, proposedNewState json.RawMessage) (json.RawMessage, []byte, error) {
	return proposedNewState, []byte("fake-planned-private"), nil
}

func (f *fakeApplier) ApplyResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, plannedState json.RawMessage, plannedPrivate []byte) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// A destroy (UBI-30, docs/executor.md's own amendment): PlannedState is
	// the literal JSON "null" ship.go's shipDestroyNode constructs,
	// PriorState the real, non-null, freshly re-read state -- the mirror
	// image of a genuine create's PriorState=="null" convention above.
	// Checked before the create/modify id-extraction path below, since a
	// destroy's PlannedState has no "id" to extract at all.
	if string(plannedState) == "null" {
		if len(plannedPrivate) == 0 {
			return nil, fmt.Errorf("fake: destroy called without plannedPrivate -- shipDestroyNode must call PlanResourceChange first (UBI-30)")
		}
		id, ok := extractID(priorState)
		if !ok {
			return nil, fmt.Errorf("fake: destroy priorState has no id: %s", priorState)
		}
		if steps, ok := f.scripts[id]; ok && len(steps) > 0 {
			step := steps[0]
			f.scripts[id] = steps[1:]
			if step.lyingDestroy {
				// UBI-44: reports the identical clean-success shape a
				// genuine destroy produces (nil error, literal "null"
				// NewState) but never actually removes the resource --
				// the exact response the real google_pubsub_topic gave.
				return json.RawMessage("null"), nil
			}
			if step.delayedAbsenceReads > 0 {
				// UBI-42: a genuine destroy, eventually -- but resources[id]
				// is deliberately NOT deleted here; ReadResource's own
				// countdown (below) is what removes it, after the scripted
				// number of reads, simulating real deletion-visibility lag
				// rather than an instantaneous fake.
				if f.readsRemaining == nil {
					f.readsRemaining = map[string]int{}
				}
				f.readsRemaining[id] = step.delayedAbsenceReads
				return json.RawMessage("null"), nil
			}
			if step.destroyLanded {
				delete(f.resources, id)
			}
			if step.err != nil {
				return nil, step.err
			}
		}
		delete(f.resources, id)
		return json.RawMessage("null"), nil
	}

	id, ok := extractID(plannedState)
	if !ok {
		// A genuine from-scratch create (UBI-27, docs/executor.md's own
		// amendment): PriorState is the literal JSON "null" ship.go's
		// shipCreate constructs, and the resolved config has no "id" at
		// all (never referenced by anything, so core/resolver never
		// marked it $computed). Assign one, the same role a real
		// provider's Apply plays once handed a genuine Unknown
		// (provider/ctyvalue.go's encodeUnknownAwareDynamicValue) --
		// never for a modify, where a missing id is a real bug, not a
		// create, hence the priorState check.
		if string(priorState) != "null" {
			return nil, fmt.Errorf("fake: plannedState has no id: %s", plannedState)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(plannedState, &m); err != nil {
			return nil, fmt.Errorf("fake: decode plannedState: %w", err)
		}
		if v, _ := m["value"].(string); v != "" {
			if steps, ok := f.createScripts[v]; ok && len(steps) > 0 {
				step := steps[0]
				f.createScripts[v] = steps[1:]
				if step.err != nil {
					return nil, step.err
				}
			}
		}
		f.createCounter++
		id = fmt.Sprintf("created-%d", f.createCounter)
		m["id"] = id
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		plannedState = b
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

// deleteState removes id's live state, as if it had already been destroyed
// out-of-band before ubx ever attempted anything -- UBI-30's "destroy of an
// already-absent resource" row, and the "landed before ubx got to check"
// half of the crash rows below.
func (f *fakeApplier) deleteState(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.resources, id)
}

// scriptDestroyOutcome schedules the next ApplyResourceChange call for a
// destroy of id to return err while EITHER actually deleting the resource
// server-side (destroyLanded=true, simulating a timeout where the destroy
// actually landed) OR leaving it untouched (destroyLanded=false, simulating
// one where it didn't) -- docs/destroys-adversarial.md's own two timeout
// rows, the destroy-specific counterpart to scriptApplyTimeoutButLanded.
func (f *fakeApplier) scriptDestroyOutcome(id string, err error, destroyLanded bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{err: err, destroyLanded: destroyLanded})
}

// scriptLyingDestroy (UBI-44) schedules the next ApplyResourceChange call
// for a destroy of id to report a clean success -- nil error, the correct
// literal "null" NewState, indistinguishable at the wire-protocol level
// from a genuine destroy -- while never actually removing the resource.
// This is the fixture-level reproduction of what a real
// google_pubsub_topic destroy did against real GCP: no error, no
// diagnostics, and zero real DeleteTopic calls (confirmed via Cloud Audit
// Logs). Proves shipDestroyNode's own universal post-destroy read-back
// (docs/executor.md's UBI-44 amendment) catches a lie a provider's own
// response gives no other signal of.
func (f *fakeApplier) scriptLyingDestroy(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{lyingDestroy: true})
}

// scriptDelayedAbsence (UBI-42) schedules the next ApplyResourceChange call
// for a destroy of id to report a clean success and genuinely land, but
// keep reading back present for exactly readsBeforeAbsent further
// ReadResource calls first -- simulating a real cloud provider's own
// bounded deletion-visibility lag (SQS's own ~60-second figure, UBI-30)
// rather than an always-instantly-consistent fake. Proves the new
// destroyReconcileBackoffSchedule (UBI-42) reaches a genuinely
// slow-but-real absence instead of giving up too soon.
func (f *fakeApplier) scriptDelayedAbsence(id string, readsBeforeAbsent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{delayedAbsenceReads: readsBeforeAbsent})
}

func (f *fakeApplier) scriptApplyError(id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[id] = append(f.scripts[id], applyStep{err: err})
}

// scriptCreateFailure schedules the next ApplyResourceChange call for a
// CREATE whose resolved config's own "value" field equals value to
// return err instead -- a create has no id yet at script-time, so this is
// keyed by content instead, the same role scriptApplyError plays for an
// already-identified (modify) resource.
func (f *fakeApplier) scriptCreateFailure(value string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createScripts == nil {
		f.createScripts = map[string][]applyStep{}
	}
	f.createScripts[value] = append(f.createScripts[value], applyStep{err: err})
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

// changeCreateJSON builds one delta.creates entry's raw JSON, matching
// core/resolver's own emitted node shape (docs/schema.md's "Amendment:
// intent files and resolved change proposals") -- hand-built here rather
// than actually resolved, the same "don't depend on core/resolver for an
// executor test" posture acceptRevert already takes for drift_revert
// (core/executor stays independently testable from core/resolver).
func changeCreateJSON(t *testing.T, addr core.Address, config string, dependsOn ...string) json.RawMessage {
	t.Helper()
	node := map[string]interface{}{
		"stack":  addr.Stack,
		"type":   addr.Type,
		"name":   addr.Name,
		"config": json.RawMessage(config),
	}
	if len(dependsOn) > 0 {
		node["depends_on"] = dependsOn
	}
	b, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("build create node: %v", err)
	}
	return b
}

// acceptChange builds and accepts a kind:"change" proposal directly from
// already-built delta.creates nodes.
func acceptChange(t *testing.T, l *core.Ledger, stack string, creates []json.RawMessage) *core.Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "test change"},
		Delta:         core.Delta{Creates: creates},
		Resolution:    core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   core.BlastRadius{Creates: int64(len(creates))},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept change: %v", err)
	}
	return accepted
}

// destroyTargetInput builds the resolution.inputs["destroy_target"] entry
// core.Validate requires for every delta.destroys entry (docs/schema.md's
// "Amendment: destroys") -- observed_hash of state, and the same lookup
// convention lookupJSON/fakeState already establish for every other
// fixture in this file.
func destroyTargetInput(t *testing.T, addr core.Address, state json.RawMessage) core.ResolutionInput {
	t.Helper()
	hash, err := core.ObservedHash(state)
	if err != nil {
		t.Fatalf("destroy target input: %v", err)
	}
	return core.ResolutionInput{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: lookupJSON(addr)}
}

// acceptDestroy builds and accepts a kind:"change" proposal directly from
// already-built delta.destroys entries and their own resolution.inputs
// evidence -- the destroy-specific counterpart to acceptChange, hand-built
// rather than actually resolved (core/executor stays independently
// testable from core/resolver, the same posture acceptRevert/acceptChange
// already take).
func acceptDestroy(t *testing.T, l *core.Ledger, stack string, destroys []core.DestroyEntry, inputs []core.ResolutionInput) *core.Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "test destroy"},
		Delta:         core.Delta{Destroys: destroys},
		Resolution:    core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: inputs},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   core.BlastRadius{Destroys: int64(len(destroys))},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept destroy: %v", err)
	}
	return accepted
}

// singleResourceDestroy is the common one-resource destroy fixture:
// adopted at "v1" (real state, via the real scan/accept pipeline like
// every other fixture here), then a kind:"change" proposal destroying it
// accepted on top.
func singleResourceDestroy(t *testing.T) (l *core.Ledger, fake *fakeApplier, addr core.Address, p *core.Proposal) {
	t.Helper()
	l = core.Open(t.TempDir())
	fake = newFakeApplier()
	addr = core.Address{Stack: "payments", Type: "fake_widget", Name: "old-widget"}
	adoptFake(t, l, fake, addr, "v1")
	state, _, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	entry := core.DestroyEntry{Address: addr, State: state}
	p = acceptDestroy(t, l, "payments", []core.DestroyEntry{entry}, []core.ResolutionInput{destroyTargetInput(t, addr, state)})
	return l, fake, addr, p
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

// fakeStateWithTags builds a fake_widget-shaped state that also carries a
// "tags" map -- needed to reproduce a real bug found live-testing ubx ship
// end to end (UBI-26 session 3): a key added to a map attribute
// out-of-band has no After entry at all (diffAttributes has no ledger
// value to record for something the ledger never had), only a Before one,
// and a revert must delete it, not silently leave it in place.
func fakeStateWithTags(addr core.Address, value string, tags map[string]string) json.RawMessage {
	b, _ := json.Marshal(map[string]interface{}{"id": addr.String(), "value": value, "tags": tags})
	return json.RawMessage(b)
}

// --- happy path ---------------------------------------------------------

func TestShip_SingleResource_AppliesCleanly(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)
	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

// TestShip_RevertRemovesAttributeAddedOutOfBand is a permanent end-to-end
// regression guard (through the real Ship loop, not just core.ApplyAfter's
// own unit test) for the bug found live-testing this session: a tag added
// out-of-band -- present in drifted reality, never in the ledger's own
// recorded truth -- must be REMOVED by a shipped revert, not silently
// carried forward because it has no After entry to substitute.
func TestShip_RevertRemovesAttributeAddedOutOfBand(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "api-cache"}

	fake.setState(addr.String(), fakeStateWithTags(addr, "v1", map[string]string{"env": "prod"}))
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("adopt scan: %v", err)
	}
	adoptProp, err := core.GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("adopt generate: %v", err)
	}
	if _, err := core.Accept(l, adoptProp); err != nil {
		t.Fatalf("adopt accept: %v", err)
	}

	// Out-of-band change: a "hotfix" tag added, never recorded in the ledger.
	fake.setState(addr.String(), fakeStateWithTags(addr, "v1", map[string]string{"env": "prod", "hotfix": "true"}))
	driftRes, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("drift scan: %v", err)
	}
	if driftRes.Outcome != core.ScanDrifted {
		t.Fatalf("outcome = %v, want ScanDrifted", driftRes.Outcome)
	}
	p := acceptRevert(t, l, "payments", driftRes)

	// Confirm the proposal itself has the expected asymmetric shape before
	// shipping it: Before names the added tag, After does not.
	mod := p.Delta.Modifies[0]
	if _, ok := mod.Before["tags.hotfix"]; !ok {
		t.Fatalf("mod.Before = %+v, want a tags.hotfix entry", mod.Before)
	}
	if _, ok := mod.After["tags.hotfix"]; ok {
		t.Fatalf("mod.After = %+v, want no tags.hotfix entry (the ledger never had one)", mod.After)
	}

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %q, want applied", sealed.Summary.Outcome)
	}

	live, err := fake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addr))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(live, &m)
	tags, _ := m["tags"].(map[string]interface{})
	if _, present := tags["hotfix"]; present {
		t.Fatalf("live tags = %v, want hotfix removed -- ship reported \"applied\" but left the added tag in place", tags)
	}
	if tags["env"] != "prod" {
		t.Fatalf("live tags = %v, want env untouched", tags)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", accepted); !errors.Is(err, ErrUnsupportedKind) {
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
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("err = %v, want ErrNotAccepted", err)
	}
}

// --- redacted after values are declined, never applied -------------------

func TestShip_RedactedAfterValue_Declined_NeverAppliesOrReads(t *testing.T) {
	l, fake, addr, p := singleResourceRevert(t)

	// Simulate a redacted restore target -- as if the attribute being
	// reverted were provider-Sensitive-flagged (UBI-23/24): mod.After holds
	// a $redacted marker, never the real material.
	p.Delta.Modifies[0].After["value"] = json.RawMessage(`{"$redacted":{"sha256":"deadbeef"}}`)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "failed" {
		t.Fatalf("outcome = %q, want failed", sealed.Summary.Outcome)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourcePending {
		t.Fatalf("last state = %s, want pending (declined before ever attempting anything)", st)
	}
	if len(ra.Errors) != 1 || ra.Errors[0].Classification != core.ErrorTerminal {
		t.Fatalf("errors = %+v, want exactly one terminal decline", ra.Errors)
	}

	// Confirm nothing was ever attempted against the live resource --
	// still exactly the drifted "v2" value, never a literal "$redacted"
	// string written to it, and never silently reverted either.
	live, err := fake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addr))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(live, &m)
	if m["value"] != "v2" {
		t.Fatalf("live value = %v, want untouched v2 -- ship must never construct a live apply from a redacted value", m["value"])
	}
}

func TestShip_RedactedAfterValue_RetriedForever_NeverSilentlySkipped(t *testing.T) {
	// A declined resource stays "pending" forever (never reaches
	// in_flight, never counts toward the retry budget), so a re-run
	// declines it again identically rather than treating it as
	// already-resolved -- the only way out is a human using `ubx
	// revert-plan`'s manual path.
	l, fake, _, p := singleResourceRevert(t)
	p.Delta.Modifies[0].After["value"] = json.RawMessage(`{"$redacted":{"sha256":"deadbeef"}}`)

	for i := 0; i < 3; i++ {
		sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
		if err != nil {
			t.Fatalf("ship attempt %d: %v", i+1, err)
		}
		if sealed.Summary.Outcome != "failed" {
			t.Fatalf("attempt %d: outcome = %q, want failed", i+1, sealed.Summary.Outcome)
		}
	}
}

// --- idempotency ---------------------------------------------------------

func TestShip_AlreadyFullyApplied_IsANoOp(t *testing.T) {
	l, fake, _, p := singleResourceRevert(t)
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p); err != nil {
		t.Fatalf("first ship: %v", err)
	}
	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p); !errors.Is(err, ErrAlreadyApplied) {
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
		sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
		if err != nil {
			t.Fatalf("ship attempt %d: %v", i+1, err)
		}
		if st, _ := sealed.Resources[0].LastState(); st != core.ResourceFailed {
			t.Fatalf("attempt %d: last state = %s, want failed", i+1, st)
		}
	}

	// The budget is now exhausted -- a further ship must refuse to issue
	// another ApplyResourceChange call at all, rather than retry forever.
	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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
	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

// TestShip_CrashAfterApplyLanded_PureDeletionRevert_ReconciliationResolvesApplied
// is a permanent regression guard for a real bug found live-testing the
// centerpiece kill -9 scenario against real AWS (UBI-26 session 4,
// docs/reliability-report.md): a drift_revert whose only change is
// removing an attribute added out-of-band has an EMPTY After map (nothing
// to add back -- see core.ApplyAfter's own Before-only-path deletion
// logic). reconciliationVerdict's first version only ever compared
// observed state against mod.After's own dot-paths, so it could never
// conclude "applied" for a pure-deletion revert -- reconciliation reported
// "still_unknown" forever, even reading a live state that had, in fact,
// already been correctly corrected by the real ApplyResourceChange call
// before ubx crashed.
func TestShip_CrashAfterApplyLanded_PureDeletionRevert_ReconciliationResolvesApplied(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "api-cache"}

	fake.setState(addr.String(), fakeStateWithTags(addr, "v1", map[string]string{"env": "prod"}))
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("adopt scan: %v", err)
	}
	adoptProp, err := core.GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("adopt generate: %v", err)
	}
	if _, err := core.Accept(l, adoptProp); err != nil {
		t.Fatalf("adopt accept: %v", err)
	}

	fake.setState(addr.String(), fakeStateWithTags(addr, "v1", map[string]string{"env": "prod", "added": "out-of-band"}))
	driftRes, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{Address: addr, CurrentState: lookupJSON(addr)})
	if err != nil {
		t.Fatalf("drift scan: %v", err)
	}
	p := acceptRevert(t, l, "payments", driftRes)
	if len(p.Delta.Modifies[0].After) != 0 {
		t.Fatalf("mod.After = %+v, want empty (pure deletion)", p.Delta.Modifies[0].After)
	}

	// Simulate: attempt 1 crashed right after the real apply call landed
	// (the "added" tag genuinely already removed live), before ubx recorded
	// the applied transition.
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
	fake.setState(addr.String(), fakeStateWithTags(addr, "v1", map[string]string{"env": "prod"}))

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	gotRA := sealed.Resources[0]
	if st, _ := gotRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("last state = %s, want applied -- reconciliation must recognize a pure-deletion revert already landed", st)
	}
	if len(gotRA.Reconciliation) == 0 || gotRA.Reconciliation[0].Outcome != "applied" {
		t.Fatalf("reconciliation = %+v, want its first attempt to conclude applied", gotRA.Reconciliation)
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

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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
			sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
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

	if _, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p); !errors.Is(err, core.ErrCorruptApplyRecord) {
		t.Fatalf("err = %v, want ErrCorruptApplyRecord", err)
	}
}

// --- shipping change proposals (UBI-27) --------------------------------

// TestShip_ChangeProposal_CreateChain_FeedsComputedOutputForward is the
// centerpiece happy path: "mirror" creates a resource whose config
// carries a $computed marker pointing at "primary"'s not-yet-known id.
// Confirms shipChange ships primary first, then substitutes primary's
// REAL applied id into mirror's PlannedState before mirror's own apply
// call -- never the marker itself, never a guess.
func TestShip_ChangeProposal_CreateChain_FeedsComputedOutputForward(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	primary := core.Address{Stack: "payments", Type: "fake_widget", Name: "primary"}
	mirror := core.Address{Stack: "payments", Type: "fake_widget", Name: "mirror"}

	creates := []json.RawMessage{
		changeCreateJSON(t, primary, `{"value":"v1"}`),
		changeCreateJSON(t, mirror, `{"value":{"$computed":{"from":"payments.fake_widget.primary.id"}}}`, primary.String()),
	}
	p := acceptChange(t, l, "payments", creates)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied", sealed.Summary.Outcome)
	}

	var primaryRA, mirrorRA *core.ResourceApply
	for _, ra := range sealed.Resources {
		switch ra.Address.String() {
		case primary.String():
			primaryRA = ra
		case mirror.String():
			mirrorRA = ra
		}
	}
	if primaryRA == nil || mirrorRA == nil {
		t.Fatalf("expected resource_apply entries for both primary and mirror, got %+v", sealed.Resources)
	}
	if st, _ := primaryRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("primary state = %s, want applied", st)
	}
	if st, _ := mirrorRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("mirror state = %s, want applied", st)
	}

	var primaryResult struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(primaryRA.ProviderResult, &primaryResult); err != nil {
		t.Fatalf("decode primary result: %v", err)
	}
	if primaryResult.ID == "" {
		t.Fatal("primary's applied result has no id -- the fake's own create fill-in didn't run")
	}

	var mirrorResult struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(mirrorRA.ProviderResult, &mirrorResult); err != nil {
		t.Fatalf("decode mirror result: %v", err)
	}
	if mirrorResult.Value != primaryResult.ID {
		t.Fatalf("mirror.value = %q, want primary's real applied id %q -- the $computed marker was not correctly substituted mid-walk", mirrorResult.Value, primaryResult.ID)
	}
}

// TestShip_ChangeProposal_DependentNeverAttemptedBeforeDependencyApplies
// is the first of the two adversarial rows requested for UBI-27's
// executor session: "dependent shipped before its dependency's output
// exists" must be structurally impossible, not merely undesirable.
// primary's create fails terminally; mirror (which depends on it) must
// never be attempted at all -- confirmed both by its own recorded state
// (blocked, never in_flight) and by the fake provider's own resource
// store staying completely empty (mirror's ApplyResourceChange was never
// even called).
func TestShip_ChangeProposal_DependentNeverAttemptedBeforeDependencyApplies(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	primary := core.Address{Stack: "payments", Type: "fake_widget", Name: "primary"}
	mirror := core.Address{Stack: "payments", Type: "fake_widget", Name: "mirror"}

	creates := []json.RawMessage{
		changeCreateJSON(t, primary, `{"value":"v1"}`),
		changeCreateJSON(t, mirror, `{"value":{"$computed":{"from":"payments.fake_widget.primary.id"}}}`, primary.String()),
	}
	p := acceptChange(t, l, "payments", creates)

	fake.scriptCreateFailure("v1", &TerminalError{Err: errors.New("simulated: primary create rejected")})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "failed" {
		t.Fatalf("outcome = %s, want failed (nothing applied)", sealed.Summary.Outcome)
	}

	var primaryRA, mirrorRA *core.ResourceApply
	for _, ra := range sealed.Resources {
		switch ra.Address.String() {
		case primary.String():
			primaryRA = ra
		case mirror.String():
			mirrorRA = ra
		}
	}
	if primaryRA == nil || mirrorRA == nil {
		t.Fatalf("expected resource_apply entries for both primary and mirror, got %+v", sealed.Resources)
	}
	if st, _ := primaryRA.LastState(); st != core.ResourceFailed {
		t.Fatalf("primary state = %s, want failed", st)
	}
	for _, tr := range mirrorRA.Transitions {
		if tr.State == core.ResourceInFlight {
			t.Fatal("mirror reached in_flight -- it must never be attempted while its dependency has not applied")
		}
	}
	if len(mirrorRA.Errors) == 0 || mirrorRA.Errors[0].Message == "" {
		t.Fatal("expected mirror to record a clear blocked-on-dependency error, not silence")
	}
	if len(fake.resources) != 0 {
		t.Fatalf("fake.resources = %+v, want empty -- neither primary (failed) nor mirror (blocked) should have landed anything", fake.resources)
	}
}

// TestShip_ChangeProposal_KillBetweenDependencyAppliedAndDependentStarting_RecoversRealOutputOnRerun
// is the second requested adversarial row: a real `kill -9` between one
// resource's apply completing and the next one's own turn beginning.
// Attempt 1 is hand-built to show primary already `applied`, with a real
// ProviderResult, and left UNSEALED -- exactly what a process kill at
// that exact point leaves on disk (the same convention
// TestShip_CrashBetweenInFlightWriteAndCall_NeverLanded_RetriedAsFailed
// already established for a single-resource drift_revert). A fresh
// Ship() call must recognize primary as already applied (never
// re-applying it against the fake provider a second time) and recover its
// REAL recorded output from history to correctly ship mirror.
func TestShip_ChangeProposal_KillBetweenDependencyAppliedAndDependentStarting_RecoversRealOutputOnRerun(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	primary := core.Address{Stack: "payments", Type: "fake_widget", Name: "primary"}
	mirror := core.Address{Stack: "payments", Type: "fake_widget", Name: "mirror"}

	creates := []json.RawMessage{
		changeCreateJSON(t, primary, `{"value":"v1"}`),
		changeCreateJSON(t, mirror, `{"value":{"$computed":{"from":"payments.fake_widget.primary.id"}}}`, primary.String()),
	}
	p := acceptChange(t, l, "payments", creates)

	rec, err := l.BeginApply(p.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	primaryResult := json.RawMessage(`{"id":"primary-real-id","value":"v1"}`)
	ra := &core.ResourceApply{Address: primary}
	rec.Resources = append(rec.Resources, ra)
	recordTransition(ra, core.ResourcePending, "")
	recordTransition(ra, core.ResourceInFlight, "")
	ra.ProviderResult = primaryResult
	recordTransition(ra, core.ResourceApplied, "")
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	// Attempt 1 is never sealed -- the process died before mirror's own
	// turn ever began.

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("re-run ship: %v", err)
	}
	if sealed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", sealed.Attempt)
	}

	var primaryRA, mirrorRA *core.ResourceApply
	for _, ra := range sealed.Resources {
		switch ra.Address.String() {
		case primary.String():
			primaryRA = ra
		case mirror.String():
			mirrorRA = ra
		}
	}
	if primaryRA == nil || mirrorRA == nil {
		t.Fatalf("expected resource_apply entries for both primary and mirror, got %+v", sealed.Resources)
	}
	if st, _ := primaryRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("primary state = %s, want applied (recognized from attempt 1's own history, never re-applied)", st)
	}
	if len(primaryRA.Transitions) != 1 {
		t.Fatalf("primary should show only a single pass-through confirmation transition in attempt 2, got %+v", primaryRA.Transitions)
	}
	if st, _ := mirrorRA.LastState(); st != core.ResourceApplied {
		t.Fatalf("mirror state = %s, want applied", st)
	}
	if _, exists := fake.resources["primary-real-id"]; exists {
		t.Fatal("primary was re-applied for real against the fake provider -- it should have been recognized as already-applied from attempt 1's own history and skipped entirely")
	}

	var mirrorResult struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(mirrorRA.ProviderResult, &mirrorResult); err != nil {
		t.Fatalf("decode mirror result: %v", err)
	}
	if mirrorResult.Value != "primary-real-id" {
		t.Fatalf("mirror.value = %q, want primary's REAL recorded id %q (recovered from attempt 1's own history, not lost)", mirrorResult.Value, "primary-real-id")
	}
}
