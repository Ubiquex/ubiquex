package ts

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

func rt(wireType string, fields ...ir.Field) *ir.ResourceType {
	return &ir.ResourceType{WireType: wireType, Fields: fields}
}

func scalarField(wireName string, scalar ir.ScalarKind, required, optional, computed, sensitive bool) ir.Field {
	return ir.Field{
		WireName:  wireName,
		Type:      ir.TypeRef{Kind: ir.KindScalar, Scalar: scalar},
		Required:  required,
		Optional:  optional,
		Computed:  computed,
		Sensitive: sensitive,
	}
}

func TestGeneratedFile_FlatResource(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_db_instance",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("instance_class", ir.ScalarString, false, true, false, false),
			scalarField("allocated_storage", ir.ScalarNumber, false, true, false, false),
			scalarField("master_password", ir.ScalarString, true, false, false, true),
		),
	}
	out, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	mustContain(t, out, `export const __ubxSourceProvenance = { source: "hashicorp/aws", version: "6.60.0" } as const;`)
	mustContain(t, out, "export interface AwsDbInstanceConfig {")
	// id is Computed-only -- never in Config specifically (it legitimately
	// appears in the separate Attrs interface, checked below).
	configBlock := out[strings.Index(out, "export interface AwsDbInstanceConfig {"):strings.Index(out, "export interface AwsDbInstanceAttrs {")]
	mustNotContain(t, configBlock, "id:")
	mustContain(t, out, "instanceClass?: string | Computed<string>;")
	mustContain(t, out, "allocatedStorage?: number | Computed<number>;")
	// Required, no `?`.
	mustContain(t, out, "masterPassword: string | Computed<string>;")

	mustContain(t, out, "export interface AwsDbInstanceAttrs {")
	mustContain(t, out, "id: string;") // Attrs carries every field, plain type

	mustContain(t, out, `export const AwsDbInstance: ResourceBinding<AwsDbInstanceConfig, AwsDbInstanceAttrs> = {`)
	mustContain(t, out, `wireType: "aws_db_instance",`)
	mustContain(t, out, `instanceClass: "instance_class",`)
	mustContain(t, out, `allocatedStorage: "allocated_storage",`)
	// id never appears in the runtime fields map (not settable).
	if strings.Contains(out, `id: "id",`) {
		t.Fatalf("generated fields map should not include the computed-only id field:\n%s", out)
	}
}

func TestGeneratedFile_ListSetMapOfScalar(t *testing.T) {
	listField := ir.Field{WireName: "security_group_ids", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindList, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}
	setField := ir.Field{WireName: "availability_zones", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindSet, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}
	mapField := ir.Field{WireName: "tags", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindMap, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}

	types := []*ir.ResourceType{rt("aws_db_instance", listField, setField, mapField)}
	out, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	mustContain(t, out, "securityGroupIds?: string[] | Computed<string[]>;")
	mustContain(t, out, "availabilityZones?: string[] | Computed<string[]>;")
	mustContain(t, out, "tags?: Record<string, string> | Computed<Record<string, string>>;")
	// Scalar collections need no recursive fields map -- plain wire-name string leaf.
	mustContain(t, out, `securityGroupIds: "security_group_ids",`)
	mustContain(t, out, `tags: "tags",`)
}

func TestGeneratedFile_NestedObjectBlock(t *testing.T) {
	nested := ir.Field{
		WireName: "settings",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	types := []*ir.ResourceType{rt("aws_thing", nested)}
	out, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	mustContain(t, out, "export interface AwsThing_Settings {\n  enabled: boolean;\n}")
	mustContain(t, out, "settings?: AwsThing_Settings | Computed<AwsThing_Settings>;")
	mustContain(t, out, `wireName: "settings"`)
	mustContain(t, out, `kind: "object"`)
	mustContain(t, out, `enabled: "enabled",`)
}

func TestGeneratedFile_ListOfNestedObject(t *testing.T) {
	nested := ir.Field{
		WireName: "rule",
		Type: ir.TypeRef{Kind: ir.KindList, Element: &ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "from_port", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarNumber}},
		}}},
	}
	types := []*ir.ResourceType{rt("aws_security_group", nested)}
	out, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}

	mustContain(t, out, "export interface AwsSecurityGroup_Rule {\n  fromPort: number;\n}")
	mustContain(t, out, "rule?: AwsSecurityGroup_Rule[] | Computed<AwsSecurityGroup_Rule[]>;")
	mustContain(t, out, `wireName: "rule"`)
	mustContain(t, out, `kind: "list"`)
	mustContain(t, out, `fromPort: "from_port",`)
}

func TestGeneratedFile_UnsupportedWireNameCharacters_Errors(t *testing.T) {
	types := []*ir.ResourceType{rt("aws_thing", scalarField("Weird-Name!", ir.ScalarString, false, true, false, false))}
	if _, err := GeneratedFile("hashicorp/aws", "6.60.0", types); err == nil {
		t.Fatal("GeneratedFile: got nil error for an unsupported wire-name character, want an error")
	}
}

func TestGeneratedFile_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_db_instance",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("instance_class", ir.ScalarString, false, true, false, false),
		),
		rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	first, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := GeneratedFile("hashicorp/aws", "6.60.0", types)
		if err != nil {
			t.Fatalf("GeneratedFile (run %d): %v", i, err)
		}
		if again != first {
			t.Fatalf("run %d produced different output than run 0", i)
		}
	}
}

func TestGeneratedFile_SortsByWireTypeRegardlessOfInputOrder(t *testing.T) {
	a := rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false))
	b := rt("aws_db_instance", scalarField("id", ir.ScalarString, false, false, true, false))

	out1, err := GeneratedFile("hashicorp/aws", "6.60.0", []*ir.ResourceType{a, b})
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}
	out2, err := GeneratedFile("hashicorp/aws", "6.60.0", []*ir.ResourceType{b, a})
	if err != nil {
		t.Fatalf("GeneratedFile: %v", err)
	}
	if out1 != out2 {
		t.Fatalf("output depends on input slice order:\n--- order [vpc, db_instance] ---\n%s\n--- order [db_instance, vpc] ---\n%s", out1, out2)
	}
	if strings.Index(out1, "AwsDbInstance") > strings.Index(out1, "AwsVpc") {
		t.Fatalf("expected aws_db_instance to be rendered before aws_vpc (sorted by wire type):\n%s", out1)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("output missing %q:\n%s", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("output unexpectedly contains %q:\n%s", needle, haystack)
	}
}
