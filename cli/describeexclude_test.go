package cli

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

func TestDescribeExcludeFromParams_RealTOMLArrayShape(t *testing.T) {
	params := map[string]any{
		"schema_source": "cloudformation",
		"describe_exclude": []any{
			"aws_quick_sight_dashboard",
			"aws_quick_sight_analysis",
			"aws_quick_sight_template",
		},
	}
	got := describeExcludeFromParams(params)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %v", len(got), got)
	}
	for _, name := range []string{"aws_quick_sight_dashboard", "aws_quick_sight_analysis", "aws_quick_sight_template"} {
		if !got[name] {
			t.Errorf("expected %q to be excluded", name)
		}
	}
	if got["aws_sqs_queue"] {
		t.Error("aws_sqs_queue was never declared, must not be excluded")
	}
}

func TestDescribeExcludeFromParams_AbsentKeyReturnsNil(t *testing.T) {
	if got := describeExcludeFromParams(map[string]any{"schema_source": "openapi"}); got != nil {
		t.Fatalf("got %v, want nil (no describe_exclude key at all)", got)
	}
	if got := describeExcludeFromParams(nil); got != nil {
		t.Fatalf("got %v, want nil (nil params)", got)
	}
}

func TestDescribeExcludeFromParams_WrongTypeFailsOpen(t *testing.T) {
	// A real config typo (a bare string instead of an array, say) must
	// never panic and must never accidentally exclude everything --
	// failing open (nothing excluded) is the only safe default.
	got := describeExcludeFromParams(map[string]any{"describe_exclude": "aws_quick_sight_dashboard"})
	if got != nil {
		t.Fatalf("got %v, want nil (wrong-shaped value fails open)", got)
	}
}

func TestDescribeExcludeFromParams_NonStringElementsSkipped(t *testing.T) {
	got := describeExcludeFromParams(map[string]any{
		"describe_exclude": []any{"aws_real_one", 42, nil, ""},
	})
	if len(got) != 1 || !got["aws_real_one"] {
		t.Fatalf("got %v, want exactly {aws_real_one: true} -- non-string/empty entries must be skipped, not panic", got)
	}
}

func fakeResourceType(wireType string, fields ...ir.Field) *ir.ResourceType {
	return &ir.ResourceType{WireType: wireType, Fields: fields}
}

func scalarField(name string, source ir.DescriptionSource) ir.Field {
	return ir.Field{WireName: name, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}, DescriptionSource: source}
}

func TestPartitionDescribeTypes_SplitsExcludedFromDescribable(t *testing.T) {
	sqs := fakeResourceType("aws_sqs_queue", scalarField("queue_name", ir.DescriptionSourceModel))
	dashboard := fakeResourceType("aws_quick_sight_dashboard", scalarField("dashboard_id", ir.DescriptionSourceNone))
	analysis := fakeResourceType("aws_quick_sight_analysis", scalarField("analysis_id", ir.DescriptionSourceNone))

	exclude := map[string]bool{"aws_quick_sight_dashboard": true, "aws_quick_sight_analysis": true}
	describeTypes, excludedTypes := partitionDescribeTypes([]*ir.ResourceType{sqs, dashboard, analysis}, exclude)

	if len(describeTypes) != 1 || describeTypes[0].WireType != "aws_sqs_queue" {
		t.Fatalf("describeTypes = %v, want exactly [aws_sqs_queue]", typeNames(describeTypes))
	}
	if len(excludedTypes) != 2 {
		t.Fatalf("excludedTypes = %v, want exactly 2 entries", typeNames(excludedTypes))
	}
}

func TestPartitionDescribeTypes_NilExcludeReturnsAllAsDescribable(t *testing.T) {
	sqs := fakeResourceType("aws_sqs_queue")
	describeTypes, excludedTypes := partitionDescribeTypes([]*ir.ResourceType{sqs}, nil)
	if len(describeTypes) != 1 || len(excludedTypes) != 0 {
		t.Fatalf("nil exclude: describeTypes=%v excludedTypes=%v, want everything describable, nothing excluded", typeNames(describeTypes), typeNames(excludedTypes))
	}
}

func typeNames(types []*ir.ResourceType) []string {
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = t.WireType
	}
	return out
}

func TestCountAllFields_RecursesIntoNestedObjectsAndCollections(t *testing.T) {
	nested := []ir.Field{
		scalarField("name", ir.DescriptionSourceModel),
		scalarField("version", ir.DescriptionSourceNone),
	}
	fields := []ir.Field{
		scalarField("top", ir.DescriptionSourceNone),
		{
			WireName: "rendering_engine",
			Type:     ir.TypeRef{Kind: ir.KindObject, Object: nested},
		},
		{
			WireName: "tags",
			Type: ir.TypeRef{
				Kind:    ir.KindList,
				Element: &ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{scalarField("key", ir.DescriptionSourceModel)}},
			},
		},
	}
	// top(1) + rendering_engine(1) + its 2 nested + tags(1) + its 1 nested element field = 6
	if got := countAllFields(fields); got != 6 {
		t.Fatalf("countAllFields = %d, want 6", got)
	}
}

func TestDescriptionCoverage_ExcludedRendersOnlyWhenNonZero(t *testing.T) {
	zero := descriptionCoverage{Sourced: 1, None: 1}
	if s := zero.String(); s == "" || strings.Contains(s, "excluded") {
		t.Fatalf("zero-excluded coverage rendered %q, must not mention excluded at all", s)
	}
	withExcluded := descriptionCoverage{Sourced: 1, None: 1, Excluded: 77457}
	if s := withExcluded.String(); !strings.Contains(s, "77457 excluded") {
		t.Fatalf("coverage with a real Excluded count rendered %q, want it to mention 77457 excluded", s)
	}
	if withExcluded.total() != 77459 {
		t.Fatalf("total() = %d, want 77459 (Excluded counted into the real total)", withExcluded.total())
	}
}
