package resolver

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// seedLedgerWithLookup mirrors seedLedger (above) but also records a
// resolution.inputs Lookup -- destroys[] specifically needs one (docs/schema.md's
// "Amendment: destroys" requires resolution.inputs["destroy_target"].Lookup
// non-empty), which seedLedger's own plain adoption fixture never set
// because no test needed it before this one.
func seedLedgerWithLookup(t *testing.T, l *core.Ledger, addr core.Address, state, lookup string) {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	})
	if err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("seed ledger: head: %v", err)
	}
	hash, err := core.ObservedHash(json.RawMessage(state))
	if err != nil {
		t.Fatalf("seed ledger: observed hash: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "seed " + addr.String()},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(lookup)},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("seed ledger: accept: %v", err)
	}
}

// recordHistoricalDependsOn hand-builds and accepts a minimal kind:"change"
// proposal recording a single Modification of dependerAddr whose DependsOn
// names dependeeAddr -- the same "hand-build via Accept directly" fixture
// convention core/ubi29_test.go already established for core package tests
// needing a specific ledger shape without going through the full resolve/
// ship pipeline. Deliberately NOT built via a real create-with-$ref
// resolve: a change-proposal CREATE is only foldable by FoldState once it
// has actually shipped (UBI-29), which these tests have no need to
// exercise -- Delta.Modifies carries no such gating, so recording the edge
// directly here tests resolveDestroys' own depends_on walk in isolation,
// not the unrelated shipped-create discovery mechanism.
func recordHistoricalDependsOn(t *testing.T, l *core.Ledger, dependerAddr, dependeeAddr core.Address) {
	t.Helper()
	current, found, err := l.FoldState(dependerAddr)
	if err != nil || !found {
		t.Fatalf("recordHistoricalDependsOn: %s must already be seeded: found=%v err=%v", dependerAddr, found, err)
	}
	hash, err := core.ObservedHash(current)
	if err != nil {
		t.Fatalf("recordHistoricalDependsOn: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("recordHistoricalDependsOn: head: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         dependerAddr.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "record depends_on " + dependerAddr.String() + " -> " + dependeeAddr.String()},
		Delta: core.Delta{
			Modifies: []core.Modification{{
				Target:    dependerAddr,
				DependsOn: []string{dependeeAddr.String()},
			}},
		},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: dependerAddr.String(), ObservedHash: hash},
			},
		},
		CostDelta:   core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{Modifies: 1},
		Status:      core.StatusDraft,
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("recordHistoricalDependsOn: accept: %v", err)
	}
}

// --- happy paths ------------------------------------------------------------

func TestResolve_Destroy_Basic(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999","cidr_block":"10.0.0.0/16"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Destroys) != 1 {
		t.Fatalf("destroys = %d, want 1", len(p.Delta.Destroys))
	}
	d := p.Delta.Destroys[0]
	if d.Address != addr {
		t.Fatalf("destroy address = %v, want %v", d.Address, addr)
	}
	var state map[string]interface{}
	json.Unmarshal(d.State, &state)
	if state["cidr_block"] != "10.0.0.0/16" {
		t.Fatalf("destroy state = %v, want full folded state carried inline", state)
	}
	if len(d.DependsOn) != 0 {
		t.Fatalf("depends_on = %v, want none (nothing else references this address)", d.DependsOn)
	}
	if p.BlastRadius.Destroys != 1 {
		t.Fatalf("blast_radius.destroys = %d, want 1", p.BlastRadius.Destroys)
	}

	var destroyInput, orphanInput *core.ResolutionInput
	for i := range p.Resolution.Inputs {
		in := &p.Resolution.Inputs[i]
		switch in.Kind {
		case "destroy_target":
			destroyInput = in
		case "cross_stack_orphan_check":
			orphanInput = in
		}
	}
	if destroyInput == nil {
		t.Fatal("no destroy_target resolution input recorded")
	}
	if destroyInput.ObservedHash == "" {
		t.Fatal("destroy_target observed_hash is empty")
	}
	var gotLookup, wantLookup map[string]interface{}
	json.Unmarshal(destroyInput.Lookup, &gotLookup)
	json.Unmarshal([]byte(`{"id":"vpc-999"}`), &wantLookup)
	if gotLookup["id"] != wantLookup["id"] {
		t.Fatalf("destroy_target lookup = %s, want the recorded lookup carried forward ({\"id\":\"vpc-999\"})", destroyInput.Lookup)
	}
	if orphanInput == nil || orphanInput.Status != "not_performed" {
		t.Fatalf("cross_stack_orphan_check = %+v, want status \"not_performed\" (no known_dependents given)", orphanInput)
	}

	if err := core.Validate(p); err != nil {
		t.Fatalf("resolved destroy proposal should validate cleanly: %v", err)
	}
}

