package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// --- fakeApplierPool: a real multi-entry ApplierPool, for hermetic tests
// that need to distinguish between two or more providers -- SingleApplierPool
// (ship.go) only ever wraps one, which can't exercise "provider B fails to
// launch while provider A is fine" or "provider B's own client is launched
// fresh on a re-run" at all.

type fakeApplierPool struct {
	mu       sync.Mutex
	entries  map[string]*fakeApplier
	fail     map[string]error
	getCalls map[string]int
}

func newFakeApplierPool() *fakeApplierPool {
	return &fakeApplierPool{entries: map[string]*fakeApplier{}, fail: map[string]error{}, getCalls: map[string]int{}}
}

func poolKey(source, version string) string { return source + "@" + version }

// register makes app the Applier this pool returns for source@version.
func (p *fakeApplierPool) register(source, version string, app *fakeApplier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[poolKey(source, version)] = app
}

// failLaunch makes this pool's own Get return err for source@version instead
// of a real Applier -- docs/multi-provider-adversarial.md row 4's own
// injection ("fails to launch: bad credentials, unreachable registry, a
// crashed handshake"), and row 6's own stand-in for "the process was killed
// before this provider's client ever launched" -- both leave the identical
// trace in the ledger (nothing about this provider's nodes was ever
// attempted), so the same mechanism produces either scenario's starting
// state.
func (p *fakeApplierPool) failLaunch(source, version string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[poolKey(source, version)] = err
}

func (p *fakeApplierPool) Get(ctx context.Context, source, version string) (Applier, json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := poolKey(source, version)
	p.getCalls[key]++
	if err, ok := p.fail[key]; ok {
		return nil, nil, err
	}
	app, ok := p.entries[key]
	if !ok {
		return nil, nil, fmt.Errorf("fakeApplierPool: no provider registered for %s", key)
	}
	return app, nil, nil
}

// calls reports how many times Get was actually invoked for source@version
// -- the "provider A's own client is never re-launched needlessly" half of
// row 6's own required outcome needs a way to prove a call did NOT happen
// again on a re-run, not just that the final state looks right.
func (p *fakeApplierPool) calls(source, version string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getCalls[poolKey(source, version)]
}

