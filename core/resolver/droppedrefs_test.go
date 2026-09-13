package resolver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// A create referencing an unchanged resource used to leave a dangling
// edge and an unsubstitutable $computed marker, because resolveRef
// defers whenever the target is in the batch while the drop removed the
// target from the delta. The edge panicked inside the ship-time
// topological sort, and filtering it alone would only have moved the
// failure to ErrDependencyNotApplied (UBI-267).

// dlqAndQueue seeds an already-shipped dlq and returns an intent whose
// queue references it, the shape the reported stack had.
func dlqAndQueue(t *testing.T) (*core.Ledger, *IntentFile) {
	t.Helper()
	l := core.Open(t.TempDir())
	dlq := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	seedLedger(t, l, dlq, `{"id":"dlq-1","instance_class":"db.t3.large"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
		ri("aws_db_instance", "queue", OpCreate,
			`{"instance_class":"db.t3.large","replicate_source_db":{"$ref":{"to":"payments.aws_db_instance.dlq.id"}}}`),
	)
	return l, intent
}

func TestDroppedRef_ResolvesFromTheLedgerAndLeavesNoDanglingEdge(t *testing.T) {
	l, intent := dlqAndQueue(t)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The dlq is unchanged, so it produces no entry.
	if len(p.Delta.Modifies) != 0 {
		t.Errorf("the unchanged dependency still produced a modify: %+v", p.Delta.Modifies)
	}
	if len(p.Delta.Creates) != 1 {
		t.Fatalf("want one create for the queue, got %d", len(p.Delta.Creates))
	}

	var node struct {
		Name      string                 `json:"name"`
		Config    map[string]interface{} `json:"config"`
		DependsOn []string               `json:"depends_on"`
	}
	if err := json.Unmarshal(p.Delta.Creates[0], &node); err != nil {
		t.Fatal(err)
	}

	// No edge to an address the proposal does not carry. This is what
	// panicked inside changeNodesOf.
	for _, dep := range node.DependsOn {
		if dep == "payments.aws_db_instance.dlq" {
			t.Errorf("the create still depends on the dropped dlq: %v", node.DependsOn)
		}
	}

	// And the value is concrete, not a marker ship could never
	// substitute.
	raw, _ := json.Marshal(node.Config["replicate_source_db"])
	if strings.Contains(string(raw), "$computed") {
		t.Fatalf("the reference is still deferred to a ship-time substitution that will never come: %s", raw)
	}
	if got := strings.Trim(string(raw), `"`); got != "dlq-1" {
		t.Errorf("the reference resolved to %q, want the dlq's own recorded id", got)
	}
}

// A reference to a dependency that IS changing stays deferred, because
// there the ship-time apply result is the correct value and the ledger's
// is stale.
func TestDroppedRef_AChangingDependencyStaysDeferred(t *testing.T) {
	l := core.Open(t.TempDir())
	dlq := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	seedLedger(t, l, dlq, `{"id":"dlq-1","instance_class":"db.t3.large"}`)

	intent := intentFile("payments",
		// The dlq genuinely changes, so it survives as a modify.
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.m5.xlarge"}`),
		ri("aws_db_instance", "queue", OpCreate,
			`{"instance_class":"db.t3.large","replicate_source_db":{"$ref":{"to":"payments.aws_db_instance.dlq.id"}}}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("the changing dependency was dropped: %d modify(s)", len(p.Delta.Modifies))
	}
	var node struct {
		Config    map[string]interface{} `json:"config"`
		DependsOn []string               `json:"depends_on"`
	}
	if err := json.Unmarshal(p.Delta.Creates[0], &node); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(node.Config["replicate_source_db"])
	if !strings.Contains(string(raw), "$computed") {
		t.Errorf("a reference to a CHANGING dependency was resolved from the stale ledger value: %s", raw)
	}
	var keeps bool
	for _, dep := range node.DependsOn {
		if dep == "payments.aws_db_instance.dlq" {
			keeps = true
		}
	}
	if !keeps {
		t.Errorf("the edge to a surviving dependency was removed: %v", node.DependsOn)
	}
}

// The lookup requirement moved with the drop, decided explicitly rather
// than by placement. A resource this has chosen not to touch should not
// fail a resolve for lacking a lookup it will never use.
func TestDroppedModify_NoLookupIsNotAnError(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "dlq"}
	// seedLedger records no lookup for this address.
	seedLedger(t, l, addr, `{"id":"dlq-1","instance_class":"db.t3.large"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "dlq", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("an unchanged resource with no recorded lookup failed the resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 0 {
		t.Errorf("want no modify, got %d", len(p.Delta.Modifies))
	}
}

// resolution.inputs is appended unsorted and goes straight into the
// hashed proposal, so its ORDER is part of the proposal's identity. The
// refactor moved where live_state inputs are appended from, and this
// pins the sequence rather than the contents.
func TestResolutionInputs_OrderIsUnchangedByTheRefactor(t *testing.T) {
	l := core.Open(t.TempDir())
	for _, name := range []string{"a", "b", "c"} {
		seedLedgerWithLookup(t, l,
			core.Address{Stack: "payments", Type: "aws_db_instance", Name: name},
			`{"id":"`+name+`-1","instance_class":"db.t3.large"}`,
			`{"id":"`+name+`-1"}`)
	}

	// All three change, so all three survive and each contributes one
	// live_state input.
	intent := intentFile("payments",
		ri("aws_db_instance", "a", OpModify, `{"instance_class":"db.m5.large"}`),
		ri("aws_db_instance", "b", OpModify, `{"instance_class":"db.m5.large"}`),
		ri("aws_db_instance", "c", OpModify, `{"instance_class":"db.m5.large"}`),
	)

	p, err := Resolve(l, singleProvider(newFakeSchema()), intent, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var got []string
	for _, in := range p.Resolution.Inputs {
		if in.Kind == "live_state" {
			got = append(got, in.Resource)
		}
	}
	want := []string{
		"payments.aws_db_instance.a",
		"payments.aws_db_instance.b",
		"payments.aws_db_instance.c",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d live_state inputs, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("live_state inputs are out of order:\n  got:  %v\n  want: %v", got, want)
		}
	}
}
