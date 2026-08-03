package gotmpl

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
	out, err := ResourceFile("db", "instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "package db")
	mustContain(t, out, "type InstanceConfig struct {")
	// id is Computed-only -- never settable, must not appear in Config.
	configBlock := out[strings.Index(out, "type InstanceConfig struct {"):]
	configBlock = configBlock[:strings.Index(configBlock, "}")]
	mustNotContain(t, configBlock, "Id ")
	mustContain(t, out, "InstanceClass any")
	mustContain(t, out, "AllocatedStorage any")
	mustContain(t, out, "MasterPassword any")

	mustContain(t, out, `var Instance = sdk.ResourceBinding{`)
	// WireType carries the REAL, full wire type -- never shortened, even
	// though the Go identifier above dropped the "aws_db_" prefix.
	mustContain(t, out, `WireType: "aws_db_instance",`)
	mustContain(t, out, `"InstanceClass": sdk.FieldSpec{WireName: "instance_class"},`)
	mustContain(t, out, `"AllocatedStorage": sdk.FieldSpec{WireName: "allocated_storage"},`)
	// id never appears in the runtime fields map (not settable).
	if strings.Contains(out, `"Id":`) {
		t.Fatalf("generated fields map should not include the computed-only id field:\n%s", out)
	}
}

