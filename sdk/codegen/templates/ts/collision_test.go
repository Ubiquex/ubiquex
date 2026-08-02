package ts

import (
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// TestCheckNoDuplicateDeclarations_DetectsRealCollision proves the checker
// catches two `export interface` declarations sharing one name -- see
// this package's own resourceRenderer doc comment for why that's a real,
// live-verified failure mode (UBI-96), not a hypothetical.
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `export interface Foo {
  bar: string;
}

export interface Foo {
  baz: number;
}
`
	err := CheckNoDuplicateDeclarations(src)
	if err == nil {
		t.Fatal("expected an error for two `export interface Foo` declarations, got nil")
	}
	mustContain(t, err.Error(), "Foo")
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	src := `export interface Foo {
  bar: string;
}

export const Foo: Foo = { bar: "x" };
`
	// A const and an interface sharing a name is FINE in TS (separate
	// type/value namespaces) -- must not be flagged.
	if err := CheckNoDuplicateDeclarations(src); err != nil {
		t.Fatalf("expected no error (interface/const namespaces are separate in TS), got: %v", err)
	}
}

// TestGeneratedFile_CrossResourceNestedBlockVsSiblingResource_NoCollision
// is sdk/codegen/templates/go's own sibling test, same synthetic
// aws_thing/aws_thing_logging shape (the real aws_s3_bucket/
// aws_s3_bucket_logging collision, live-verified against
// hashicorp/aws@6.54.0 this session -- see STATE.md), ported to this
// package's own TS output.
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

	mustContain(t, out, "export interface AwsThing_Logging {\n  enabled: boolean;\n}")
	mustContain(t, out, "export interface AwsThingLoggingConfig {")
	mustContain(t, out, "export const AwsThingLogging:")
	mustNotContain(t, out, "export interface AwsThingLogging {")

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("GeneratedFile output has a real duplicate declaration: %v", err)
	}
}
