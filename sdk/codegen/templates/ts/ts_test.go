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

func TestResourceFile_FlatResource(t *testing.T) {
	rt := rt("aws_db_instance",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("instance_class", ir.ScalarString, false, true, false, false),
		scalarField("allocated_storage", ir.ScalarNumber, false, true, false, false),
		scalarField("master_password", ir.ScalarString, true, false, false, true),
	)
	out, err := ResourceFile("instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "export interface InstanceConfig {")
	// id is Computed-only -- never in Config specifically (it legitimately
	// appears in the separate Attrs interface, checked below).
	configBlock := out[strings.Index(out, "export interface InstanceConfig {"):strings.Index(out, "export interface InstanceAttrs {")]
	mustNotContain(t, configBlock, "id:")
	mustContain(t, out, "instanceClass?: string | Computed<string>;")
	mustContain(t, out, "allocatedStorage?: number | Computed<number>;")
	// Required, no `?`.
	mustContain(t, out, "masterPassword: string | Computed<string>;")

	mustContain(t, out, "export interface InstanceAttrs {")
	mustContain(t, out, "id: string;") // Attrs carries every field, plain type

	mustContain(t, out, `export const Instance: ResourceBinding<InstanceConfig, InstanceAttrs> = {`)
	// wireType carries the REAL, full wire type -- never shortened, even
	// though the exported identifier above dropped the "aws_db_" prefix.
	mustContain(t, out, `wireType: "aws_db_instance",`)
	mustContain(t, out, `instanceClass: "instance_class",`)
	mustContain(t, out, `allocatedStorage: "allocated_storage",`)
	// id never appears in the runtime fields map (not settable).
	if strings.Contains(out, `id: "id",`) {
		t.Fatalf("generated fields map should not include the computed-only id field:\n%s", out)
	}
}

func TestResourceFile_DropsProviderAndServicePrefix_FoundersLockedNamingScheme(t *testing.T) {
	// UBI-98's own founder comment, locked in verbatim, applied
	// identically across all three languages: ecr.Repository, never
	// AwsEcrRepository -- the directory already encodes provider+service,
	// so the redundant Aws<Service> prefix must be dropped from every
	// generated identifier name.
	rt := rt("aws_ecr_repository",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("name", ir.ScalarString, false, true, false, false),
	)
	out, err := ResourceFile("repository", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	mustContain(t, out, "export interface RepositoryConfig {")
	mustContain(t, out, "export const Repository:")
	mustContain(t, out, `wireType: "aws_ecr_repository",`)
	mustNotContain(t, out, "AwsEcrRepository")
	mustNotContain(t, out, "AwsRepository")
}

func TestResourceFile_ListSetMapOfScalar(t *testing.T) {
	listField := ir.Field{WireName: "security_group_ids", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindList, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}
	setField := ir.Field{WireName: "availability_zones", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindSet, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}
	mapField := ir.Field{WireName: "tags", Optional: true,
		Type: ir.TypeRef{Kind: ir.KindMap, Element: &ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString}}}

	rt := rt("aws_db_instance", listField, setField, mapField)
	out, err := ResourceFile("instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "securityGroupIds?: string[] | Computed<string[]>;")
	mustContain(t, out, "availabilityZones?: string[] | Computed<string[]>;")
	mustContain(t, out, "tags?: Record<string, string> | Computed<Record<string, string>>;")
	// Scalar collections need no recursive fields map -- plain wire-name string leaf.
	mustContain(t, out, `securityGroupIds: "security_group_ids",`)
	mustContain(t, out, `tags: "tags",`)
}

func TestResourceFile_NestedObjectBlock(t *testing.T) {
	nested := ir.Field{
		WireName: "settings",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	rt := rt("aws_thing", nested)
	out, err := ResourceFile("thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "export interface Thing_Settings {\n  enabled: boolean;\n}")
	mustContain(t, out, "settings?: Thing_Settings | Computed<Thing_Settings>;")
	mustContain(t, out, `wireName: "settings"`)
	mustContain(t, out, `kind: "object"`)
	mustContain(t, out, `enabled: "enabled",`)
}

func TestResourceFile_ListOfNestedObject(t *testing.T) {
	nested := ir.Field{
		WireName: "rule",
		Type: ir.TypeRef{Kind: ir.KindList, Element: &ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "from_port", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarNumber}},
		}}},
	}
	rt := rt("aws_security_group", nested)
	out, err := ResourceFile("security_group", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "export interface SecurityGroup_Rule {\n  fromPort: number;\n}")
	mustContain(t, out, "rule?: SecurityGroup_Rule[] | Computed<SecurityGroup_Rule[]>;")
	mustContain(t, out, `wireName: "rule"`)
	mustContain(t, out, `kind: "list"`)
	mustContain(t, out, `fromPort: "from_port",`)
}

func TestResourceFile_UnsupportedWireNameCharacters_Errors(t *testing.T) {
	rt := rt("aws_thing", scalarField("Weird-Name!", ir.ScalarString, false, true, false, false))
	if _, err := ResourceFile("thing", rt); err == nil {
		t.Fatal("ResourceFile: got nil error for an unsupported wire-name character, want an error")
	}
}

func TestResourceFile_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	rt := rt("aws_db_instance",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("instance_class", ir.ScalarString, false, true, false, false),
	)
	first, err := ResourceFile("instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := ResourceFile("instance", rt)
		if err != nil {
			t.Fatalf("ResourceFile (run %d): %v", i, err)
		}
		if again != first {
			t.Fatalf("run %d produced different output than run 0", i)
		}
	}
}

