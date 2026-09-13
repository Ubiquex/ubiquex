package resolver

import (
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// An inferred modify comes from a describe-only program, which emits
// what it sets and says nothing about the rest. Every attribute it did
// not mention used to become a Before-only path, which this codebase
// models as a deletion, so a plan to change one thing proposed removing
// everything the provider had filled in (UBI-267).
//
// The ledger state here is what a create really leaves behind: the
// author's own attribute, a schema-Computed one, and two the provider
// defaulted that the program never mentioned.
const dlqLedgerState = `{"id":"dlq-1","instance_class":"db.t3.large","visibility_timeout":30,"maximum_message_size":1048576}`

func seedDLQ(t *testing.T) (*core.Ledger, core.Address) {
	t.Helper()
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	seedLedger(t, l, addr, dlqLedgerState)
	return l, addr
}

func TestInferredModify_PreservesAttributesTheProgramNeverSet(t *testing.T) {
	l, _ := seedDLQ(t)

	// The program, unchanged: it sets instance_class and nothing else.
	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertNotRemoved(t, p, "visibility_timeout", "maximum_message_size")
}

// assertNotRemoved fails if any modify in the proposal plans to remove
// one of the named attributes, which is a Before entry with no matching
// After.
//
// Written over the whole proposal rather than one modify on purpose: an
// unchanged resource now produces no modify at all, and the property
// that matters is that nothing anywhere plans the removal, which stays
// true either way.
func assertNotRemoved(t *testing.T, p *core.Proposal, attrs ...string) {
	t.Helper()
	for _, m := range p.Delta.Modifies {
		for _, attr := range attrs {
			if _, before := m.Before[attr]; !before {
				continue
			}
			if _, after := m.After[attr]; !after {
				t.Errorf("%s is planned for removal on %s, but the program never set it: before=%v after=%v",
					attr, m.Target, m.Before, m.After)
			}
		}
	}
}

// Preserving must not mean ignoring. An attribute the program does
// change still diffs, or the fix would have replaced a dangerous plan
// with a useless one.
func TestInferredModify_StillDiffsWhatTheProgramDoesChange(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.m5.xlarge"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("want one modify, got %d", len(p.Delta.Modifies))
	}
	m := p.Delta.Modifies[0]
	if _, ok := m.After["instance_class"]; !ok {
		t.Errorf("the attribute the program actually changed is missing from the diff: after=%v", m.After)
	}
	// And only that one.
	for k := range m.After {
		if k != "instance_class" {
			t.Errorf("unrelated attribute %q appeared in the diff: after=%v", k, m.After)
		}
	}
}

// An attribute adopted from live state is the case that settled this.
// `ubx scan` folds console changes into the ledger, so preserving them
// is the difference between a tool that records reality and one that
// silently reverts it.
func TestInferredModify_PreservesAnAttributeAdoptedFromLiveState(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	// Someone changed this in the console and `ubx scan` adopted it.
	seedLedger(t, l, addr, `{"id":"dlq-1","instance_class":"db.t3.large","backup_retention_period":35}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
	)
	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertNotRemoved(t, p, "backup_retention_period")
	_ = addr
}

// A hand-written modify keeps today's behaviour exactly: the author
// supplies a full desired end-state, so an omission really does mean
// remove, and only schema-Computed attributes are backfilled.
func TestHandWrittenModify_OmissionStillMeansRemove(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpModify, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("want one modify, got %d", len(p.Delta.Modifies))
	}
	m := p.Delta.Modifies[0]
	removed := 0
	for _, attr := range []string{"visibility_timeout", "maximum_message_size"} {
		if _, ok := m.Before[attr]; ok {
			removed++
		}
	}
	if removed != 2 {
		t.Errorf("a hand-written modify no longer treats an omission as a removal: before=%v after=%v", m.Before, m.After)
	}
}

// An empty modify is not cosmetic: shipModifyNode has no no-op branch,
// so it runs the full read/plan/apply path, and CCAPI rejects an update
// with nothing to update. That failed a whole ship and blocked every
// resource behind it (UBI-267).
func TestInferredModify_UnchangedResourceProducesNoEntryAtAll(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 0 {
		t.Errorf("an unchanged resource still produced a modify: %+v", p.Delta.Modifies)
	}
	// The resolution input goes with it, or core.Validate refuses the
	// proposal for having a modify with no matching input.
	for _, in := range p.Resolution.Inputs {
		if in.Kind == "live_state" {
			t.Errorf("a live_state input survived its dropped modify: %+v", in)
		}
	}
	if p.BlastRadius.Modifies != 0 {
		t.Errorf("blast radius still counts the dropped modify: %d", p.BlastRadius.Modifies)
	}
}

// A document where every resource is unchanged resolves to nothing at
// all. Nothing had produced that shape before, so it is asserted rather
// than assumed.
func TestInferredModify_EverythingUnchangedIsAValidZeroDeltaProposal(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if n := len(p.Delta.Creates) + len(p.Delta.Modifies) + len(p.Delta.Destroys); n != 0 {
		t.Fatalf("want a zero delta, got %d entries", n)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("core.Validate refuses a zero-delta proposal: %v", err)
	}
}

// Dropping must not swallow a real change, or it would have replaced a
// failing ship with a silent one.
func TestInferredModify_ARealChangeIsStillPlanned(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.m5.xlarge"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("a real change was dropped: %d modify(s)", len(p.Delta.Modifies))
	}
	if p.BlastRadius.Modifies != 1 {
		t.Errorf("blast radius = %d, want 1", p.BlastRadius.Modifies)
	}
	var liveState int
	for _, in := range p.Resolution.Inputs {
		if in.Kind == "live_state" {
			liveState++
		}
	}
	if liveState != 1 {
		t.Errorf("want one live_state input for the surviving modify, got %d", liveState)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("core.Validate: %v", err)
	}
}

// A hand-written modify keeps its empty entry, because there an author
// really did ask for it and re-asserting an unchanged config may be the
// point.
func TestHandWrittenModify_EmptyEntryIsKept(t *testing.T) {
	l, _ := seedDLQ(t)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpModify, `{"id":"dlq-1","instance_class":"db.t3.large","visibility_timeout":30,"maximum_message_size":1048576}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("a hand-written no-op modify was dropped: %d modify(s)", len(p.Delta.Modifies))
	}
	m := p.Delta.Modifies[0]
	if len(m.Before) != 0 || len(m.After) != 0 {
		t.Fatalf("test setup is wrong, this modify is not empty: before=%v after=%v", m.Before, m.After)
	}
}
