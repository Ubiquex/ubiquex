package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// UBI-57 Part 2's own CLI-level rendering coverage -- the underlying
// multi-hop walk logic itself is proven hermetically, in depth, at
// core/resolver/pinchain_test.go (three real ledgers, per-hop
// staleness, the actual "does an indirect ancestor's staleness block
// the proposal" semantics question). These tests prove the THIN
// rendering layer on top: `ubx why`/`ubx addresses` actually call
// WalkPinChain and show its result, and (just as important) stay
// completely silent for the overwhelmingly common case of no
// cross-stack pin at all -- no regression, no new noise.
//
// Git-local ledgers throughout (core.Open(dir), matching addresses_test.go's
// own TestAddresses_CrossStack_ViaExternalOverride precedent) --
// cross-stack pins work identically for a git-local neighbor via
// "ledger_dir", no remote-store machinery needed for this.

// seedLedgerWithPinForTest hand-builds and accepts an adoption-shaped
// proposal carrying a real cross_stack_pin resolution input alongside
// its own live_state one -- the cli-package mirror of core/resolver/
// pinchain_test.go's own seedLedgerWithPin (unexported there, so
// rebuilt here from the same exported primitives; see that file's own
// doc comment for exactly why an adoption shape, not a real Resolve
// call, is what makes FoldState see the neighbor's resource
// immediately).
func seedLedgerWithPinForTest(t *testing.T, l *core.Ledger, addr core.Address, state, pinResource, pinLedgerDir, pinHead string) *core.Proposal {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := core.ObservedHash(json.RawMessage(state))
	if err != nil {
		t.Fatal(err)
	}
	lookup := core.DeriveLookupFromResult(json.RawMessage(state), nil)
	inputs := []core.ResolutionInput{
		{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: lookup},
	}
	if pinLedgerDir != "" {
		inputs = append(inputs, core.ResolutionInput{Kind: "cross_stack_pin", Resource: pinResource, LedgerDir: pinLedgerDir, PinnedHead: pinHead})
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "seed " + addr.String()},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution:    core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: inputs},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("seed ledger with pin: accept: %v", err)
	}
	return accepted
}

func TestWhy_PinChain_TwoHops_Rendered(t *testing.T) {
	securityDir := t.TempDir()
	security := core.Open(securityDir)
	seedLedgerWithPinForTest(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "shared"}, `{"id":"role-abc"}`, "", "", "")
	securityHead, err := security.Head()
	if err != nil {
		t.Fatal(err)
	}

	networkingDir := t.TempDir()
	networking := core.Open(networkingDir)
	seedLedgerWithPinForTest(t, networking, core.Address{Stack: "networking", Type: "aws_db_instance", Name: "db"}, `{"id":"db-123"}`,
		"networking.aws_db_instance.db", securityDir, securityHead)
	networkingHead, err := networking.Head()
	if err != nil {
		t.Fatal(err)
	}

	paymentsDir := t.TempDir()
	payments := core.Open(paymentsDir)
	pA := seedLedgerWithPinForTest(t, payments, core.Address{Stack: "payments", Type: "aws_rds_cluster", Name: "main"}, `{"id":"cluster-1"}`,
		"payments.aws_rds_cluster.main", networkingDir, networkingHead)

	out, err := runUbx(t, nil, "why", pA.ID, "--ledger-dir", paymentsDir)
	if err != nil {
		t.Fatalf("why: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pin chain (2 hops)") {
		t.Fatalf("expected a 2-hop pin chain rendered, got:\n%s", out)
	}
	if !strings.Contains(out, "payments.aws_rds_cluster.main") || !strings.Contains(out, "networking.aws_db_instance.db") {
		t.Fatalf("expected both real hop resources named, got:\n%s", out)
	}
	if strings.Contains(out, "STALE") {
		t.Fatalf("nothing has moved -- expected no STALE marker, got:\n%s", out)
	}
}