func TestGeneratedRepo_GroupsByServiceDirectory(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_ecr_repository", scalarField("id", ir.ScalarString, false, false, true, false)),
		rt("aws_iam_role", scalarField("id", ir.ScalarString, false, false, true, false)),
		rt("aws_iam_role_policy_attachment",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("role", ir.ScalarString, true, false, false, false),
		),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	wantPaths := []string{
		"sdk/typescript/package.json",
		"sdk/typescript/aws/ecr/doc.ts", "sdk/typescript/aws/ecr/repository.ts",
		"sdk/typescript/aws/iam/doc.ts", "sdk/typescript/aws/iam/role.ts", "sdk/typescript/aws/iam/role_policy_attachment.ts",
	}
	for _, p := range wantPaths {
		if _, ok := files[p]; !ok {
			t.Errorf("GeneratedRepo: missing expected path %q, got paths: %v", p, keys(files))
		}
	}
	if len(files) != len(wantPaths) {
		t.Errorf("GeneratedRepo: got %d files, want %d: %v", len(files), len(wantPaths), keys(files))
	}

	mustContain(t, files["sdk/typescript/package.json"], `"name": "@ubx/sdk-aws"`)

	mustContain(t, files["sdk/typescript/aws/ecr/doc.ts"], `__ubxSourceProvenance = { source: "hashicorp/aws", version: "6.54.0" }`)
	mustContain(t, files["sdk/typescript/aws/ecr/repository.ts"], "export const Repository:")
	// aws/ecr/repository.ts must NOT redeclare __ubxSourceProvenance --
	// that lives exactly once, in aws/ecr/doc.ts, for the whole directory.
	mustNotContain(t, files["sdk/typescript/aws/ecr/repository.ts"], "__ubxSourceProvenance")

	mustContain(t, files["sdk/typescript/aws/iam/role.ts"], "export const Role:")
	mustContain(t, files["sdk/typescript/aws/iam/role_policy_attachment.ts"], "export const RolePolicyAttachment:")
	mustContain(t, files["sdk/typescript/aws/iam/role_policy_attachment.ts"], `wireType: "aws_iam_role_policy_attachment",`)

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real declaration collisions: %v", err)
	}
}

func TestGeneratedRepo_BareTwoTokenType(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["sdk/typescript/aws/vpc/vpc.ts"]; !ok {
		t.Fatalf("GeneratedRepo: expected aws/vpc/vpc.ts for the bare \"aws_vpc\" type, got paths: %v", keys(files))
	}
	mustContain(t, files["sdk/typescript/aws/vpc/vpc.ts"], "export const Vpc:")
}

func TestGeneratedRepo_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_db_instance", scalarField("id", ir.ScalarString, false, false, true, false)),
		rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	first, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
		if err != nil {
			t.Fatalf("GeneratedRepo (run %d): %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d: got %d files, want %d", i, len(again), len(first))
		}
		for path, content := range first {
			if again[path] != content {
				t.Fatalf("run %d produced different content for %s than run 0", i, path)
			}
		}
	}
}

// TestResourceFile_RecursiveShape_DeduplicatesIdenticalStructs is the
// hermetic, small-fixture sibling of the real, live-verified finding
// this session (aws_wafv2_web_acl_rule's own recursive "statement" tree
// rendered to 16.7MB/~253,000 lines before this fix -- confirmed NOT to
// crash `deno check`, unlike Go's own compiler, but still a real
// reviewability/size problem worth fixing anyway, resourceRenderer's own
// doc comment): a schema shape that repeats IDENTICALLY at multiple
// depths must share one TS interface declaration, not mint a new,
// byte-identical one per depth.
func TestResourceFile_RecursiveShape_DeduplicatesIdenticalStructs(t *testing.T) {
	leaf := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
	}}
	level2 := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		{WireName: "child", Type: leaf},
	}}
	level1 := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		{WireName: "child", Type: level2},
	}}
	rt := rt("aws_thing", ir.Field{WireName: "statement", Type: level1})
	out, err := ResourceFile("thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "export interface Thing_Statement {")
	mustContain(t, out, "export interface Thing_Statement_Child {")
	if n := strings.Count(out, "export interface Thing_"); n != 3 {
		t.Fatalf("expected exactly 3 nested interface declarations, got %d:\n%s", n, out)
	}

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("output has real declaration collisions: %v", err)
	}
}

// TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared is
// this session's own real, live-verified fix, ported from Go: the
// nested-interface dedup above is COSMETIC (sdk/ts/runtime never reads a
// config value by a nested interface's own declared TYPE NAME -- TS
// types are erased entirely at runtime), but the runtime FieldMap object
// literal ResourceBinding.fields is actually built from is NOT cosmetic.
// This test asserts the actual mechanism: the SECOND occurrence of an
// identical shape's FieldSpec must reference a shared top-level const
// (`fields: <Name>Fields`), never re-inline a second full object literal.
func TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared(t *testing.T) {
	shape := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
	}}
	rt := rt("aws_thing",
		ir.Field{WireName: "primary_statement", Type: shape},
		ir.Field{WireName: "secondary_statement", Type: shape},
	)
	out, err := ResourceFile("thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	if n := strings.Count(out, "const Thing_PrimaryStatementFields: FieldMap = {"); n != 1 {
		t.Fatalf("expected exactly 1 hoisted shared const declaration, got %d:\n%s", n, out)
	}
	if n := strings.Count(out, `enabled: "enabled",`); n != 1 {
		t.Fatalf("expected exactly 1 inline enabled field spec (inside the shared const), got %d:\n%s", n, out)
	}
	mustContain(t, out, "fields: Thing_PrimaryStatementFields,")
	mustNotContain(t, out, "Thing_SecondaryStatementFields")

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("output has real declaration collisions: %v", err)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
