package diagram

import (
	"strings"
	"testing"

	"oss.terrastruct.com/d2/d2format"
	"oss.terrastruct.com/d2/d2parser"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// These tests exercise emitD2/attrTooltip/d2Quote directly -- the text-
// building half of Emit, deliberately isolated from any real ledger so
// the wide range of shapes (dependency edges, cross-stack reference
// nodes, empty attrs, name collisions across types) can be checked
// without paying for a full create/accept/ship pipeline per case. Emit's
// own ledger-walking half (Fleet/FoldState/Read wiring) is covered end to
// end by cli/render_test.go, against a real fakeprovider-shipped ledger.

func mustEmitD2(t *testing.T, resources []emitResource) string {
	t.Helper()
	b, err := emitD2("payments", resources)
	if err != nil {
		t.Fatalf("emitD2: %v", err)
	}
	return string(b)
}

func TestEmitD2_TwoResourcesWithDependency(t *testing.T) {
	resources := []emitResource{
		{
			addr:  core.Address{Stack: "payments", Type: "aws_db_instance", Name: "primary-db"},
			attrs: map[string]interface{}{"engine": "postgres", "instance_class": "db.t3.micro"},
		},
		{
			addr:      core.Address{Stack: "payments", Type: "aws_vpc", Name: "main-vpc"},
			attrs:     map[string]interface{}{"id": "vpc-123"},
			dependsOn: nil,
		},
	}
	// db depends on vpc -- attach after construction so sort order (by
	// type,name: aws_db_instance < aws_vpc) is exercised too.
	resources[0].dependsOn = []string{"payments.aws_vpc.main-vpc"}

	out := mustEmitD2(t, resources)

	if !strings.Contains(out, `classes: {`) || !strings.Contains(out, "aws_db_instance") || !strings.Contains(out, "aws_vpc") {
		t.Fatalf("missing classes block: %s", out)
	}
	if !strings.Contains(out, `r0: "primary-db"`) {
		t.Fatalf("expected r0 to be primary-db (sorted before main-vpc by type), got: %s", out)
	}
	if !strings.Contains(out, "class: aws_db_instance") {
		t.Fatalf("missing class: aws_db_instance: %s", out)
	}
	if !strings.Contains(out, `r1: "main-vpc"`) {
		t.Fatalf("expected r1 to be main-vpc: %s", out)
	}
	if !strings.Contains(out, "r0 -> r1") {
		t.Fatalf("expected the depends_on edge r0 -> r1, got: %s", out)
	}
	if !strings.Contains(out, "engine: postgres") || !strings.Contains(out, "instance_class: db.t3.micro") {
		t.Fatalf("missing attribute tooltip content: %s", out)
	}
}

func TestEmitD2_CrossStackReference_AnnotatedWithPinnedHead(t *testing.T) {
	resources := []emitResource{
		{
			addr:  core.Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"},
			attrs: map[string]interface{}{},
			crossPins: []core.ResolutionInput{
				{
					Kind:       "cross_stack_pin",
					Resource:   "networking.aws_vpc.main",
					From:       "payments.aws_db_instance.payments-db",
					PinnedHead: "7fc2abc",
				},
			},
		},
	}

	out := mustEmitD2(t, resources)

	if !strings.Contains(out, "external") {
		t.Fatalf("expected the external class to be declared: %s", out)
	}
	if !strings.Contains(out, `ref0: "@networking.aws_vpc.main"`) {
		t.Fatalf("expected a reference node labeled with the pinned address, got: %s", out)
	}
	if !strings.Contains(out, "class: external") {
		t.Fatalf("expected the reference node's own class: external, got: %s", out)
	}
	if !strings.Contains(out, "pinned_head: 7fc2abc") {
		t.Fatalf("expected the pinned_head annotation, got: %s", out)
	}
	if !strings.Contains(out, "r0 -> ref0") {
		t.Fatalf("expected an edge from the referencing resource to the reference node, got: %s", out)
	}
}

func TestEmitD2_TwoResourcesShareNeighborPin_OneReferenceNode(t *testing.T) {
	pin := core.ResolutionInput{Kind: "cross_stack_pin", Resource: "networking.aws_vpc.main", PinnedHead: "abc"}
	resources := []emitResource{
		{addr: core.Address{Stack: "payments", Type: "aws_db_instance", Name: "a"}, crossPins: []core.ResolutionInput{pin}},
		{addr: core.Address{Stack: "payments", Type: "aws_db_instance", Name: "b"}, crossPins: []core.ResolutionInput{pin}},
	}

	out := mustEmitD2(t, resources)

	if strings.Count(out, "class: external") != 1 {
		t.Fatalf("expected exactly one reference node despite two resources pinning it, got: %s", out)
	}
	if !strings.Contains(out, "r0 -> ref0") || !strings.Contains(out, "r1 -> ref0") {
		t.Fatalf("expected both resources to edge into the same ref0, got: %s", out)
	}
}

func TestEmitD2_NameCollisionAcrossTypes_NoKeyCollision(t *testing.T) {
	// Two different-typed resources sharing the same Name -- a real,
	// legal scenario (only (type,name) is unique) that would collide if
	// the D2 key were derived from Name alone.
	resources := []emitResource{
		{addr: core.Address{Stack: "payments", Type: "aws_db_instance", Name: "primary"}},
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "primary"}},
	}

	out := mustEmitD2(t, resources)

	if !strings.Contains(out, `r0: "primary"`) || !strings.Contains(out, `r1: "primary"`) {
		t.Fatalf("expected both resources to render with distinct keys r0/r1, got: %s", out)
	}
}

