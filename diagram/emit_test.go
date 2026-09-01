package diagram

import (
	"strings"
	"testing"

	"oss.terrastruct.com/d2/d2format"
	"oss.terrastruct.com/d2/d2parser"

	"github.com/ubiquex/ubiquex/core"
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

// TestEmitD2_BlueprintSourcedResources_GroupInDashedContainer is UBI-74
// Slice 6's own required hermetic proof, at the emitD2 unit level: two
// resources sharing the same blueprintRef nest inside one dashed-border
// container, edges between them use the container-qualified path, and
// the container is labeled with a short form of the ref.
func TestEmitD2_BlueprintSourcedResources_GroupInDashedContainer(t *testing.T) {
	resources := []emitResource{
		{
			addr:         core.Address{Stack: "payments", Type: "fake_widget", Name: "mirror"},
			dependsOn:    []string{"payments.fake_widget.primary"},
			blueprintRef: "ci-platform:sha256:" + strings.Repeat("a", 64),
		},
		{
			addr:         core.Address{Stack: "payments", Type: "fake_widget", Name: "primary"},
			blueprintRef: "ci-platform:sha256:" + strings.Repeat("a", 64),
		},
	}

	out := mustEmitD2(t, resources)

	if strings.Count(out, "style.stroke-dash: 3") != 1 || strings.Count(out, "style.fill: transparent") != 1 {
		t.Fatalf("expected exactly one dashed-border container (both resources share one blueprint call), got: %s", out)
	}
	if !strings.Contains(out, "bp0: ") {
		t.Fatalf("expected the container key bp0, got: %s", out)
	}
	if !strings.Contains(out, "ci-platform:sha256:"+strings.Repeat("a", 12)+"…") {
		t.Fatalf("expected the container labeled with the short-hash form of the ref, got: %s", out)
	}
	// Indented (nested inside bp0's own braces), not top-level -- a
	// top-level r0/r1 would render at zero indent, the exact distinction
	// TestEmitD2_MixedBlueprintAndOrdinaryResources_OnlyBlueprintOnesGroup
	// below also checks for the mixed case.
	if !strings.Contains(out, "  r0: \"mirror\" {") || !strings.Contains(out, "  r1: \"primary\" {") {
		t.Fatalf("expected both resources nested (indented) inside bp0, got: %s", out)
	}
	if !strings.Contains(out, "bp0.r0 -> bp0.r1") {
		t.Fatalf("expected the depends_on edge between container-qualified paths, got: %s", out)
	}
}

// TestEmitD2_MixedBlueprintAndOrdinaryResources_OnlyBlueprintOnesGroup
// proves an ordinary (non-blueprint) resource renders top-level exactly
// as before this slice, even in a stack that ALSO has a blueprint-sourced
// resource -- grouping is purely additive, never a behavior change for a
// resource with no blueprintRef.
func TestEmitD2_MixedBlueprintAndOrdinaryResources_OnlyBlueprintOnesGroup(t *testing.T) {
	resources := []emitResource{
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "main-vpc"}},
		{
			addr:         core.Address{Stack: "payments", Type: "fake_widget", Name: "queue"},
			blueprintRef: "ci-platform:sha256:" + strings.Repeat("b", 64),
		},
	}

	out := mustEmitD2(t, resources)

	if !strings.Contains(out, "\nr0: \"main-vpc\" {") {
		t.Fatalf("expected the ordinary resource to render top-level (unindented) as r0, unchanged, got: %s", out)
	}
	if !strings.Contains(out, "  r1: \"queue\" {") {
		t.Fatalf("expected the blueprint-sourced resource nested (indented) inside bp0 as r1, got: %s", out)
	}
}

// TestEmitD2_NoBlueprintResources_NoContainer_ByteIdenticalToBefore
// confirms a stack with zero blueprint-sourced resources produces output
// with no container syntax at all -- Slice 6's own explicit requirement
// that grouping never changes an ordinary stack's rendering.
func TestEmitD2_NoBlueprintResources_NoContainer_ByteIdenticalToBefore(t *testing.T) {
	resources := []emitResource{
		{addr: core.Address{Stack: "payments", Type: "aws_vpc", Name: "main-vpc"}},
	}
	out := mustEmitD2(t, resources)
	if strings.Contains(out, "bp0") || strings.Contains(out, "stroke-dash") {
		t.Fatalf("expected no container syntax at all for a stack with no blueprint-sourced resources, got: %s", out)
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
