package resolver

import (
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// All three SDK runtimes hardcode op "create", because a hermetic,
// describe-only program cannot read ledger state. The resolver checked
// that op against ledger presence, because for a hand-written file the
// op is a real claim and a wrong one is a catchable authoring mistake.
//
// Together those made an SDK-authored stack write-once: any second plan
// after any successful ship was refused, and a partially shipped stack
// could not be completed by re-running its own program (UBI-267).
//
// WithInferredOp scopes the check to the documents it was written for
// rather than weakening it.

func TestResolve_InferredOp_CreateAgainstExistingBecomesModify(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve with inferred op: %v", err)
	}
	if len(p.Delta.Creates) != 0 {
		t.Errorf("an existing address was planned as a create: %d create(s)", len(p.Delta.Creates))
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("want exactly one modify, got %d", len(p.Delta.Modifies))
	}
	if got := p.Delta.Modifies[0].Target.String(); got != addr.String() {
		t.Errorf("modify targets %s, want %s", got, addr)
	}
}

func TestResolve_InferredOp_ModifyAgainstMissingBecomesCreate(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "ghost", OpModify, `{"instance_class":"db.t3.large"}`),
	)

	// Nothing generates a modify today, but inference is a function of
	// ledger presence rather than a rule pointed one way, so a generated
	// modify against an absent address is the same non-claim as a
	// generated create against a present one.
	p, err := Resolve(l, singleProvider(schema), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve with inferred op: %v", err)
	}
	if len(p.Delta.Creates) != 1 || len(p.Delta.Modifies) != 0 {
		t.Errorf("want one create and no modifies, got %d create(s) and %d modify(s)",
			len(p.Delta.Creates), len(p.Delta.Modifies))
	}
}

// The partial-ship shape, which is what prompted this: some addresses
// exist and some do not, in one document, from one program that called
// every one of them a create.
func TestResolve_InferredOp_MixedCreateAndModifyInOneDocument(t *testing.T) {
	l := core.Open(t.TempDir())
	shipped := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	seedLedger(t, l, shipped, `{"id":"dlq-1"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
		ri("aws_db_instance", "queue", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve with inferred op: %v", err)
	}
	if len(p.Delta.Creates) != 1 {
		t.Errorf("want one create for the address that never shipped, got %d", len(p.Delta.Creates))
	}
	if len(p.Delta.Modifies) != 1 {
		t.Errorf("want one modify for the address that did ship, got %d", len(p.Delta.Modifies))
	}
	if len(p.Delta.Modifies) == 1 && p.Delta.Modifies[0].Target.String() != shipped.String() {
		t.Errorf("the modify targets %s, want the already-shipped %s", p.Delta.Modifies[0].Target, shipped)
	}
}

// Without the option nothing changes. A hand-written intent file still
// has both errors, because there a human really did state an op and
// really can be wrong about it.
func TestResolve_WithoutInferredOp_BothChecksStillFire(t *testing.T) {
	t.Run("create against an existing address", func(t *testing.T) {
		l := core.Open(t.TempDir())
		addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
		seedLedger(t, l, addr, `{"id":"db-1"}`)

		_, err := Resolve(l, singleProvider(newFakeSchema()), intentFile("payments",
			ri("aws_db_instance", "db", OpCreate, `{"instance_class":"db.t3.large"}`),
		), nil)
		if !errors.Is(err, ErrCreateTargetExists) {
			t.Fatalf("err = %v, want ErrCreateTargetExists", err)
		}
	})

	t.Run("modify against a missing address", func(t *testing.T) {
		l := core.Open(t.TempDir())
		_, err := Resolve(l, singleProvider(newFakeSchema()), intentFile("payments",
			ri("aws_db_instance", "ghost", OpModify, `{"instance_class":"db.t3.large"}`),
		), nil)
		if !errors.Is(err, ErrModifyTargetMissing) {
			t.Fatalf("err = %v, want ErrModifyTargetMissing", err)
		}
	})
}

// Inference must not mutate the caller's document. Resolve runs
// resolveOnce twice through DoubleRun and compares the two results, so a
// document rewritten by the first pass would make the second pass read a
// different input than the first.
func TestResolve_InferredOp_LeavesTheIntentDocumentUnchanged(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	if _, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := intent.Resources[0].Op; got != OpCreate {
		t.Errorf("the caller's own document was rewritten: op = %q, want %q", got, OpCreate)
	}
}
