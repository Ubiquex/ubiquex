package core

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bpSource is the provenance shape blueprint.ExpandCalls stamps onto every
// resource it produces: {kind: "blueprint", ref: "<name>:<content-hash>"}.
func bpSource(ref string) []IntentSource {
	return []IntentSource{{Kind: "blueprint", Ref: ref}}
}

// adoptWithSourcesForTest seeds a resource whose create node carries
// provenance, the shape a blueprint-produced create has had since
// 2026-08-05. adoptForTest (destroy_tombstone_test.go) is the same thing
// without the "sources" key, which is the hand-written shape.
func adoptWithSourcesForTest(t *testing.T, l *Ledger, addr Address, state json.RawMessage, sources []IntentSource) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	node := map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	}
	if len(sources) > 0 {
		node["sources"] = sources
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ObservedHash(state)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := Accept(l, &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindAdoption,
		Intent:        Intent{Summary: "seed " + addr.String()},
		Delta:         Delta{Creates: []json.RawMessage{raw}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta: CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    StatusDraft,
	})
	if err != nil {
		t.Fatalf("adopt %s: %v", addr, err)
	}
	return accepted
}

// shipDeclaredModifyForTest is a kind:change modify that RE-STATES the
// resource's declaration: Provider set, exactly as core/resolver sets it on
// every Modification it produces, and shipped so FoldState actually folds
// it. sources may be nil, which is the hand-written case and the one that
// must CLEAR provenance rather than pass it through.
func shipDeclaredModifyForTest(t *testing.T, l *Ledger, addr Address, before, after map[string]json.RawMessage, sources []IntentSource) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	hash, err := ObservedHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := Accept(l, &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "declared modify " + addr.String()},
		Delta: Delta{Modifies: []Modification{{
			Target:   addr,
			Before:   before,
			After:    after,
			Provider: &ProviderRef{Source: "hashicorp/aws", Version: "5.0.0"},
			Sources:  sources,
		}}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	})
	if err != nil {
		t.Fatalf("accept declared modify: %v", err)
	}
	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec.Resources = append(rec.Resources, &ResourceApply{
		Address: addr,
		Transitions: []Transition{
			{State: ResourcePending, At: now},
			{State: ResourceInFlight, At: now},
			{State: ResourceApplied, At: now},
		},
	})
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := l.SealApply(rec, ApplySummary{StartedAt: now, FinishedAt: now, Outcome: "applied", ResourcesApplied: 1}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

// acceptDriftAdoptForTest records observed drift. It carries no Provider
// and no Sources, exactly as core/scan.go builds it, and folds into state
// the moment it is accepted with no ship of any kind (FoldState's own
// documented record-only rule).
func acceptDriftAdoptForTest(t *testing.T, l *Ledger, addr Address, before, after map[string]json.RawMessage) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	hash, err := ObservedHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := Accept(l, &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindDriftAdopt,
		Intent:        Intent{Summary: "drift " + addr.String()},
		Delta:         Delta{Modifies: []Modification{{Target: addr, Before: before, After: after}}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta: CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		// All-zero, and the ledger's own validator enforces it: a
		// drift_adopt proposes no change, it records one that already
		// happened. Independent corroboration of the distinction
		// restatesDeclaration draws, from a rule written long before it.
		BlastRadius: BlastRadius{},
		Status:      StatusDraft,
	})
	if err != nil {
		t.Fatalf("accept drift adopt: %v", err)
	}
	return accepted
}

func onlyRef(t *testing.T, got []IntentSource) string {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want exactly one source, got %d: %+v", len(got), got)
	}
	return got[0].Ref
}

