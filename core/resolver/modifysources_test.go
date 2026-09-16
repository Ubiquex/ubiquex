package resolver

import (
	"encoding/json"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestResolve_ModifyFromABlueprintCallCarriesProvenance is the check a
// walkthrough finding demanded: a modify produced from a blueprint call
// must record what produced it, which is UBI-281's entire subject.
//
// The second ship of an unchanged blueprint stack resolves to a modify,
// and if that modify carried no sources the fold would read it as a
// hand-written re-declaration and CLEAR the blueprint record, which is
// the failure UBI-227's restore path had until #200.
func TestResolve_ModifyFromABlueprintCallCarriesProvenance(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	seedLedgerWithSources(t, l, addr,
		`{"id":"vpc-1","cidr_block":"10.0.0.0/16"}`, `{"id":"vpc-1"}`,
		[]core.IntentSource{{Kind: "blueprint", Ref: "network:sha256:v1"}})

	// What ExpandCalls produces on a second run: the same resource, with
	// the blueprint's stamp, changed. Op is inferred, as it is for any
	// generated document.
	intent := intentFile("payments")
	intent.Resources = []ResourceIntent{{
		Type: addr.Type,
		Name: addr.Name,
		// What a blueprint produces: every resource is a create, and
		// inference flips it once the ledger already has the address.
		Op:     OpCreate,
		Config: json.RawMessage(`{"cidr_block":"10.1.0.0/16"}`),
		Sources: []core.IntentSource{{
			Kind: "blueprint", Ref: "network:sha256:v2",
			Declaration:    "oci://ghcr.io/ubx-blueprints/network:v2",
			DeclaredSource: "oci://ghcr.io/ubx-blueprints/network:v2",
			DeclaredArgs:   map[string]string{"cidr": "10.1.0.0/16"},
			WithheldArgs:   []string{"api_token"},
		}},
	}}

	p, err := Resolve(l, singleProvider(schema), intent, nil, WithInferredOp())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("expected exactly 1 modify, got %d creates and %d modifies",
			len(p.Delta.Creates), len(p.Delta.Modifies))
	}
	got := p.Delta.Modifies[0].Sources
	if len(got) != 1 {
		t.Fatalf("the modify carries no provenance, so the fold would read it as a hand-written "+
			"re-declaration and clear the blueprint record: %+v", p.Delta.Modifies[0])
	}
	if got[0].Ref != "network:sha256:v2" {
		t.Errorf("ref = %q", got[0].Ref)
	}
	if got[0].Declaration != "oci://ghcr.io/ubx-blueprints/network:v2" {
		t.Errorf("declaration = %q", got[0].Declaration)
	}
	if got[0].DeclaredArgs["cidr"] != "10.1.0.0/16" {
		t.Errorf("arguments = %+v", got[0].DeclaredArgs)
	}
	if len(got[0].WithheldArgs) != 1 {
		t.Errorf("withheld = %v", got[0].WithheldArgs)
	}
}