func TestWhy_PinChain_IndirectAncestorStale_ShownButNotBlocking(t *testing.T) {
	securityDir := t.TempDir()
	security := core.Open(securityDir)
	seedLedgerWithPinForTest(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "shared"}, `{"id":"role-abc"}`, "", "", "")
	securityHead, err := security.Head()
	if err != nil {
		t.Fatal(err)
	}

	networkingDir := t.TempDir()
	networking := core.Open(networkingDir)
	seedLedgerWithPinForTest(t, networking, core.Address{Stack: "networking", Type: "aws_db_instance", Name: "db"}, `{"id":"db-123"}`,
		"networking.aws_db_instance.db", securityDir, securityHead)
	networkingHead, err := networking.Head()
	if err != nil {
		t.Fatal(err)
	}

	paymentsDir := t.TempDir()
	payments := core.Open(paymentsDir)
	pA := seedLedgerWithPinForTest(t, payments, core.Address{Stack: "payments", Type: "aws_rds_cluster", Name: "main"}, `{"id":"cluster-1"}`,
		"payments.aws_rds_cluster.main", networkingDir, networkingHead)

	// security genuinely advances -- networking itself is untouched.
	seedLedgerWithPinForTest(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "second"}, `{"id":"role-def"}`, "", "", "")

	out, err := runUbx(t, nil, "why", pA.ID, "--ledger-dir", paymentsDir)
	if err != nil {
		t.Fatalf("why: %v\n%s", err, out)
	}
	if !strings.Contains(out, "STALE") {
		t.Fatalf("expected the indirect ancestor's own staleness to be shown, got:\n%s", out)
	}
	if !strings.Contains(out, "informational only") {
		t.Fatalf("expected the explicit non-blocking note, got:\n%s", out)
	}
}

func TestWhy_PinChain_NoCrossStackPin_SilentAsBefore(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	p := shipFakeWidgetCreate(t, l, core.Address{Stack: "payments", Type: "fake_widget", Name: "solo"}, "w-1")

	out, err := runUbx(t, nil, "why", p.ID, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("why: %v\n%s", err, out)
	}
	if strings.Contains(out, "pin chain") {
		t.Fatalf("expected no pin chain output at all -- no cross-stack pin exists, got:\n%s", out)
	}
}

func TestAddresses_PinChain_Annotated(t *testing.T) {
	securityDir := t.TempDir()
	security := core.Open(securityDir)
	seedLedgerWithPinForTest(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "shared"}, `{"id":"role-abc"}`, "", "", "")
	securityHead, err := security.Head()
	if err != nil {
		t.Fatal(err)
	}

	networkingDir := t.TempDir()
	networking := core.Open(networkingDir)
	seedLedgerWithPinForTest(t, networking, core.Address{Stack: "networking", Type: "aws_db_instance", Name: "db"}, `{"id":"db-123"}`,
		"networking.aws_db_instance.db", securityDir, securityHead)
	networkingHead, err := networking.Head()
	if err != nil {
		t.Fatal(err)
	}

	paymentsDir := t.TempDir()
	payments := core.Open(paymentsDir)
	seedLedgerWithPinForTest(t, payments, core.Address{Stack: "payments", Type: "aws_rds_cluster", Name: "main"}, `{"id":"cluster-1"}`,
		"payments.aws_rds_cluster.main", networkingDir, networkingHead)

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", paymentsDir)
	if err != nil {
		t.Fatalf("addresses: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pinned via a 2-hop cross-stack chain") {
		t.Fatalf("expected the 2-hop annotation, got:\n%s", out)
	}
}

func TestAddresses_PinChain_NoCrossStackPin_SilentAsBefore(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	shipFakeWidgetCreate(t, l, core.Address{Stack: "payments", Type: "fake_widget", Name: "solo"}, "w-1")

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("addresses: %v\n%s", err, out)
	}
	if strings.Contains(out, "pinned via") {
		t.Fatalf("expected no pin annotation at all -- no cross-stack pin exists, got:\n%s", out)
	}
}
