package pytmpl

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

	mustContain(t, out, "class InstanceConfig:")
	configBlock := out[strings.Index(out, "class InstanceConfig:"):]
	configBlock = configBlock[:strings.Index(configBlock, "\n\n")]
	mustNotContain(t, configBlock, "id:")
	mustContain(t, out, "instance_class: Any = None")
	mustContain(t, out, "allocated_storage: Any = None")
	mustContain(t, out, "master_password: Any = None")

	mustContain(t, out, "Instance = sdk.ResourceBinding(")
	// wire_type carries the REAL, full wire type -- never shortened, even
	// though the class name above dropped the "aws_db_" prefix.
	mustContain(t, out, `wire_type="aws_db_instance",`)
	mustContain(t, out, `"instance_class": sdk.FieldSpec(wire_name="instance_class"),`)
	mustContain(t, out, `"allocated_storage": sdk.FieldSpec(wire_name="allocated_storage"),`)
	if strings.Contains(out, `"id": sdk.FieldSpec`) {
		t.Fatalf("generated fields dict should not include the computed-only id field:\n%s", out)
	}
}

func TestResourceFile_DropsProviderAndServicePrefix_FoundersLockedNamingScheme(t *testing.T) {
	// UBI-98's own founder comment, locked in verbatim, applied
	// identically across all three languages: ecr.Repository, never
	// AwsEcrRepository -- the package already encodes provider+service,
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
	mustContain(t, out, "class RepositoryConfig:")
	mustContain(t, out, "Repository = sdk.ResourceBinding(")
	mustContain(t, out, `wire_type="aws_ecr_repository",`)
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

	mustContain(t, out, "security_group_ids: Any = None")
	mustContain(t, out, "availability_zones: Any = None")
	mustContain(t, out, "tags: Any = None")
	mustContain(t, out, `"security_group_ids": sdk.FieldSpec(wire_name="security_group_ids"),`)
	mustContain(t, out, `"tags": sdk.FieldSpec(wire_name="tags"),`)
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

	mustContain(t, out, "class Thing_Settings:\n    enabled: Any = None")
	mustContain(t, out, "settings: Any = None")
	mustContain(t, out, `wire_name="settings"`)
	mustContain(t, out, `kind="object"`)
	mustContain(t, out, `"enabled": sdk.FieldSpec(wire_name="enabled")`)
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

	mustContain(t, out, "class SecurityGroup_Rule:\n    from_port: Any = None")
	mustContain(t, out, "rule: Any = None")
	mustContain(t, out, `wire_name="rule"`)
	mustContain(t, out, `kind="list"`)
	mustContain(t, out, `"from_port": sdk.FieldSpec(wire_name="from_port")`)
}

func TestResourceFile_PythonKeywordCollision_GetsTrailingUnderscore(t *testing.T) {
	rt := rt("aws_thing", scalarField("class", ir.ScalarString, false, true, false, false))
	out, err := ResourceFile("thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	mustContain(t, out, "class_: Any = None")
	mustContain(t, out, `"class_": sdk.FieldSpec(wire_name="class")`)
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

func TestGeneratedRepo_GroupsByServicePackage(t *testing.T) {
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
		"pyproject.toml",
		"ecr/__init__.py", "ecr/repository.py",
		"iam/__init__.py", "iam/role.py", "iam/role_policy_attachment.py",
	}
	for _, p := range wantPaths {
		if _, ok := files[p]; !ok {
			t.Errorf("GeneratedRepo: missing expected path %q, got paths: %v", p, keys(files))
		}
	}
	if len(files) != len(wantPaths) {
		t.Errorf("GeneratedRepo: got %d files, want %d: %v", len(files), len(wantPaths), keys(files))
	}

	mustContain(t, files["pyproject.toml"], `name = "ubx-sdk-aws"`)

	mustContain(t, files["ecr/__init__.py"], `SOURCE_PROVENANCE = {"source": "hashicorp/aws", "version": "6.54.0"}`)
	mustContain(t, files["ecr/repository.py"], "Repository = sdk.ResourceBinding(")
	// ecr/repository.py must NOT redeclare SOURCE_PROVENANCE -- that
	// lives exactly once, in ecr/__init__.py, for the whole package.
	mustNotContain(t, files["ecr/repository.py"], "SOURCE_PROVENANCE")

	mustContain(t, files["iam/role.py"], "Role = sdk.ResourceBinding(")
	mustContain(t, files["iam/role_policy_attachment.py"], "RolePolicyAttachment = sdk.ResourceBinding(")
	mustContain(t, files["iam/role_policy_attachment.py"], `wire_type="aws_iam_role_policy_attachment",`)

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
	if _, ok := files["vpc/vpc.py"]; !ok {
		t.Fatalf("GeneratedRepo: expected vpc/vpc.py for the bare \"aws_vpc\" type, got paths: %v", keys(files))
	}
	mustContain(t, files["vpc/vpc.py"], "Vpc = sdk.ResourceBinding(")
}

// TestGeneratedRepo_ServiceNameIsPythonKeyword_Escaped is a real, live-
// verified edge case, not a hypothetical: "aws_lambda_function" and 19
// sibling real hashicorp/aws@6.54.0 types derive service "lambda" -- a
// Python keyword ("import lambda.function" is a SyntaxError).
func TestGeneratedRepo_ServiceNameIsPythonKeyword_Escaped(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_lambda_function", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["lambda_/function.py"]; !ok {
		t.Fatalf("GeneratedRepo: expected lambda_/function.py (escaped Python keyword), got paths: %v", keys(files))
	}
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
// rendered to 21.2MB/~258,000 lines/21,026 dataclasses before this fix --
// confirmed NOT to crash a real Python import, unlike Go's own compiler,
// but still a real reviewability/size problem worth fixing anyway,
// resourceRenderer's own doc comment): a schema shape that repeats
// IDENTICALLY at multiple depths must share one dataclass declaration,
// not mint a new, byte-identical one per depth.
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

	mustContain(t, out, "class Thing_Statement:")
	mustContain(t, out, "class Thing_Statement_Child:")
	if n := strings.Count(out, "class Thing_"); n != 3 {
		t.Fatalf("expected exactly 3 nested dataclass declarations, got %d:\n%s", n, out)
	}

	if err := CheckNoDuplicateDeclarations(out); err != nil {
		t.Fatalf("output has real declaration collisions: %v", err)
	}
}

// TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared is
// this session's own real, live-verified fix, ported from Go/TS: the
// nested-dataclass dedup above is COSMETIC (sdk/py/ubx_sdk never reads a
// config value by a nested dataclass's own declared CLASS NAME -- it
// reads dataclasses.fields()/getattr by FIELD NAME), but the runtime
// fields={...} dict ResourceBinding is built from is NOT cosmetic. This
// test asserts the actual mechanism: the SECOND occurrence of an
// identical shape's FieldSpec must reference a shared module-level
// variable, never re-inline a second full dict literal.
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

	if n := strings.Count(out, "_Thing_PrimaryStatementFields = {"); n != 1 {
		t.Fatalf("expected exactly 1 hoisted shared module-level variable, got %d:\n%s", n, out)
	}
	if n := strings.Count(out, `"enabled": sdk.FieldSpec(wire_name="enabled")`); n != 1 {
		t.Fatalf("expected exactly 1 inline enabled field spec (inside the shared var), got %d:\n%s", n, out)
	}
	mustContain(t, out, "fields=_Thing_PrimaryStatementFields,")
	mustNotContain(t, out, "_Thing_SecondaryStatementFields")

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