func TestEmitD2_NoResources_EmptyButValid(t *testing.T) {
	out := mustEmitD2(t, nil)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output for a stack with no live resources, got: %q", out)
	}
}

func TestEmitD2_NoAttrs_NoTooltip(t *testing.T) {
	resources := []emitResource{
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "main-vpc"}, attrs: map[string]interface{}{}},
	}
	out := mustEmitD2(t, resources)
	if strings.Contains(out, "tooltip") {
		t.Fatalf("expected no tooltip attribute for a resource with no attrs, got: %s", out)
	}
}

func TestEmitD2_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	resources := []emitResource{
		{
			addr:      core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"},
			attrs:     map[string]interface{}{"z": "1", "a": "2", "m": "3"},
			dependsOn: []string{"payments.aws_vpc.vpc"},
		},
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "vpc"}},
	}
	first := mustEmitD2(t, resources)
	for i := 0; i < 4; i++ {
		if got := mustEmitD2(t, resources); got != first {
			t.Fatalf("emitD2 output changed across repeated calls with identical input:\nfirst:\n%s\ngot:\n%s", first, got)
		}
	}
}

func TestEmitD2_RoundTripsThroughParse(t *testing.T) {
	// Emit's own output, fed back through the real, unmodified Parse, is
	// real proof of "render/parse share one convention, not two" --
	// docs/diagram-medium.md's own design center for this medium being
	// bidirectional by construction, not just each direction tested in
	// isolation.
	resources := []emitResource{
		{
			addr:      core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"},
			attrs:     map[string]interface{}{"engine": "postgres"},
			dependsOn: []string{"payments.aws_vpc.vpc"},
			crossPins: []core.ResolutionInput{{Kind: "cross_stack_pin", Resource: "networking.aws_vpc.main", PinnedHead: "abc"}},
		},
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "vpc"}},
	}
	out := mustEmitD2(t, resources)

	neighborDir := t.TempDir()
	intent := mustParse(t, out, "payments", []resolver.DeclaredProvider{awsProvider()},
		Options{NeighborLedgers: map[string]string{"networking": neighborDir}})

	if len(intent.Resources) != 2 {
		t.Fatalf("resources = %+v, want 2 (the reference node must not become a resource)", intent.Resources)
	}
	db := resourceByName(t, intent, "db")
	if db.Type != "aws_db_instance" {
		t.Fatalf("db.Type = %q, want aws_db_instance -- class: round-tripped incorrectly", db.Type)
	}
	if len(db.DependsOn) != 1 || db.DependsOn[0] != "payments.aws_vpc.vpc" {
		t.Fatalf("db.DependsOn = %v, want [payments.aws_vpc.vpc] -- the r0 -> r1 edge must round-trip into depends_on", db.DependsOn)
	}
	vpc := resourceByName(t, intent, "vpc")
	if vpc.Type != "aws_vpc" {
		t.Fatalf("vpc.Type = %q, want aws_vpc", vpc.Type)
	}
	if len(intent.Intent.Defaults) != 1 {
		t.Fatalf("defaults = %+v, want exactly 1 -- the emitted reference-node edge must round-trip into the $cross structural-limitation note", intent.Intent.Defaults)
	}
	if !strings.Contains(intent.Intent.Defaults[0].Text, "networking.aws_vpc.main") {
		t.Fatalf("defaults[0].Text = %q, want it to name the round-tripped reference address", intent.Intent.Defaults[0].Text)
	}
}

func TestEmitD2_OutputIsFormatIdempotent(t *testing.T) {
	// docs/diagram-medium.md's own confirmed-idempotent property that
	// makes render --check's byte-compare meaningful at all: re-parsing
	// and re-formatting Emit's own output must be a no-op.
	resources := []emitResource{
		{
			addr:      core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"},
			attrs:     map[string]interface{}{"engine": "postgres"},
			dependsOn: []string{"payments.aws_vpc.vpc"},
			crossPins: []core.ResolutionInput{{Kind: "cross_stack_pin", Resource: "networking.aws_vpc.main", PinnedHead: "abc"}},
		},
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "vpc"}},
	}
	out := mustEmitD2(t, resources)

	m, err := d2parser.Parse("payments.d2", strings.NewReader(out), nil)
	if err != nil {
		t.Fatalf("d2parser.Parse(Emit's own output): %v", err)
	}
	if reformatted := d2format.Format(m); reformatted != out {
		t.Fatalf("d2format.Format is not idempotent on Emit's own output:\nfirst:\n%s\nreformatted:\n%s", out, reformatted)
	}
}

func TestAttrTooltip_SortedDeterministic(t *testing.T) {
	got := attrTooltip(map[string]interface{}{"z": "1", "a": "2", "m": 3.0})
	want := `a: 2; m: 3; z: 1`
	if got != want {
		t.Fatalf("attrTooltip = %q, want %q", got, want)
	}
}

func TestAttrTooltip_Empty(t *testing.T) {
	if got := attrTooltip(nil); got != "" {
		t.Fatalf("attrTooltip(nil) = %q, want empty", got)
	}
	if got := attrTooltip(map[string]interface{}{}); got != "" {
		t.Fatalf("attrTooltip({}) = %q, want empty", got)
	}
}

func TestD2Quote_EscapesQuotesBackslashesAndNewlines(t *testing.T) {
	got := d2Quote(`say "hi"` + "\\" + "\nnext line")
	want := `"say \"hi\"\\\nnext line"`
	if got != want {
		t.Fatalf("d2Quote = %q, want %q", got, want)
	}
}