func TestResolve_DestroyMutualBatch_ReversedOrder(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	mainAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	dbAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedgerWithLookup(t, l, mainAddr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)
	seedLedgerWithLookup(t, l, dbAddr, `{"id":"db-1","vpc_ref":"vpc-123"}`, `{"id":"db-1"}`)
	// db historically depends on main -- a real recorded depends_on edge,
	// exactly the shape a same-batch $ref would have produced.
	recordHistoricalDependsOn(t, l, dbAddr, mainAddr)

	// Now destroy BOTH main and db together -- a mutual destroy, not an
	// orphan: db must be destroyed before main (it still historically
	// depends on it up until its own destruction).
	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_db_instance.db", "payments.aws_vpc.main"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve destroy: %v", err)
	}
	if len(p.Delta.Destroys) != 2 {
		t.Fatalf("destroys = %d, want 2", len(p.Delta.Destroys))
	}
	if p.Delta.Destroys[0].Address.Name != "db" || p.Delta.Destroys[1].Address.Name != "main" {
		t.Fatalf("destroy order = %s, %s -- want db before main (reversed dependency order)",
			p.Delta.Destroys[0].Address.Name, p.Delta.Destroys[1].Address.Name)
	}
	mainDeps := p.Delta.Destroys[1].DependsOn
	if len(mainDeps) != 1 || mainDeps[0] != "payments.aws_db_instance.db" {
		t.Fatalf("main's depends_on = %v, want [payments.aws_db_instance.db]", mainDeps)
	}
	if len(p.Delta.Destroys[0].DependsOn) != 0 {
		t.Fatalf("db's depends_on = %v, want none", p.Delta.Destroys[0].DependsOn)
	}
}

func TestResolve_DestroyHandledBySameBatchModify_NotOrphaned(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	mainAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	dbAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedgerWithLookup(t, l, mainAddr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)
	seedLedger(t, l, dbAddr, `{"id":"db-1","instance_class":"db.t3.medium","vpc_ref":"vpc-123"}`)
	recordHistoricalDependsOn(t, l, dbAddr, mainAddr)

	// This batch modifies "db" (the historical dependent) but does not
	// destroy it -- "handled," not orphaned: main's destroy must simply wait
	// for db's own modify to complete first.
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpModify, `{"instance_class":"db.t3.large","vpc_ref":"vpc-123"}`),
	)
	intent.Destroys = []string{"payments.aws_vpc.main"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Destroys) != 1 {
		t.Fatalf("destroys = %d, want 1", len(p.Delta.Destroys))
	}
	deps := p.Delta.Destroys[0].DependsOn
	if len(deps) != 1 || deps[0] != "payments.aws_db_instance.db" {
		t.Fatalf("depends_on = %v, want [payments.aws_db_instance.db] (the same-batch modify must run first)", deps)
	}
}

// TestResolve_DestroyNoLongerOrphaned_AfterDependentRepointed reproduces a
// real bug found while building the real CLI transcripts for ubiquex-docs
// (UBI-30 session 2): historicalReverseDependents originally accumulated
// EVERY historical depends_on mention forever, so a destroy stayed refused
// even after its one-time dependent had genuinely been repointed away by a
// later, separate modify -- exactly the case "handled" (the same-batch
// variant) exists to allow, just split across two proposals instead of
// one. Fixed by tracking each address's own MOST RECENTLY recorded
// depends_on only.
func TestResolve_DestroyNoLongerOrphaned_AfterDependentRepointed(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	mainAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	dbAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedgerWithLookup(t, l, mainAddr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)
	seedLedger(t, l, dbAddr, `{"id":"db-1","vpc_ref":"vpc-123"}`)
	recordHistoricalDependsOn(t, l, dbAddr, mainAddr)

	// Confirmed refused first, matching TestResolve_DestroyOrphaned_Rejected.
	destroyIntent := intentFile("payments")
	destroyIntent.Destroys = []string{"payments.aws_vpc.main"}
	if _, err := Resolve(l, singleProvider(schema), destroyIntent, nil); !errors.Is(err, ErrDestroyOrphaned) {
		t.Fatalf("got %v, want ErrDestroyOrphaned before repointing", err)
	}

	// db is repointed away, in its OWN separate proposal -- a real modify
	// whose resolved depends_on no longer names main at all.
	repoint := intentFile("payments",
		ri("aws_db_instance", "db", OpModify, `{"vpc_ref":"vpc-999-elsewhere"}`),
	)
	repointResolved, err := Resolve(l, singleProvider(schema), repoint, nil)
	if err != nil {
		t.Fatalf("resolve repoint: %v", err)
	}
	if len(repointResolved.Delta.Modifies[0].DependsOn) != 0 {
		t.Fatalf("repoint depends_on = %v, want none", repointResolved.Delta.Modifies[0].DependsOn)
	}
	if _, err := core.Accept(l, repointResolved); err != nil {
		t.Fatalf("accept repoint: %v", err)
	}

	// Now the destroy must succeed -- db's own MOST RECENT depends_on (from
	// the repoint proposal) no longer names main, even though an OLDER
	// proposal once recorded that it did.
	if _, err := Resolve(l, singleProvider(schema), destroyIntent, nil); err != nil {
		t.Fatalf("resolve destroy after repoint: %v, want success", err)
	}
}

