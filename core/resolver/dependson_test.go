package resolver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// Hermetic tests for ResourceIntent.DependsOn (docs/schema.md's own
// "Amendment: ResourceIntent.DependsOn", UBI-47) -- a topology-only
// dependency signal with no config-attribute opinion, built for the
// diagram medium but usable by any producer of an intent file. These
// mirror the existing $ref-derived dependency tests in resolver_test.go
// row for row, confirming DependsOn shares the identical dependency
// graph, cycle detection, and destroy-target checks -- not a second,
// parallel mechanism.

func TestResolve_DependsOn_BatchInternal_AddsEdgeAndOrders(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		// Deliberately out of order, and with NO $ref at all -- DependsOn
		// is the only signal ordering this correctly.
		ResourceIntent{Type: "aws_db_instance", Name: "api", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"payments.aws_vpc.main"}},
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 2 {
		t.Fatalf("creates = %d, want 2", len(p.Delta.Creates))
	}
	var first, second map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &first)
	json.Unmarshal(p.Delta.Creates[1], &second)
	if first["name"] != "main" || second["name"] != "api" {
		t.Fatalf("execution order = %v, %v -- want main before api (DependsOn order)", first["name"], second["name"])
	}
	dependsOn, _ := second["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_vpc.main" {
		t.Fatalf("depends_on = %v, want [payments.aws_vpc.main]", dependsOn)
	}
}

func TestResolve_DependsOn_Cycle_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ResourceIntent{Type: "aws_db_instance", Name: "a", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"payments.aws_db_instance.b"}},
		ResourceIntent{Type: "aws_db_instance", Name: "b", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"payments.aws_db_instance.a"}},
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("err = %v, want ErrCycleDetected", err)
	}
}

func TestResolve_DependsOn_AlreadyLedgeredResource_ValidatedButNoEdge(t *testing.T) {
	l := core.Open(t.TempDir())
	vpcAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	seedLedger(t, l, vpcAddr, `{"id":"vpc-123","cidr_block":"10.0.0.0/16"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ResourceIntent{Type: "aws_db_instance", Name: "api", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"payments.aws_vpc.main"}},
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	// Matches the identical, existing behavior a $ref to an
	// already-ledgered (non-batch) resource already has
	// (TestResolve_RefToExistingLedgeredResource_AlwaysConcrete): nothing
	// to wait for, so no depends_on entry at all -- validated to exist,
	// never added as a graph edge.
	if _, has := node["depends_on"]; has {
		t.Fatalf("depends_on = %v, want absent (already-ledgered dependency needs no execution ordering)", node["depends_on"])
	}
}

func TestResolve_DependsOn_NonexistentAddress_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ResourceIntent{Type: "aws_db_instance", Name: "api", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"payments.aws_vpc.ghost"}},
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
}

func TestResolve_DependsOn_MalformedAddress_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ResourceIntent{Type: "aws_db_instance", Name: "api", Op: OpCreate, Config: json.RawMessage(`{}`),
			DependsOn: []string{"not-a-valid-address"}},
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
}

func TestResolve_DependsOn_DestroyTarget_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	oldAddr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "old"}
	seedLedger(t, l, oldAddr, `{"id":"old-1"}`)

	schema := newFakeSchema()
	intent := &IntentFile{
		SchemaVersion: 1,
		Kind:          IntentFileKind,
		Stack:         "payments",
		Intent:        core.Intent{Summary: "test"},
		Resources: []ResourceIntent{
			{Type: "aws_db_instance", Name: "api", Op: OpCreate, Config: json.RawMessage(`{}`),
				DependsOn: []string{"payments.aws_db_instance.old"}},
		},
		Destroys: []string{"payments.aws_db_instance.old"},
	}

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefToDestroyTarget) {
		t.Fatalf("err = %v, want ErrRefToDestroyTarget", err)
	}
}

func TestResolve_DependsOn_UnionsWithRefDerivedEdge_NoDuplicate(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
		// A real $ref AND an identical DependsOn entry naming the same
		// address -- must not appear twice in the resolved depends_on.
		ResourceIntent{Type: "aws_db_instance", Name: "db", Op: OpCreate,
			Config:    json.RawMessage(`{"vpc_cidr":{"$ref":{"to":"payments.aws_vpc.main.cidr_block"}}}`),
			DependsOn: []string{"payments.aws_vpc.main"}},
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[1], &node)
	dependsOn, _ := node["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_vpc.main" {
		t.Fatalf("depends_on = %v, want exactly one entry, not a duplicate", dependsOn)
	}
}

func TestResolve_DependsOn_EmptyIsNoOp(t *testing.T) {
	// A ResourceIntent with no DependsOn at all (every existing producer
	// -- hand-written JSON, --from-doc, --from-code) must behave exactly
	// as before this amendment.
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	if _, has := node["depends_on"]; has {
		t.Fatalf("depends_on = %v, want absent", node["depends_on"])
	}
}