// TestFoldSources_LaterDeclarationWins is UBI-284's own which-version
// question, answered: a resource created by a blueprint at v1 and
// re-declared by the same blueprint at v2 is, right now, what v2 describes.
// The destroy that removes it should say v2, not v1, for the same reason
// DestroyEntry.State records the folded final state rather than the state
// it was created with.
func TestFoldSources_LaterDeclarationWins(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`), bpSource("queue:sha256:v1"))

	shipDeclaredModifyForTest(t, l, addr,
		map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
		bpSource("queue:sha256:v2"))

	got, found, err := l.FoldSources(addr)
	if err != nil {
		t.Fatalf("fold sources: %v", err)
	}
	if !found {
		t.Fatal("the resource still exists, so its provenance must be findable")
	}
	if ref := onlyRef(t, got); ref != "queue:sha256:v2" {
		t.Errorf("provenance is %q, want the version that describes it now, queue:sha256:v2", ref)
	}
}

// TestFoldSources_DriftAdoptDoesNotErase is the whole reason
// restatesDeclaration exists. A drift adopt says the cloud changed and the
// ledger should record it. It does not say the resource stopped being
// blueprint-managed, and a fold that took provenance from the last
// state-contributing operation regardless would make it say exactly that,
// silently, on every adoption.
func TestFoldSources_DriftAdoptDoesNotErase(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`), bpSource("queue:sha256:v1"))

	acceptDriftAdoptForTest(t, l, addr,
		map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`604800`)})

	// The drift really did fold into state: this is not a test that passes
	// because nothing happened.
	state, _, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !strings.Contains(string(state), "604800") {
		t.Fatalf("the drift did not fold into state, so this test proves nothing: %s", state)
	}

	got, found, err := l.FoldSources(addr)
	if err != nil {
		t.Fatalf("fold sources: %v", err)
	}
	if !found {
		t.Fatal("the resource still exists")
	}
	if ref := onlyRef(t, got); ref != "queue:sha256:v1" {
		t.Errorf("a drift adopt erased provenance, leaving %q: recording an observation is not re-declaring the resource", ref)
	}
}

// TestFoldSources_HandWrittenModifyClearsIt is the other half, and the
// reason "pass through whenever sources are empty" would be wrong. A modify
// that re-states the declaration and names no blueprint IS a statement:
// nothing in a blueprint describes this resource any more.
func TestFoldSources_HandWrittenModifyClearsIt(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`), bpSource("queue:sha256:v1"))

	shipDeclaredModifyForTest(t, l, addr,
		map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
		nil)

	got, found, err := l.FoldSources(addr)
	if err != nil {
		t.Fatalf("fold sources: %v", err)
	}
	if !found {
		t.Fatal("the resource still exists")
	}
	if len(got) != 0 {
		t.Errorf("a hand-written re-declaration left provenance %+v, but nothing declared it any more", got)
	}
}

// TestFoldSources_AgreesWithFoldStateOnWhichOperation is the invariant that
// makes the semantics checkable rather than argued: the sources recorded
// are the sources of the operation that produced the state recorded. The
// two are read from one walk, so this is a guard on that staying true, not
// a reconciliation of two implementations.
//
// Built as a chain that ends on each of the three contributing shapes in
// turn, so an implementation that updated provenance at a point where it
// does not update state (or the reverse) is caught wherever it did it.
func TestFoldSources_AgreesWithFoldStateOnWhichOperation(t *testing.T) {
	type step struct {
		name      string
		apply     func(t *testing.T, l *Ledger, addr Address)
		wantState string // a value only the operation that last set state could have left
		wantRef   string // "" meaning provenance must be empty
	}
	steps := []step{
		{
			name:      "create seeds both",
			apply:     func(t *testing.T, l *Ledger, addr Address) {},
			wantState: "86400",
			wantRef:   "queue:sha256:v1",
		},
		{
			name: "declared modify moves both",
			apply: func(t *testing.T, l *Ledger, addr Address) {
				shipDeclaredModifyForTest(t, l, addr,
					map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
					map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
					bpSource("queue:sha256:v2"))
			},
			wantState: "259200",
			wantRef:   "queue:sha256:v2",
		},
		{
			name: "drift moves state alone",
			apply: func(t *testing.T, l *Ledger, addr Address) {
				shipDeclaredModifyForTest(t, l, addr,
					map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
					map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
					bpSource("queue:sha256:v2"))
				acceptDriftAdoptForTest(t, l, addr,
					map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
					map[string]json.RawMessage{"retention": json.RawMessage(`604800`)})
			},
			wantState: "604800",
			// Still v2: the drift is the last operation to touch state, and
			// v2 is the last to touch the declaration. This is the one case
			// where the two readings legitimately come from different
			// operations, and it is exactly the case the semantics are for.
			wantRef: "queue:sha256:v2",
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			l := Open(t.TempDir())
			addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
			adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`), bpSource("queue:sha256:v1"))
			s.apply(t, l, addr)

			state, stateFound, err := l.FoldState(addr)
			if err != nil {
				t.Fatalf("fold state: %v", err)
			}
			sources, srcFound, err := l.FoldSources(addr)
			if err != nil {
				t.Fatalf("fold sources: %v", err)
			}
			if stateFound != srcFound {
				t.Fatalf("the two readings disagree on whether the resource exists: state %v, sources %v", stateFound, srcFound)
			}
			if !strings.Contains(string(state), s.wantState) {
				t.Errorf("state is %s, want it to carry %s", state, s.wantState)
			}
			switch s.wantRef {
			case "":
				if len(sources) != 0 {
					t.Errorf("want no provenance, got %+v", sources)
				}
			default:
				if ref := onlyRef(t, sources); ref != s.wantRef {
					t.Errorf("provenance is %q, want %q", ref, s.wantRef)
				}
			}
		})
	}
}