func TestResolve_CrossStackOrphanCheck_CheckedClear(t *testing.T) {
	l := core.Open(t.TempDir())
	neighbor := core.Open(t.TempDir())
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "unrelated"}, `{"id":"vpc-1"}`)

	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, []string{neighbor.Dir()})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var orphanInput *core.ResolutionInput
	for i := range p.Resolution.Inputs {
		if p.Resolution.Inputs[i].Kind == "cross_stack_orphan_check" {
			orphanInput = &p.Resolution.Inputs[i]
		}
	}
	if orphanInput == nil || orphanInput.Status != "checked_clear" {
		t.Fatalf("cross_stack_orphan_check = %+v, want status \"checked_clear\"", orphanInput)
	}
	if len(orphanInput.CheckedLedgerDirs) != 1 || orphanInput.CheckedLedgerDirs[0] != neighbor.Dir() {
		t.Fatalf("checked_ledger_dirs = %v, want [%s]", orphanInput.CheckedLedgerDirs, neighbor.Dir())
	}
}

// --- rejections --------------------------------------------------------------

func TestResolve_DestroyTargetMissing_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.ghost"}

	_, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if !errors.Is(err, ErrDestroyTargetMissing) {
		t.Fatalf("got %v, want ErrDestroyTargetMissing", err)
	}
}

func TestResolve_DuplicateDestroy_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old", "payments.aws_vpc.old"}

	_, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if !errors.Is(err, ErrDuplicateDestroy) {
		t.Fatalf("got %v, want ErrDuplicateDestroy", err)
	}
}

func TestResolve_DestroyResourceConflict_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)

	intent := intentFile("payments",
		ri("aws_vpc", "main", OpModify, `{"cidr_block":"10.0.0.0/16"}`),
	)
	intent.Destroys = []string{"payments.aws_vpc.main"}

	_, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if !errors.Is(err, ErrDestroyResourceConflict) {
		t.Fatalf("got %v, want ErrDestroyResourceConflict", err)
	}
}

func TestResolve_RefToDestroyTarget_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_ref":{"$ref":{"to":"payments.aws_vpc.old.id"}}}`),
	)
	intent.Destroys = []string{"payments.aws_vpc.old"}

	_, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if !errors.Is(err, ErrRefToDestroyTarget) {
		t.Fatalf("got %v, want ErrRefToDestroyTarget", err)
	}
}

