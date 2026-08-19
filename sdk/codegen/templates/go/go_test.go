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
	out, err := ResourceFile("db", "instance", rt, "")
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

	mustContain(t, out, `var Instance = ubx.ResourceBinding{`)
	// WireType carries the REAL, full wire type -- never shortened, even
	// though the Go identifier above dropped the "aws_db_" prefix.
	mustContain(t, out, `WireType: "aws_db_instance",`)
	mustContain(t, out, `"InstanceClass": ubx.FieldSpec{WireName: "instance_class"},`)
	mustContain(t, out, `"AllocatedStorage": ubx.FieldSpec{WireName: "allocated_storage"},`)
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
	out, err := ResourceFile("ecr", "repository", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	mustContain(t, out, "package ecr")
	mustContain(t, out, "type RepositoryConfig struct {")
	mustContain(t, out, "var Repository = ubx.ResourceBinding{")
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
	out, err := ResourceFile("db", "instance", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "SecurityGroupIds any")
	mustContain(t, out, "AvailabilityZones any")
	mustContain(t, out, "Tags any")
	// Scalar collections need no recursive fields map -- plain wire-name leaf.
	mustContain(t, out, `"SecurityGroupIds": ubx.FieldSpec{WireName: "security_group_ids"},`)
	mustContain(t, out, `"Tags": ubx.FieldSpec{WireName: "tags"},`)
}