func TestResourceFile_DropsProviderAndServicePrefix_FoundersLockedNamingScheme(t *testing.T) {
	// UBI-98's own founder comment, locked in verbatim: ecr.Repository,
	// never generated.AwsEcrRepository -- the import path already
	// encodes provider+service, so the redundant Aws<Service> prefix
	// must be dropped from every generated type name.
	rt := rt("aws_ecr_repository",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("name", ir.ScalarString, false, true, false, false),
	)
	out, err := ResourceFile("ecr", "repository", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	mustContain(t, out, "package ecr")
	mustContain(t, out, "type RepositoryConfig struct {")
	mustContain(t, out, "var Repository = sdk.ResourceBinding{")
	mustContain(t, out, `WireType: "aws_ecr_repository",`)
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
	out, err := ResourceFile("db", "instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "SecurityGroupIds any")
	mustContain(t, out, "AvailabilityZones any")
	mustContain(t, out, "Tags any")
	// Scalar collections need no recursive fields map -- plain wire-name leaf.
	mustContain(t, out, `"SecurityGroupIds": sdk.FieldSpec{WireName: "security_group_ids"},`)
	mustContain(t, out, `"Tags": sdk.FieldSpec{WireName: "tags"},`)
}

func TestResourceFile_NestedObjectBlock(t *testing.T) {
	nested := ir.Field{
		WireName: "settings",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	rt := rt("aws_thing", nested)
	out, err := ResourceFile("thing", "thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "type Thing_Settings struct {\n\tEnabled any\n}")
	mustContain(t, out, "Settings any")
	mustContain(t, out, `WireName: "settings"`)
	mustContain(t, out, `Kind: "object"`)
	mustContain(t, out, `"Enabled": sdk.FieldSpec{WireName: "enabled"}`)
}

// TestResourceFile_RecursiveShape_DeduplicatesIdenticalStructs is the
// hermetic, small-fixture sibling of the real, live-verified finding
// this session (aws_wafv2_web_acl_rule's own recursive "statement" tree
// rendered to >10MB/~250,000 lines before this fix, enough on its own to
// crash the Go compiler): a schema shape that repeats IDENTICALLY at
// multiple depths must share one Go struct declaration, not mint a new,
// byte-identical one per depth. "level" here has the exact same shape
// (one "enabled" bool field, one nested "child" object of the identical
// shape) at three different paths -- top-level, one level down, two
// levels down -- exercising real depth-based repetition, not just two
// siblings with matching shapes.
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
	out, err := ResourceFile("thing", "thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	// level1 and level2 are DIFFERENT shapes (level1's own "child" is
	// level2, which has one more nested field than the innermost leaf) --
	// each gets its own declaration.
	mustContain(t, out, "type Thing_Statement struct {")
	mustContain(t, out, "type Thing_Statement_Child struct {")
	// The innermost leaf shape ({enabled bool}) is structurally IDENTICAL
	// to nothing else declared here by coincidence in this fixture -- the
	// real proof is the declaration COUNT: exactly 3 nested types (statement,
	// its child, and the child's own child), not 3 further "grandchild"
	// duplicates the old per-path-only scheme would have minted had this
	// fixture nested one level deeper.
	if n := strings.Count(out, "type Thing_"); n != 3 {
		t.Fatalf("expected exactly 3 nested struct declarations, got %d:\n%s", n, out)
	}

	if err := CheckNoDuplicateDeclarations(map[string]string{"thing.go": out}); err != nil {
		t.Fatalf("output has real package-level collisions: %v", err)
	}
}

// TestResourceFile_RecursiveShape_TrueRepeat is the sharper case: TWO
// DIFFERENT top-level fields whose nested shape is EXACTLY identical
// (same field, same type, recursively) -- the actual repeated-verbatim-
// block shape a real recursive AWS schema produces at successive depths.
// Must collapse to ONE shared struct declaration, reused by both fields.
func TestResourceFile_RecursiveShape_TrueRepeat(t *testing.T) {
	shape := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
	}}
	rt := rt("aws_thing",
		ir.Field{WireName: "primary_statement", Type: shape},
		ir.Field{WireName: "secondary_statement", Type: shape},
	)
	out, err := ResourceFile("thing", "thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	// Exactly ONE nested struct declaration, even though two different
	// top-level fields reference the identical shape -- the second one
	// shares the first's already-emitted type rather than duplicating it.
	if n := strings.Count(out, "struct {\n\tEnabled any\n}"); n != 1 {
		t.Fatalf("expected exactly 1 shared nested struct declaration for two identically-shaped fields, got %d:\n%s", n, out)
	}
	// Both fields are still individually present and correctly wire-mapped.
	mustContain(t, out, `WireName: "primary_statement"`)
	mustContain(t, out, `WireName: "secondary_statement"`)

	if err := CheckNoDuplicateDeclarations(map[string]string{"thing.go": out}); err != nil {
		t.Fatalf("output has real package-level collisions: %v", err)
	}
}

// TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared is
// this session's own real, live-verified fix -- the struct-declaration
// dedup above is COSMETIC (sdk/go/runtime never reads a nested value by
// its declared Go type name), but the runtime sdk.FieldMap{...} literal
// that ResourceBinding.Fields is actually built from is NOT cosmetic; it
// is what the runtime reads. Confirmed live against the real
// hashicorp/aws@6.54.0 schema: deduplicating only the struct
// declarations left aws_wafv2_web_acl_rule at ~6.5MB and the real
// full-provider build still crashed identically -- the FieldMap literal
// was still being re-inlined at every depth, unbounded. This test
// asserts the actual mechanism: the SECOND occurrence of an identical
// shape's FieldSpec must reference a shared top-level var (`Fields:
// <Name>Fields`), never re-inline a second full `sdk.FieldMap{...}`
// literal.
func TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared(t *testing.T) {
	shape := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
	}}
	rt := rt("aws_thing",
		ir.Field{WireName: "primary_statement", Type: shape},
		ir.Field{WireName: "secondary_statement", Type: shape},
	)
	out, err := ResourceFile("thing", "thing", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	// Exactly ONE hoisted shared FieldMap var for the shared shape (plus
	// the top-level ResourceBinding.Fields map itself -- 2 total, not 3),
	// and exactly ONE inline "Enabled": sdk.FieldSpec{...} entry anywhere
	// in the file (inside that one shared var) -- never a second copy
	// inlined directly into the second field's own FieldSpec.
	if n := strings.Count(out, "sdk.FieldMap{"); n != 2 {
		t.Fatalf("expected exactly 2 sdk.FieldMap{ literals (1 hoisted shared var + 1 top-level ResourceBinding.Fields), got %d:\n%s", n, out)
	}
	if n := strings.Count(out, "var Thing_PrimaryStatementFields = sdk.FieldMap{"); n != 1 {
		t.Fatalf("expected exactly 1 hoisted shared var declaration, got %d:\n%s", n, out)
	}
	if n := strings.Count(out, `"Enabled": sdk.FieldSpec{WireName: "enabled"}`); n != 1 {
		t.Fatalf("expected exactly 1 inline Enabled field spec (inside the shared var), got %d:\n%s", n, out)
	}
	// Both FieldSpecs reference the SAME shared var by name, not an
	// inline literal each.
	mustContain(t, out, "Fields: Thing_PrimaryStatementFields,")
	mustContain(t, out, "Fields: Thing_PrimaryStatementFields,") // secondary_statement shares primary_statement's own var (first-seen wins)
	mustNotContain(t, out, "Fields: Thing_SecondaryStatementFields")

	if err := CheckNoDuplicateDeclarations(map[string]string{"thing.go": out}); err != nil {
		t.Fatalf("output has real package-level collisions: %v", err)
	}
	requireGoToolchain(t)
	buildGeneratedRepo(t, map[string]string{
		"go.mod":     "module github.com/ubiquex/ubx-sdk-aws\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n",
		"thing/a.go": out,
	})
}

func TestResourceFile_ListOfNestedObject(t *testing.T) {
	nested := ir.Field{
		WireName: "rule",
		Type: ir.TypeRef{Kind: ir.KindList, Element: &ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "from_port", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarNumber}},
		}}},
	}
	rt := rt("aws_security_group", nested)
	out, err := ResourceFile("securitygroup", "security_group", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "type SecurityGroup_Rule struct {\n\tFromPort any\n}")
	mustContain(t, out, "Rule any")
	mustContain(t, out, `WireName: "rule"`)
	mustContain(t, out, `Kind: "list"`)
	mustContain(t, out, `"FromPort": sdk.FieldSpec{WireName: "from_port"}`)
}