// recordUnrelatedDriftAdopt hand-builds and accepts a minimal
// kind:"drift_adopt" proposal recording an out-of-band correction for
// addr -- unrelated to any dependency relationship, and (like every
// drift_adopt) never carrying a DependsOn value at all, since that field
// is a resolver-produced, change-proposal-only concept
// (docs/schema.md's own amendment). Used to reproduce a real bug found
// live (UBI-30's own AWS finale): historicalReverseDependents originally
// folded ALL proposal kinds' own Delta.Modifies into its
// most-recently-touched map, so this drift_adopt's own empty DependsOn
// silently overwrote and erased a real dependency a PRIOR change
// proposal had recorded for the exact same address.
func recordUnrelatedDriftAdopt(t *testing.T, l *core.Ledger, addr core.Address) {
	t.Helper()
	current, found, err := l.FoldState(addr)
	if err != nil || !found {
		t.Fatalf("recordUnrelatedDriftAdopt: %s must already be seeded: found=%v err=%v", addr, found, err)
	}
	hash, err := core.ObservedHash(current)
	if err != nil {
		t.Fatalf("recordUnrelatedDriftAdopt: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("recordUnrelatedDriftAdopt: head: %v", err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindDriftAdopt,
		Intent:        core.Intent{Summary: "unrelated out-of-band correction for " + addr.String()},
		Delta: core.Delta{
			Modifies: []core.Modification{{Target: addr}},
		},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("recordUnrelatedDriftAdopt: accept: %v", err)
	}
}

// TestResolve_DestroyOrphaned_SurvivesInterveningDriftAdopt reproduces the
// exact bug found live during UBI-30's own AWS finale: destroying a queue
// alone, after its dependent policy had a totally unrelated drift_adopt
// correction recorded against it (e.g. an out-of-band tag change) since
// its own original create, was wrongly ALLOWED -- the drift_adopt's own
// empty DependsOn overwrote the real dependency the original change
// proposal had recorded. Orphan protection must survive an intervening
// proposal of a kind that was never in a position to say anything
// meaningful about depends_on in the first place.
func TestResolve_DestroyOrphaned_SurvivesInterveningDriftAdopt(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	mainAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	dbAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedgerWithLookup(t, l, mainAddr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)
	seedLedger(t, l, dbAddr, `{"id":"db-1","vpc_ref":"vpc-123"}`)
	recordHistoricalDependsOn(t, l, dbAddr, mainAddr)

	// An unrelated out-of-band correction to db, recorded well after the
	// real dependency above -- must not erase it.
	recordUnrelatedDriftAdopt(t, l, dbAddr)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.main"}
	if _, err := Resolve(l, singleProvider(schema), intent, nil); !errors.Is(err, ErrDestroyOrphaned) {
		t.Fatalf("got %v, want ErrDestroyOrphaned -- db's real dependency on main must survive the intervening drift_adopt", err)
	}
}

func TestResolve_DestroyOrphaned_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	mainAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	dbAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedgerWithLookup(t, l, mainAddr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)
	seedLedger(t, l, dbAddr, `{"id":"db-1","vpc_ref":"vpc-123"}`)
	// "db" historically depends on "main" -- a real recorded depends_on
	// edge, exactly like the mutual-batch test above.
	recordHistoricalDependsOn(t, l, dbAddr, mainAddr)

	// Destroying "main" alone, touching nothing else: "db" still exists and
	// still (historically) depends on it -- refused, not silently allowed.
	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.main"}

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrDestroyOrphaned) {
		t.Fatalf("got %v, want ErrDestroyOrphaned", err)
	}
}

func TestResolve_CrossStackOrphanCheck_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-123"}`, `{"id":"vpc-123"}`)

	neighbor := core.Open(t.TempDir())
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "other"}, `{"id":"vpc-456"}`)
	crossIntent := intentFile("networking",
		ri("aws_db_instance", "consumer", OpCreate, `{"vpc_cidr":{"$cross":{"ledger_dir":"`+l.Dir()+`","to":"payments.aws_vpc.main.id"}}}`),
	)
	crossResolved, err := Resolve(neighbor, singleProvider(schema), crossIntent, nil)
	if err != nil {
		t.Fatalf("resolve cross-stack consumer: %v", err)
	}
	if _, err := core.Accept(neighbor, crossResolved); err != nil {
		t.Fatalf("accept cross-stack consumer: %v", err)
	}

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.main"}

	_, err = Resolve(l, singleProvider(schema), intent, []string{neighbor.Dir()})
	if !errors.Is(err, ErrDestroyOrphaned) {
		t.Fatalf("got %v, want ErrDestroyOrphaned (a real cross-stack pin exists)", err)
	}
}

func TestResolve_CrossStackOrphanCheck_DependentLedgerMissing_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	_, err := Resolve(l, singleProvider(newFakeSchema()), intent, []string{t.TempDir() + "/never-initialized"})
	if !errors.Is(err, ErrDependentLedgerMissing) {
		t.Fatalf("got %v, want ErrDependentLedgerMissing", err)
	}
}

// --- validate.go: change proposals may now carry destroys (UBI-30) ---------

func TestValidate_ChangeProposalWithDestroys_Accepted(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("a resolver-produced destroy proposal must validate cleanly: %v", err)
	}
}

func TestValidate_ChangeProposalDestroysBlastRadiusMismatch_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	p.BlastRadius.Destroys = 0
	if err := core.Validate(p); !errors.Is(err, core.ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal for a blast_radius.destroys mismatch", err)
	}
}

func TestValidate_DestroyMissingResolutionInput_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Strip the destroy_target resolution input a real resolve would have
	// recorded -- simulating a hand-tampered proposal.
	var kept []core.ResolutionInput
	for _, in := range p.Resolution.Inputs {
		if in.Kind != "destroy_target" {
			kept = append(kept, in)
		}
	}
	p.Resolution.Inputs = kept

	if err := core.Validate(p); !errors.Is(err, core.ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal for a destroy with no matching resolution input", err)
	}
}