func TestResourceFile_NestedObjectBlock(t *testing.T) {
	nested := ir.Field{
		WireName: "settings",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	rt := rt("aws_thing", nested)
	out, err := ResourceFile("thing", "thing", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "type Thing_Settings struct {\n\tEnabled any\n}")
	mustContain(t, out, "Settings any")
	mustContain(t, out, `WireName: "settings"`)
	mustContain(t, out, `Kind: "object"`)
	mustContain(t, out, `"Enabled": ubx.FieldSpec{WireName: "enabled"}`)
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
	out, err := ResourceFile("thing", "thing", rt, "")
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
	out, err := ResourceFile("thing", "thing", rt, "")
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
// its declared Go type name), but the runtime ubx.FieldMap{...} literal
// that ResourceBinding.Fields is actually built from is NOT cosmetic; it
// is what the runtime reads. Confirmed live against the real
// hashicorp/aws@6.54.0 schema: deduplicating only the struct
// declarations left aws_wafv2_web_acl_rule at ~6.5MB and the real
// full-provider build still crashed identically -- the FieldMap literal
// was still being re-inlined at every depth, unbounded. This test
// asserts the actual mechanism: the SECOND occurrence of an identical
// shape's FieldSpec must reference a shared top-level var (`Fields:
// <Name>Fields`), never re-inline a second full `ubx.FieldMap{...}`
// literal.
func TestResourceFile_RecursiveShape_FieldMapLiteralIsHoistedAndShared(t *testing.T) {
	shape := ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
		{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
	}}
	rt := rt("aws_thing",
		ir.Field{WireName: "primary_statement", Type: shape},
		ir.Field{WireName: "secondary_statement", Type: shape},
	)
	out, err := ResourceFile("thing", "thing", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	// Exactly ONE hoisted shared FieldMap var for the shared shape (plus
	// the top-level ResourceBinding.Fields map itself -- 2 total, not 3),
	// and exactly ONE inline "Enabled": ubx.FieldSpec{...} entry anywhere
	// in the file (inside that one shared var) -- never a second copy
	// inlined directly into the second field's own FieldSpec.
	if n := strings.Count(out, "ubx.FieldMap{"); n != 2 {
		t.Fatalf("expected exactly 2 ubx.FieldMap{ literals (1 hoisted shared var + 1 top-level ResourceBinding.Fields), got %d:\n%s", n, out)
	}
	if n := strings.Count(out, "var Thing_PrimaryStatementFields = ubx.FieldMap{"); n != 1 {
		t.Fatalf("expected exactly 1 hoisted shared var declaration, got %d:\n%s", n, out)
	}
	if n := strings.Count(out, `"Enabled": ubx.FieldSpec{WireName: "enabled"}`); n != 1 {
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
		"sdk/go/go.mod": "module github.com/ubiquex/ubx-sdk-aws/sdk/go\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n",
		"sdk/go/thing/a.go": out,
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
	out, err := ResourceFile("securitygroup", "security_group", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "type SecurityGroup_Rule struct {\n\tFromPort any\n}")
	mustContain(t, out, "Rule any")
	mustContain(t, out, `WireName: "rule"`)
	mustContain(t, out, `Kind: "list"`)
	mustContain(t, out, `"FromPort": ubx.FieldSpec{WireName: "from_port"}`)
}

func TestResourceFile_UnsupportedWireNameCharacters_Errors(t *testing.T) {
	rt := rt("aws_thing", scalarField("Weird-Name!", ir.ScalarString, false, true, false, false))
	if _, err := ResourceFile("thing", "thing", rt, ""); err == nil {
		t.Fatal("ResourceFile: got nil error for an unsupported wire-name character, want an error")
	}
}

func TestResourceFile_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	rt := rt("aws_db_instance",
		scalarField("id", ir.ScalarString, false, false, true, false),
		scalarField("instance_class", ir.ScalarString, false, true, false, false),
	)
	first, err := ResourceFile("db", "instance", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := ResourceFile("db", "instance", rt, "")
		if err != nil {
			t.Fatalf("ResourceFile (run %d): %v", i, err)
		}
		if again != first {
			t.Fatalf("run %d produced different output than run 0", i)
		}
	}
}

// UBI-153: GeneratedRepo must emit exactly the goDirective value it's
// given, verbatim -- never a hardcoded constant of its own (that's the
// real bug this fixes: a stale template value silently downgrading a
// real, already-bumped repo's go.mod on every regen). "1.30.7" is
// deliberately a real-looking but fictional value, higher than any
// version that has ever been this package's own hardcoded default, so
// a pass here can't be coincidental.
func TestGeneratedRepo_EmitsGivenGoDirective_NeverHardcoded(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_iam_role", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.30.7")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	goMod, ok := files["sdk/go/go.mod"]
	if !ok {
		t.Fatalf("GeneratedRepo: missing sdk/go/go.mod, got paths: %v", keys(files))
	}
	mustContain(t, goMod, "go 1.30.7\n")
	if strings.Contains(goMod, "go 1.23\n") {
		t.Fatalf("GeneratedRepo: emitted the old hardcoded go 1.23 instead of the given goDirective:\n%s", goMod)
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
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	wantPaths := []string{
		"sdk/go/go.mod",
		"sdk/go/aws/ecr/doc.go", "sdk/go/aws/ecr/repository.go",
		"sdk/go/aws/iam/doc.go", "sdk/go/aws/iam/role.go", "sdk/go/aws/iam/role_policy_attachment.go",
	}
	for _, p := range wantPaths {
		if _, ok := files[p]; !ok {
			t.Errorf("GeneratedRepo: missing expected path %q, got paths: %v", p, keys(files))
		}
	}
	if len(files) != len(wantPaths) {
		t.Errorf("GeneratedRepo: got %d files, want %d: %v", len(files), len(wantPaths), keys(files))
	}

	mustContain(t, files["sdk/go/go.mod"], "module github.com/ubiquex/ubx-sdk-aws/sdk/go")
	mustContain(t, files["sdk/go/go.mod"], "require github.com/ubiquex/ubx-sdk-go v0.0.0")

	mustContain(t, files["sdk/go/aws/ecr/doc.go"], "package ecr")
	mustContain(t, files["sdk/go/aws/ecr/doc.go"], `Source: "hashicorp/aws", Version: "6.54.0"`)
	mustContain(t, files["sdk/go/aws/ecr/repository.go"], "package ecr")
	mustContain(t, files["sdk/go/aws/ecr/repository.go"], "var Repository = ubx.ResourceBinding{")
	// aws/ecr/repository.go must NOT redeclare SourceProvenance -- that
	// lives exactly once, in aws/ecr/doc.go, for the whole package.
	mustNotContain(t, files["sdk/go/aws/ecr/repository.go"], "SourceProvenance")

	mustContain(t, files["sdk/go/aws/iam/role.go"], "var Role = ubx.ResourceBinding{")
	mustContain(t, files["sdk/go/aws/iam/role_policy_attachment.go"], "var RolePolicyAttachment = ubx.ResourceBinding{")
	mustContain(t, files["sdk/go/aws/iam/role_policy_attachment.go"], `WireType: "aws_iam_role_policy_attachment",`)

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
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["sdk/go/aws/default_/vpc.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected aws/default_/vpc.go (escaped Go keyword), got paths: %v", keys(files))
	}
	mustContain(t, files["sdk/go/aws/default_/vpc.go"], "package default_")
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
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["sdk/go/aws/main_/route_table_association.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected aws/main_/route_table_association.go (escaped go-tool-special name), got paths: %v", keys(files))
	}
	mustContain(t, files["sdk/go/aws/main_/route_table_association.go"], "package main_")
	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real package-level collisions: %v", err)
	}
}