// changeCreateJSONWithProvider mirrors changeCreateJSON (ship_test.go) but
// also sets the "provider" field core/resolver now emits (docs/schema.md's
// own "Amendment: the provider field returns", UBI-43) -- hand-built here
// for the identical "core/executor stays independently testable from
// core/resolver" reason changeCreateJSON's own doc comment already gives.
func changeCreateJSONWithProvider(t *testing.T, addr core.Address, source, version, config string, dependsOn ...string) json.RawMessage {
	t.Helper()
	node := map[string]interface{}{
		"stack":    addr.Stack,
		"type":     addr.Type,
		"name":     addr.Name,
		"provider": map[string]interface{}{"source": source, "version": version},
		"config":   json.RawMessage(config),
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

const (
	awsSource, awsVersion   = "hashicorp/aws", "6.60.0"
	helmSource, helmVersion = "hashicorp/helm", "3.0.2"
)

// --- row 4: provider launch failure mid-walk -------------------------------

func TestShip_ProviderLaunchFailureMidWalk_PerNodeTerminal_OtherProviderProceeds(t *testing.T) {
	l := core.Open(t.TempDir())
	addrAWS := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	addrHelm := core.Address{Stack: "payments", Type: "helm_release", Name: "app"}

	// No dependency between them -- either walk order is valid, exactly
	// row 4's own setup.
	creates := []json.RawMessage{
		changeCreateJSONWithProvider(t, addrAWS, awsSource, awsVersion, `{"id":null,"value":"db"}`),
		changeCreateJSONWithProvider(t, addrHelm, helmSource, helmVersion, `{"id":null,"value":"release"}`),
	}
	p := acceptChange(t, l, "payments", creates)

	pool := newFakeApplierPool()
	pool.register(awsSource, awsVersion, newFakeApplier())
	pool.failLaunch(helmSource, helmVersion, fmt.Errorf("dial tcp: connection refused"))

	sealed, err := Ship(context.Background(), l, pool, "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "partially_applied" {
		t.Fatalf("outcome = %q, want partially_applied", sealed.Summary.Outcome)
	}

	var raAWS, raHelm *core.ResourceApply
	for _, r := range sealed.Resources {
		switch r.Address.Name {
		case "main":
			raAWS = r
		case "app":
			raHelm = r
		}
	}
	if st, _ := raAWS.LastState(); st != core.ResourceApplied {
		t.Fatalf("aws node's last state = %s, want applied -- an unrelated provider's own launch failure must never block it", st)
	}
	if st, ok := raHelm.LastState(); ok && (st == core.ResourceInFlight || st == core.ResourceApplied) {
		t.Fatalf("helm node's last state = %s, must never reach in_flight/applied -- its own provider never launched", st)
	}
	foundTerminal := false
	for _, e := range raHelm.Errors {
		if e.Classification == core.ErrorTerminal {
			foundTerminal = true
			if !strings.Contains(e.Message, "provider unavailable") {
				t.Fatalf("helm node's terminal error = %q, want it to name the launch failure", e.Message)
			}
		}
	}
	if !foundTerminal {
		t.Fatalf("helm node's errors = %+v, want a terminal error naming the launch failure", raHelm.Errors)
	}

	// A re-run, once the failed provider is fixed, resumes correctly --
	// the failed node retried, the already-applied one skipped.
	pool2 := newFakeApplierPool()
	pool2.register(awsSource, awsVersion, newFakeApplier()) // a fresh instance -- must never be asked to do anything for "main"
	pool2.register(helmSource, helmVersion, newFakeApplier())
	sealed2, err := Ship(context.Background(), l, pool2, "", p)
	if err != nil {
		t.Fatalf("re-ship: %v", err)
	}
	if sealed2.Summary.Outcome != "applied" {
		t.Fatalf("re-ship outcome = %q, want applied", sealed2.Summary.Outcome)
	}
	if pool2.calls(awsSource, awsVersion) != 0 {
		t.Fatalf("aws provider Get called %d times on re-run, want 0 -- main is already applied, never needs its provider again", pool2.calls(awsSource, awsVersion))
	}
}

// --- row 6: kill -9 between providers ---------------------------------------

func TestShip_KillBetweenProviders_RerunLaunchesSecondProviderFresh(t *testing.T) {
	l := core.Open(t.TempDir())
	addrAWS := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	addrHelm := core.Address{Stack: "payments", Type: "helm_release", Name: "app"}

	creates := []json.RawMessage{
		changeCreateJSONWithProvider(t, addrAWS, awsSource, awsVersion, `{"id":null,"value":"db"}`),
		changeCreateJSONWithProvider(t, addrHelm, helmSource, helmVersion, `{"id":null,"value":"release"}`),
	}
	p := acceptChange(t, l, "payments", creates)

	// Attempt 1: provider A launches and its own node lands for real;
	// provider B never launches at all -- standing in for a kill -9
	// strictly before B's own client existed. From the ledger's own point
	// of view this is indistinguishable from a genuine launch failure
	// (row 4) -- neither leaves any trace that B was ever attempted --
	// which is exactly the point: the recovery path must not care why B
	// never got its turn, only that it didn't.
	poolAttempt1 := newFakeApplierPool()
	awsFake := newFakeApplier()
	poolAttempt1.register(awsSource, awsVersion, awsFake)
	poolAttempt1.failLaunch(helmSource, helmVersion, fmt.Errorf("killed before launch"))

	sealed1, err := Ship(context.Background(), l, poolAttempt1, "", p)
	if err != nil {
		t.Fatalf("ship attempt 1: %v", err)
	}
	if sealed1.Summary.Outcome != "partially_applied" {
		t.Fatalf("attempt 1 outcome = %q, want partially_applied", sealed1.Summary.Outcome)
	}

	// Attempt 2: a genuinely fresh pool -- awsFake reused (it already has
	// main's own real state; a real re-run would reuse the SAME already-
	// launched client if the process hadn't died, but even a brand-new
	// client reading the same live state converges identically, so
	// reusing the instance here only proves "never re-applied", not
	// "never re-launched" -- that's what the call-count assertion below
	// is for). helmFake is a completely untouched instance, standing in
	// for "provider B's client, launched for the very first time on this
	// attempt."
	poolAttempt2 := newFakeApplierPool()
	poolAttempt2.register(awsSource, awsVersion, awsFake)
	helmFake := newFakeApplier()
	poolAttempt2.register(helmSource, helmVersion, helmFake)

	sealed2, err := Ship(context.Background(), l, poolAttempt2, "", p)
	if err != nil {
		t.Fatalf("ship attempt 2: %v", err)
	}
	if sealed2.Summary.Outcome != "applied" {
		t.Fatalf("attempt 2 outcome = %q, want applied", sealed2.Summary.Outcome)
	}

	var raAWS2, raHelm2 *core.ResourceApply
	for _, r := range sealed2.Resources {
		switch r.Address.Name {
		case "main":
			raAWS2 = r
		case "app":
			raHelm2 = r
		}
	}
	// main: no re-attempt against the already-terminal node -- the
	// existing idempotency contract's own single "already applied in a
	// prior attempt" transition, nothing else.
	if len(raAWS2.Transitions) != 1 || raAWS2.Transitions[0].State != core.ResourceApplied {
		t.Fatalf("main's attempt-2 transitions = %+v, want exactly one applied (already-applied pass-through)", raAWS2.Transitions)
	}
	if poolAttempt2.calls(awsSource, awsVersion) != 0 {
		t.Fatalf("aws provider Get called %d times in attempt 2, want 0 -- main never needs its provider again once applied", poolAttempt2.calls(awsSource, awsVersion))
	}
	// app: an ordinary fresh pre-attempt cycle, exactly as if this were
	// the first attempt for it -- pending -> in_flight -> applied.
	wantStates := []core.ResourceState{core.ResourcePending, core.ResourceInFlight, core.ResourceApplied}
	if len(raHelm2.Transitions) != len(wantStates) {
		t.Fatalf("app's attempt-2 transitions = %+v, want %v", raHelm2.Transitions, wantStates)
	}
	for i, tr := range raHelm2.Transitions {
		if tr.State != wantStates[i] {
			t.Fatalf("app's attempt-2 transitions = %+v, want %v", raHelm2.Transitions, wantStates)
		}
	}
	if poolAttempt2.calls(helmSource, helmVersion) != 1 {
		t.Fatalf("helm provider Get called %d times in attempt 2, want exactly 1 -- launched fresh, for the first time", poolAttempt2.calls(helmSource, helmVersion))
	}
}

// --- row 7: per-provider freshness, one drifts and the other doesn't -------

func TestShip_PerProviderFreshness_OneProviderDriftsOtherDoesNot(t *testing.T) {
	l := core.Open(t.TempDir())
	// fakeApplier's own Schema() only ever advertises "fake_widget"
	// (ship_test.go) -- core.RunScan validates the type against it, so
	// both addresses use that same type here, distinguished by name only,
	// matching every other fixture in this package. The point under test
	// is which PROVIDER a node routes through, not its type.
	addrAWS := core.Address{Stack: "payments", Type: "fake_widget", Name: "main-aws"}
	addrHelm := core.Address{Stack: "payments", Type: "fake_widget", Name: "main-helm"}

	awsFake := newFakeApplier()
	helmFake := newFakeApplier()
	adoptFake(t, l, awsFake, addrAWS, "v1")
	adoptFake(t, l, helmFake, addrHelm, "v1")

	awsState := fakeState(addrAWS, "v1")
	helmState := fakeState(addrHelm, "v1")
	awsHash, err := core.ObservedHash(awsState)
	if err != nil {
		t.Fatalf("observed hash: %v", err)
	}
	helmHash, err := core.ObservedHash(helmState)
	if err != nil {
		t.Fatalf("observed hash: %v", err)
	}

	modifies := []core.Modification{
		{
			Target:   addrAWS,
			Before:   map[string]json.RawMessage{"value": json.RawMessage(`"v1"`)},
			After:    map[string]json.RawMessage{"value": json.RawMessage(`"v2"`)},
			Provider: &core.ProviderRef{Source: awsSource, Version: awsVersion},
		},
		{
			Target:   addrHelm,
			Before:   map[string]json.RawMessage{"value": json.RawMessage(`"v1"`)},
			After:    map[string]json.RawMessage{"value": json.RawMessage(`"v2"`)},
			Provider: &core.ProviderRef{Source: helmSource, Version: helmVersion},
		},
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	draft := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "multi-provider modify"},
		Delta:         core.Delta{Modifies: modifies},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addrAWS.String(), ObservedHash: awsHash, Lookup: lookupJSON(addrAWS)},
				{Kind: "live_state", Resource: addrHelm.String(), ObservedHash: helmHash, Lookup: lookupJSON(addrHelm)},
			},
		},
		CostDelta:   core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{Modifies: 2},
		Status:      core.StatusDraft,
	}
	p, err := core.Accept(l, draft)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Before ubx ship reaches either resource's own turn, provider A's
	// own resource drifts out-of-band -- provider B's is untouched.
	awsFake.setState(addrAWS.String(), fakeState(addrAWS, "v1-drifted"))

	pool := newFakeApplierPool()
	pool.register(awsSource, awsVersion, awsFake)
	pool.register(helmSource, helmVersion, helmFake)

	sealed, err := Ship(context.Background(), l, pool, "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "partially_applied" {
		t.Fatalf("outcome = %q, want partially_applied", sealed.Summary.Outcome)
	}

	var raAWS, raHelm *core.ResourceApply
	for _, r := range sealed.Resources {
		switch r.Address.Name {
		case "main-aws":
			raAWS = r
		case "main-helm":
			raHelm = r
		}
	}
	if st, ok := raAWS.LastState(); ok && (st == core.ResourceInFlight || st == core.ResourceApplied) {
		t.Fatalf("aws node's last state = %s, must never reach in_flight/applied once its own provider's freshness check finds drift", st)
	}
	foundTerminal := false
	for _, e := range raAWS.Errors {
		if e.Classification == core.ErrorTerminal {
			foundTerminal = true
		}
	}
	if !foundTerminal {
		t.Fatalf("aws node's errors = %+v, want a terminal error recording the staleness", raAWS.Errors)
	}
	if st, _ := raHelm.LastState(); st != core.ResourceApplied {
		t.Fatalf("helm node's last state = %s, want applied -- a sibling node's own drift, against a different provider, must never block or contaminate it", st)
	}

	// aws's own live state (v1-drifted) must be untouched -- refused, not
	// bulldozed -- while helm's own live state genuinely landed the
	// modify.
	liveAWS, _ := awsFake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addrAWS))
	var mAWS map[string]interface{}
	json.Unmarshal(liveAWS, &mAWS)
	if mAWS["value"] != "v1-drifted" {
		t.Fatalf("aws live value = %v, want untouched v1-drifted", mAWS["value"])
	}
	liveHelm, _ := helmFake.ReadResource(context.Background(), nil, "fake_widget", lookupJSON(addrHelm))
	var mHelm map[string]interface{}
	json.Unmarshal(liveHelm, &mHelm)
	if mHelm["value"] != "v2" {
		t.Fatalf("helm live value = %v, want v2 (the modify actually landed)", mHelm["value"])
	}
}
