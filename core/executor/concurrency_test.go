package executor

// UBI-67 session 2, step 3: the permanent replacement for session 1's
// throwaway repro and this session's own step-1 checkpoint
// (parallel_repro_test.go, deleted once the recorder/scheduler landed --
// its job was proving the OLD shape lost data; this file proves the NEW
// one doesn't). Run under `go test -race`, every time, as part of the
// normal suite -- not gated behind a slow/live env var, since it's fully
// hermetic and fast.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestShip_ConcurrentIndependentCreates_NoResourceEverLost is the
// direct, real-entry-point counterpart to session 1's own throwaway
// finding: N independent (no depends_on) creates, with deliberately
// staggered completion timing (half fast, half artificially slow, via
// fakeApplier.scriptApplyDelay -- the same "one slow resource among many
// fast ones" shape as the founder's own SQS-vs-ECR/role/policy/
// attachment live finding that motivated UBI-67 in the first place),
// shipped through the real Ship/shipChange entry point, not an internal
// function called directly. Asserts every single one of N resources
// ends up correctly recorded -- zero lost, zero duplicated -- and that
// independent nodes genuinely run at the same time.
//
// UBI-253: concurrency is proven by a barrier, not inferred from
// elapsed time. The original assertion bounded the whole ship's own
// wall clock, and it failed five times over three months on PRs that
// touched nothing in this package, at 1.01s, 2.17s, 0.86s, 0.457s and
// 0.52s against a 450ms bound. Two of those are within 70ms of the
// bound, so the passing and failing populations overlap: to cover the
// observed spread the bound would have to pass 2.2s, and a serial walk
// of this fixture is >=1.5s, which means no threshold could still tell
// concurrent from serial. Timing was the wrong instrument, not a
// badly-tuned one.
//
// maxParallelShipNodes is what the barrier is sized to, so this asserts
// the scheduler actually reaches its own configured parallelism rather
// than merely exceeding one.
func TestShip_ConcurrentIndependentCreates_NoResourceEverLost(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	const n = 20
	// Sizing the barrier to the scheduler's own cap makes this assert
	// that it reaches its configured parallelism, not merely that it
	// exceeds one. That only means something while the cap is above
	// one: with a cap of 1 the barrier would be a barrier of one, which
	// every scheduler satisfies trivially, including a serial one.
	// Found by probing exactly that, so it is refused rather than
	// passing vacuously.
	if maxParallelShipNodes < 2 {
		t.Fatalf("maxParallelShipNodes = %d -- the scheduler is configured serial, so this test cannot prove concurrency (UBX_SHIP_MAX_PARALLEL?)", maxParallelShipNodes)
	}
	// Every create blocks until maxParallelShipNodes of them are in
	// flight together. A serial scheduler cannot release this, and says
	// so: the first apply reports how many companions ever joined it.
	fake.scriptApplyBarrier(maxParallelShipNodes)

	var creates []json.RawMessage
	addrs := make([]core.Address, n)
	for i := 0; i < n; i++ {
		addr := core.Address{Stack: "payments", Type: "fake_widget", Name: fmt.Sprintf("w%d", i)}
		addrs[i] = addr
		creates = append(creates, changeCreateJSON(t, addr, fmt.Sprintf(`{"value":"v%d"}`, i)))
	}
	p := acceptChange(t, l, "payments", creates)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied: %+v", sealed.Summary.Outcome, sealed.Resources)
	}

	if len(sealed.Resources) != n {
		t.Fatalf("sealed.Resources has %d entries, want %d -- UBI-67 session 1's own finding (concurrent nodes silently lose rec.Resources entries) has regressed", len(sealed.Resources), n)
	}

	seen := make(map[string]int, n)
	for _, ra := range sealed.Resources {
		seen[ra.Address.String()]++
		if st, ok := ra.LastState(); !ok || st != core.ResourceApplied {
			t.Errorf("%s: LastState = %v (ok=%v), want applied", ra.Address, st, ok)
		}
	}
	for _, addr := range addrs {
		if seen[addr.String()] != 1 {
			t.Errorf("%s: appears %d times in sealed.Resources, want exactly 1", addr, seen[addr.String()])
		}
	}
	if sealed.Summary.ResourcesApplied != int64(n) {
		t.Errorf("ResourcesApplied = %d, want %d", sealed.Summary.ResourcesApplied, n)
	}
}