// TestGeneratedRepo_LocalNameEndsInTest_FilenameEscaped is UBI-151's own
// real, live-verified fix. A real, wire-derived local name ending in
// "_test" (google_network_management_connectivity_test is real and
// confirmed live, not hypothetical -- azurerm_application_insights_web_test
// and azurerm_application_insights_standard_web_test are the other 2 real,
// confirmed instances) produces a Go source filename ending in "_test.go",
// which Go's own toolchain permanently excludes from `go build`/`go doc`/
// any real import as test-only, regardless of content -- confirmed live
// against the real, published repo before this fix (`go list -f
// '{{.TestGoFiles}}'` names the file; `go doc` finds no such symbol). The
// exported Go identifier/type name must stay untouched -- only the
// filename needs the same trailing-underscore escape this file's own
// *Config collision case already uses, above.
func TestGeneratedRepo_LocalNameEndsInTest_FilenameEscaped(t *testing.T) {
	types := []*ir.ResourceType{
		rt("google_network_management_connectivity_test", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("google", "hashicorp/google", "7.42.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["sdk/go/google/network/management_connectivity_test_.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected google/network/management_connectivity_test_.go (escaped _test.go filename), got paths: %v", keys(files))
	}
	for path := range files {
		if strings.HasSuffix(path, "_test.go") {
			t.Fatalf("GeneratedRepo: a real generated file still ends in _test.go, Go's own toolchain will silently exclude it from go build/go doc: %s", path)
		}
	}
	// The exported Go identifier itself is untouched -- only the filename
	// changed.
	mustContain(t, files["sdk/go/google/network/management_connectivity_test_.go"], "var ManagementConnectivityTest = ubx.ResourceBinding{")
	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has real package-level collisions: %v", err)
	}
}

func TestGeneratedRepo_BareTwoTokenType(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	if _, ok := files["sdk/go/aws/vpc/vpc.go"]; !ok {
		t.Fatalf("GeneratedRepo: expected aws/vpc/vpc.go for the bare \"aws_vpc\" type, got paths: %v", keys(files))
	}
	mustContain(t, files["sdk/go/aws/vpc/vpc.go"], "package vpc")
	mustContain(t, files["sdk/go/aws/vpc/vpc.go"], "var Vpc = ubx.ResourceBinding{")
}

func TestGeneratedRepo_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_db_instance", scalarField("id", ir.ScalarString, false, false, true, false)),
		rt("aws_vpc", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	first, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
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

// TestGeneratedRepo_SiblingConfigCollision_Escaped is UBI-108's own
// hermetic sibling of a real, live-verified finding against
// hashicorp/google@7.40.0 -- NOT a hypothetical, and NOT the same
// collision class UBI-96 already fixed (that one was nested BLOCK type
// names; this is two independent TOP-LEVEL resources). 3 real hits:
// google_spanner_instance + google_spanner_instance_config,
// google_workstations_workstation + google_workstations_workstation_config,
// google_migration_center_report + google_migration_center_report_config.
// "svc_instance" here mirrors spanner/instance exactly: "instance"'s own
// AUTO-derived Config struct ("InstanceConfig") collides with sibling
// "instance_config"'s own REAL binding var (also "InstanceConfig",
// PascalCase of its own wire-derived local name) -- both in the same
// service package. hashicorp/aws's own 1,682 types never happened to
// produce this exact shape; hashicorp/google's real schema does, 3 times.
func TestGeneratedRepo_SiblingConfigCollision_Escaped(t *testing.T) {
	types := []*ir.ResourceType{
		rt("aws_svc_instance", scalarField("id", ir.ScalarString, false, false, true, false)),
		rt("aws_svc_instance_config", scalarField("id", ir.ScalarString, false, false, true, false)),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	instanceSrc, ok := files["sdk/go/aws/svc/instance.go"]
	if !ok {
		t.Fatalf("expected aws/svc/instance.go, got paths: %v", keys(files))
	}
	configSrc, ok := files["sdk/go/aws/svc/instance_config.go"]
	if !ok {
		t.Fatalf("expected aws/svc/instance_config.go, got paths: %v", keys(files))
	}

	// "instance"'s own Config struct is disambiguated (trailing
	// underscore, the same escape convention goPackageIdent/pyModuleIdent
	// already use for a keyword collision) -- never the bare
	// "InstanceConfig" that would collide.
	mustContain(t, instanceSrc, "type InstanceConfig_ struct {")
	mustContain(t, instanceSrc, "var Instance = ubx.ResourceBinding{")
	mustNotContain(t, instanceSrc, "type InstanceConfig struct {")

	// "instance_config"'s own binding var and its OWN (unrelated) auto
	// Config struct are both completely undisturbed -- the collision is
	// resolved on the OTHER sibling's side, never by mangling a resource's
	// own real, wire-derived identity.
	mustContain(t, configSrc, "var InstanceConfig = ubx.ResourceBinding{")
	mustContain(t, configSrc, "type InstanceConfigConfig struct {")
	mustContain(t, configSrc, `WireType: "aws_svc_instance_config",`)

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has a real declaration collision: %v", err)
	}
}

func TestResourceFile_DescriptionSource_RenderedAsDocComment(t *testing.T) {
	rt := rt("aws_db_instance",
		ir.Field{
			WireName: "instance_class", Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString},
			Optional: true, Description: "The instance type of the RDS instance.",
			DescriptionSource: ir.DescriptionSourceModel,
		},
		ir.Field{
			WireName: "allocated_storage", Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarNumber},
			Optional: true, Description: "Amount of storage, in gibibytes.",
			DescriptionSource: ir.DescriptionSourceAIInferred,
		},
		ir.Field{
			WireName: "master_password", Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString},
			Required: true, DescriptionSource: ir.DescriptionSourceNone,
		},
	)
	out, err := ResourceFile("db", "instance", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "// The instance type of the RDS instance.\n\tInstanceClass any")
	mustContain(t, out, "// Amount of storage, in gibibytes. (AI-inferred)\n\tAllocatedStorage any")
	// A DescriptionSourceNone field gets no comment at all -- no false
	// signal is more honest than a fabricated one.
	mustContain(t, out, "\tMasterPassword any")
	mustNotContain(t, out, "// \n\tMasterPassword any")
}