func TestResourceFile_UnsupportedWireNameCharacters_Errors(t *testing.T) {
	rt := rt("aws_thing", scalarField("Weird-Name!", ir.ScalarString, false, true, false, false))
	if _, err := ResourceFile("thing", "thing", rt); err == nil {
		t.Fatal("ResourceFile: got nil error for an unsupported wire-name character, want an error")
	}
}

func TestResourceFile_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	rt := rt("aws_db_instance",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("instance_class", ir.ScalarString, false, true, false, false),
	)
	first, err := ResourceFile("db", "instance", rt)
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := ResourceFile("db", "instance", rt)
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
		"go.mod",
		"ecr/doc.go", "ecr/repository.go",
		"iam/doc.go", "iam/role.go", "iam/role_policy_attachment.go",
	}
	for _, p := range wantPaths {
		if _, ok := files[p]; !ok {
			t.Errorf("GeneratedRepo: missing expected path %q, got paths: %v", p, keys(files))
		}
	}
	if len(files) != len(wantPaths) {
		t.Errorf("GeneratedRepo: got %d files, want %d: %v", len(files), len(wantPaths), keys(files))
	}

	mustContain(t, files["go.mod"], "module github.com/ubiquex/ubx-sdk-aws")
	mustContain(t, files["go.mod"], "require github.com/ubiquex/ubx-sdk-go v0.0.0")

	mustContain(t, files["ecr/doc.go"], "package ecr")
	mustContain(t, files["ecr/doc.go"], `Source: "hashicorp/aws", Version: "6.54.0"`)
	mustContain(t, files["ecr/repository.go"], "package ecr")
	mustContain(t, files["ecr/repository.go"], "var Repository = sdk.ResourceBinding{")
	// ecr/repository.go must NOT redeclare SourceProvenance -- that lives
	// exactly once, in ecr/doc.go, for the whole package.
	mustNotContain(t, files["ecr/repository.go"], "SourceProvenance")

	mustContain(t, files["iam/role.go"], "var Role = sdk.ResourceBinding{")
	mustContain(t, files["iam/role_policy_attachment.go"], "var RolePolicyAttachment = sdk.ResourceBinding{")
	mustContain(t, files["iam/role_policy_attachment.go"], `WireType: "aws_iam_role_policy_attachment",`)

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real package-level collisions: %v", err)
	}
}

// TestGeneratedRepo_ServiceNameIsGoKeyword_Escaped is a real, live-
// verified edge case, not a hypothetical: "aws_default_vpc" and five
// sibling real hashicorp/aws@6.54.0 types derive service "default" --
// a Go keyword ("package default" is a syntax error, confirmed against
// the real full-provider generation this session). Must be escaped with
// a trailing underscore, matching sdk/codegen/templates/py's own
// pythonKeywords precedent.
func TestGeneratedRepo_ServiceNameIsGoKeyword_Escaped(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_default_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["default_/vpc.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected default_/vpc.go (escaped Go keyword), got paths: %v", keys(files))
	}
	mustContain(t, files["default_/vpc.go"], "package default_")
	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real package-level collisions: %v", err)
	}
}

// TestGeneratedRepo_ServiceNameIsGoBuildSpecial_Escaped is the "main"
// counterpart to the keyword test above -- a real, live-verified finding
// from the same session: "aws_main_route_table_association" derives
// service "main", which is not a Go keyword (legal syntax) but IS
// special to the go tool itself -- `go build ./...` treats a package
// literally named "main" as a command requiring `func main()`, which a
// generated bindings package never has, and fails loudly
// ("function main is undeclared in the main package") rather than just
// compiling as an importable library package.
func TestGeneratedRepo_ServiceNameIsGoBuildSpecial_Escaped(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_main_route_table_association", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["main_/route_table_association.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected main_/route_table_association.go (escaped go-tool-special name), got paths: %v", keys(files))
	}
	mustContain(t, files["main_/route_table_association.go"], "package main_")
	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real package-level collisions: %v", err)
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
	if _, ok := files["vpc/vpc.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected vpc/vpc.go for the bare \"aws_vpc\" type, got paths: %v", keys(files))
	}
	mustContain(t, files["vpc/vpc.go"], "package vpc")
	mustContain(t, files["vpc/vpc.go"], "var Vpc = sdk.ResourceBinding{")
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
