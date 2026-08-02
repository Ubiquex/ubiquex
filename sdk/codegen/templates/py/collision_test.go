package pytmpl

import (
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// TestCheckNoDuplicateDeclarations_DetectsRealCollision proves the checker
// catches a genuine Python module-namespace collision -- a `class Foo`
// followed by a `Foo = sdk.ResourceBinding(...)` module-level assignment,
// which Python itself never errors on (the second silently overwrites the
// first in the module's own namespace -- see this package's own
// resourceRenderer doc comment for the full, live-verified account,
// UBI-96).
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `@dataclasses.dataclass
class Foo:
    bar: Any = None

Foo = sdk.ResourceBinding(
    wire_type="aws_foo",
    fields={},
)
`
	err := CheckNoDuplicateDeclarations(src)
	if err == nil {
		t.Fatal("expected an error for `class Foo` + `Foo = sdk.ResourceBinding(...)` sharing one module name, got nil")
	}
	mustContain(t, err.Error(), "Foo")
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	src := `@dataclasses.dataclass
class Foo:
    bar: Any = None

Baz = sdk.ResourceBinding(
    wire_type="aws_baz",
    fields={},
)
`
	if err := CheckNoDuplicateDeclarations(src); err != nil {
		t.Fatalf("expected no error for a collision-free module, got: %v", err)
	}
}

// TestGeneratedFile_CrossResourceNestedBlockVsSiblingResource_NoCollision
// is sdk/codegen/templates/go's own sibling test, same synthetic
// aws_thing/aws_thing_logging shape (the real aws_s3_bucket/
// aws_s3_bucket_logging collision, live-verified against
// hashicorp/aws@6.54.0 this session -- see STATE.md), ported to this
// package's own Python output.
func TestGeneratedFile_CrossResourceNestedBlockVsSiblingResource_NoCollision(t *testing.T) {
	loggingBlock := ir.Field{
		WireName: "logging",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	types := []*ir.ResourceType{
		rt("aws_thing", loggingBlock),
		rt("aws_thing_logging",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("target", ir.ScalarString, false, true, false, false),
		),
	}
	out, err := GeneratedFile("hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	mustContain(t, out, "class AwsThing_Logging:\n    enabled: Any = None")
	mustContain(t, out, "class AwsThingLoggingConfig:")
	mustContain(t, out, "AwsThingLogging = sdk.ResourceBinding(")
	mustNotContain(t, out, "class AwsThingLogging:\n")

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("GeneratedFile output has a real duplicate declaration: %v", err)
	}
}