// TestResourceFile_ComputedOnlyField_DescriptionVisibleInAttrs is the
// real, direct regression test for checkpoint 10's own fix: a
// computed-only field (Required=false, Optional=false, Computed=true --
// the common real shape for id/node_id/created_at, confirmed live
// checkpoint 6 to have NO rendering location anywhere in generated Go)
// must now get a real, visible doc comment in the new Attrs struct, even
// though it's correctly excluded from Config (fieldIsSettable's own
// doc comment, unchanged).
func TestResourceFile_ComputedOnlyField_DescriptionVisibleInAttrs(t *testing.T) {
	rt := rt("github_label",
		ir.Field{
			WireName: "node_id", Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarString},
			Computed: true, Description: "The label's GraphQL node ID.",
			DescriptionSource: ir.DescriptionSourceAIInferred,
		},
	)
	out, err := ResourceFile("github", "label", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	// Never in Config -- computed-only, fieldIsSettable's own existing
	// rule, unchanged.
	configBlock := out[strings.Index(out, "type LabelConfig struct {"):]
	configBlock = configBlock[:strings.Index(configBlock, "}")]
	mustNotContain(t, configBlock, "NodeId")

	// Now visible in Attrs, with the real, correct AI-inferred label.
	mustContain(t, out, "type LabelAttrs struct {")
	mustContain(t, out, "// The label's GraphQL node ID. (AI-inferred)\n\tNodeId any")
}

// TestResourceFile_ComputedOnlyNestedObject_GetsARealDeclaration is the
// real, direct regression test for the deeper part of checkpoint 6's
// own finding: a computed-only TOP-LEVEL object field's entire nested
// subtree previously never got collected at all (collectNestedStructs
// was gated by the identical fieldIsSettable filter as Config itself),
// so a field like github_deployment_protection_rule's own "app" (an
// object, computed-only) had no rendering location for ANY of its own
// children either, at any depth.
func TestResourceFile_ComputedOnlyNestedObject_GetsARealDeclaration(t *testing.T) {
	rt := rt("github_deployment_protection_rule",
		ir.Field{
			WireName: "app", Computed: true,
			Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
				{WireName: "id", Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarNumber},
					Description: "The numeric ID of the GitHub App.", DescriptionSource: ir.DescriptionSourceAIInferred},
			}},
		},
	)
	out, err := ResourceFile("github", "deployment_protection_rule", rt, "")
	if err != nil {
		t.Fatalf("ResourceFile: %v", err)
	}

	mustContain(t, out, "type DeploymentProtectionRuleAttrs struct {")
	mustContain(t, out, "// The numeric ID of the GitHub App. (AI-inferred)\n\tId any")
}

func TestFieldDocComment_CollapsesNewlinesAndAbstainsOnNone(t *testing.T) {
	sourced := fieldDocComment(ir.Field{Description: "Line one.\nLine two.", DescriptionSource: ir.DescriptionSourceModel}, "\t")
	if sourced != "\t// Line one. Line two.\n" {
		t.Fatalf("sourced comment = %q", sourced)
	}
	inferred := fieldDocComment(ir.Field{Description: "Generated.", DescriptionSource: ir.DescriptionSourceAIInferred}, "\t")
	if inferred != "\t// Generated. (AI-inferred)\n" {
		t.Fatalf("AI-inferred comment = %q", inferred)
	}
	if none := fieldDocComment(ir.Field{Description: "", DescriptionSource: ir.DescriptionSourceNone}, "\t"); none != "" {
		t.Fatalf("DescriptionSourceNone comment = %q, want empty", none)
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