// TestFoldSources_ShippedDestroyClearsBoth: once a destroy ships the
// address is gone, and provenance goes with it rather than outliving the
// resource it described.
func TestFoldSources_ShippedDestroyClearsBoth(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1"}`), bpSource("queue:sha256:v1"))
	shipChangeDestroyForTest(t, l, addr, json.RawMessage(`{"id":"q-1"}`), "applied", true, true)

	sources, found, err := l.FoldSources(addr)
	if err != nil {
		t.Fatalf("fold sources: %v", err)
	}
	if found || len(sources) != 0 {
		t.Errorf("provenance outlived the resource: found=%v sources=%+v", found, sources)
	}
}

// TestProviderDiscriminatesRestatement pins the inference restatesDeclaration
// depends on.
//
// Nothing in the schema says "this entry re-states a declaration". The fold
// reads Provider instead, which holds because a record-only modify has
// nothing to apply and so no provider to apply it with. That is a property
// of the producers, not of the type, so it can only be enforced where
// producers are: every construction site of a Modification either sets
// Provider, and is therefore a declaration, or is listed here as
// deliberately record-only.
//
// A new producer that sets neither fails this test rather than silently
// folding on the wrong side, which is the failure this exists to prevent.
func TestProviderDiscriminatesRestatement(t *testing.T) {
	recordOnly := map[string]string{
		"core/scan.go": "drift_adopt and drift_revert: observations, not declarations",
	}

	root := ".."
	fset := token.NewFileSet()
	var bare []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// sdk/ is the generated multi-language monorepo and has its own
			// vendored trees; nothing there produces a core.Modification.
			if n := d.Name(); n == ".git" || n == "sdk" || n == "testdata" || n == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not our concern here; the build already covers parseability
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			name := ""
			switch t := lit.Type.(type) {
			case *ast.Ident:
				name = t.Name
			case *ast.SelectorExpr:
				name = t.Sel.Name
			}
			if name != "Modification" {
				return true
			}
			// A zero literal is an error-path return value, not a producer
			// of anything that reaches a ledger.
			if len(lit.Elts) == 0 {
				return true
			}
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Provider" {
					return true // declares itself a declaration
				}
			}
			if _, known := recordOnly[filepath.ToSlash(rel)]; known {
				return true
			}
			bare = append(bare, fset.Position(lit.Pos()).String())
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(bare) > 0 {
		t.Errorf(`these build a Modification without Provider, from a file not listed as record-only:
  %s

core.Ledger.FoldSources reads Provider to decide whether an entry re-states
the resource's declaration (and so replaces its provenance) or merely
records something observed (and so passes it through). A producer that sets
neither will be folded as record-only. If that is right, add the file to
recordOnly above with the reason. If it is not, set Provider.`, strings.Join(bare, "\n  "))
	}
}

// TestFoldSourcesAt_ReadsTheEarlierHead is the property restore depends
// on: provenance as of a past head, not as of now.
//
// Without it, restore could only have asked for current provenance, which
// for a resource whose declaration has since moved is the wrong answer,
// and for one since destroyed is no answer at all.
func TestFoldSourcesAt_ReadsTheEarlierHead(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	seed := adoptWithSourcesForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`), bpSource("queue:sha256:v1"))

	shipDeclaredModifyForTest(t, l, addr,
		map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
		bpSource("queue:sha256:v2"))

	// Now: v2.
	now, found, err := l.FoldSources(addr)
	if err != nil || !found {
		t.Fatalf("fold sources: found=%v err=%v", found, err)
	}
	if ref := onlyRef(t, now); ref != "queue:sha256:v2" {
		t.Fatalf("current provenance = %q, want v2", ref)
	}

	// As of the seed head: v1, which is what a restore to that head has
	// to record, so the provenance it writes describes the same moment as
	// the config it writes.
	then, found, err := l.FoldSourcesAt(seed.ID, addr)
	if err != nil || !found {
		t.Fatalf("fold sources at: found=%v err=%v", found, err)
	}
	if ref := onlyRef(t, then); ref != "queue:sha256:v1" {
		t.Fatalf("provenance at the earlier head = %q, want v1", ref)
	}
}
