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
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("want one modify, got %d", len(p.Delta.Modifies))
	}
	m := p.Delta.Modifies[0]
	for _, attr := range []string{"visibility_timeout", "maximum_message_size"} {
		if _, removed := m.Before[attr]; removed {
			if _, kept := m.After[attr]; !kept {
				t.Errorf("%s is planned for removal, but the program never set it: before=%v after=%v",
					attr, m.Before, m.After)
			}
		}
	}
	if len(m.Before) != 0 || len(m.After) != 0 {
		t.Errorf("an unchanged program produced a non-empty diff: before=%v after=%v", m.Before, m.After)
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
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("want one modify, got %d", len(p.Delta.Modifies))
	}
	if _, removed := p.Delta.Modifies[0].Before["backup_retention_period"]; removed {
		t.Errorf("an out-of-band change adopted into the ledger was planned for removal: %v", p.Delta.Modifies[0].Before)
	}
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