// TestShip_ConcurrentDiamondDependency_NeverStartsAheadOfDependencies
// mirrors the founder's own real platform.md shape more closely than the
// independent-only test above: two independent resources (ecr, sqs)
// feed a THIRD that depends on both (an attachment-like node analogous
// to aws_iam_role_policy_attachment), plus a fourth, wholly independent
// resource that should proceed without waiting for any of them. Proves
// the scheduler's own dependency-ready gating (docs/executor.md's own
// Finding 4: a node must genuinely WAIT for dependency completion, not
// just poll resultsByAddr once) holds under real concurrency: the
// dependent's own applied config must show it received BOTH
// dependencies' real, concrete output (only obtainable if it started
// strictly after both finished), and nothing is lost.
func TestShip_ConcurrentDiamondDependency_NeverStartsAheadOfDependencies(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	ecr := core.Address{Stack: "platform", Type: "fake_widget", Name: "ecr"}
	sqs := core.Address{Stack: "platform", Type: "fake_widget", Name: "sqs"}
	attach := core.Address{Stack: "platform", Type: "fake_widget", Name: "attach"}
	independent := core.Address{Stack: "platform", Type: "fake_widget", Name: "independent"}

	fake.scriptApplyDelay("ecr-value", 80*time.Millisecond)
	fake.scriptApplyDelay("sqs-value", 40*time.Millisecond)

	creates := []json.RawMessage{
		changeCreateJSON(t, ecr, `{"value":"ecr-value"}`),
		changeCreateJSON(t, sqs, `{"value":"sqs-value"}`),
		changeCreateJSON(t, attach, `{"value":{"$computed":{"from":"platform.fake_widget.ecr.id"}}, "combined_with":{"$computed":{"from":"platform.fake_widget.sqs.id"}}}`, ecr.String(), sqs.String()),
		changeCreateJSON(t, independent, `{"value":"independent-value"}`),
	}
	p := acceptChange(t, l, "platform", creates)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied: %+v", sealed.Summary.Outcome, sealed.Resources)
	}
	if len(sealed.Resources) != 4 {
		t.Fatalf("sealed.Resources has %d entries, want 4", len(sealed.Resources))
	}

	byAddr := make(map[string]*core.ResourceApply, 4)
	for _, ra := range sealed.Resources {
		byAddr[ra.Address.String()] = ra
	}
	for _, addr := range []core.Address{ecr, sqs, attach, independent} {
		ra, ok := byAddr[addr.String()]
		if !ok {
			t.Fatalf("%s: missing from sealed.Resources entirely", addr)
		}
		if st, ok := ra.LastState(); !ok || st != core.ResourceApplied {
			t.Fatalf("%s: LastState = %v (ok=%v), want applied", addr, st, ok)
		}
	}

	var ecrResult, sqsResult struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(byAddr[ecr.String()].ProviderResult, &ecrResult); err != nil {
		t.Fatalf("decode ecr result: %v", err)
	}
	if err := json.Unmarshal(byAddr[sqs.String()].ProviderResult, &sqsResult); err != nil {
		t.Fatalf("decode sqs result: %v", err)
	}

	var attachResult struct {
		Value        string `json:"value"`
		CombinedWith string `json:"combined_with"`
	}
	if err := json.Unmarshal(byAddr[attach.String()].ProviderResult, &attachResult); err != nil {
		t.Fatalf("decode attach result: %v", err)
	}
	if attachResult.Value != ecrResult.ID {
		t.Errorf("attach.value = %q, want ecr's real applied id %q -- the dependent started (or resolved its $computed marker) before its dependency's real output was ready", attachResult.Value, ecrResult.ID)
	}
	if attachResult.CombinedWith != sqsResult.ID {
		t.Errorf("attach.combined_with = %q, want sqs's real applied id %q -- the dependent started (or resolved its $computed marker) before its dependency's real output was ready", attachResult.CombinedWith, sqsResult.ID)
	}
}

// TestShip_ApplyBarrier_DetectsASerialScheduler proves the instrument
// the test above depends on actually works.
//
// A concurrency assertion that cannot fail is worse than none, because
// it reads as coverage. UBI-253 replaced a wall-clock bound with a
// barrier, and a barrier's own failure mode (nobody ever arrives) is
// exactly the thing that would be invisible if it were broken: the
// release would simply never be needed. So this pins the scheduler to
// one node at a time, asks for a barrier of two, and requires the ship
// to fail with the barrier's own message.
func TestShip_ApplyBarrier_DetectsASerialScheduler(t *testing.T) {
	restore := maxParallelShipNodes
	maxParallelShipNodes = 1
	defer func() { maxParallelShipNodes = restore }()

	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	fake.scriptApplyBarrier(2)
	// The failure path is the point here, so it is not waited out at its
	// real length.
	fake.barrierTimeout = 250 * time.Millisecond

	var creates []json.RawMessage
	for i := 0; i < 4; i++ {
		addr := core.Address{Stack: "payments", Type: "fake_widget", Name: fmt.Sprintf("w%d", i)}
		creates = append(creates, changeCreateJSON(t, addr, fmt.Sprintf(`{"value":"v%d"}`, i)))
	}
	p := acceptChange(t, l, "payments", creates)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err == nil && sealed.Summary.Outcome == "applied" {
		t.Fatal("a scheduler pinned to one node at a time satisfied a two-way barrier -- the barrier is not actually measuring overlap")
	}

	// The message has to name the real problem, since that is the whole
	// advantage over a timing bound: a number that needs interpreting is
	// what made UBI-253 take five occurrences to diagnose.
	got := ""
	if err != nil {
		got = err.Error()
	}
	for _, ra := range sealed.Resources {
		for _, e := range ra.Errors {
			got += " " + e.Message
		}
	}
	if !strings.Contains(got, "not running concurrently") {
		t.Errorf("failure did not name the cause, got: %s", got)
	}
}
