package diagram

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// fakeSchema is a hermetic resolver.SchemaInspector -- mirrors
// core/resolver's own test fake (package-private there, so this
// package needs its own).
type fakeSchema struct {
	types map[string]bool
	// required is MissingRequiredKeys' own opt-in fake (UBI-90), mirroring
	// core/resolver's own fakeSchema exactly: typeName -> set of attribute
	// names Required in that type's own (fake) schema. A type never
	// listed here is never flagged, so every pre-existing test in this
	// package (none of which cares about required-attribute validation)
	// stays completely unaffected.
	required map[string]map[string]bool
}

func (f *fakeSchema) HasType(t string) bool           { return f.types[t] }
func (f *fakeSchema) IsComputed(t, path string) bool  { return false }
func (f *fakeSchema) IsSensitive(t, path string) bool { return false }

// UnknownConfigKeys is an always-nil stub (UBI-66) -- this package's own
// tests are about diagram parsing/type inference, never schema-key
// validation, which core/resolver's own fakeSchema already covers.
func (f *fakeSchema) UnknownConfigKeys(t string, config map[string]interface{}) []resolver.ConfigKeyIssue {
	return nil
}

// MissingRequiredKeys reports every attribute name listed in f.required[t]
// that config has no key for at all -- UBI-90's own opt-in fake, mirroring
// core/resolver's own fakeSchema.MissingRequiredKeys exactly. A type never
// listed in f.required (every pre-existing fixture in this package) always
// reports no issues, same "existing tests stay unaffected" discipline
// UnknownConfigKeys' own fake already established.
func (f *fakeSchema) MissingRequiredKeys(t string, config map[string]interface{}) []resolver.RequiredAttributeIssue {
	req := f.required[t]
	if len(req) == 0 {
		return nil
	}
	names := make([]string, 0, len(req))
	for name := range req {
		names = append(names, name)
	}
	sort.Strings(names)
	var issues []resolver.RequiredAttributeIssue
	for _, name := range names {
		if _, present := config[name]; !present {
			issues = append(issues, resolver.RequiredAttributeIssue{Path: name})
		}
	}
	return issues
}

func awsProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "hashicorp/aws",
		Version: "6.54.0",
		Schema:  &fakeSchema{types: map[string]bool{"aws_db_instance": true, "aws_vpc": true, "aws_ecs_service": true}},
	}
}

// awsIAMRoleProvider is UBI-90's own dedicated fixture: aws_iam_role,
// schema-Required "assume_role_policy" -- the founder's own exact live
// repro type (playground-13), a real AWS attribute a diagram's own
// topology-only design structurally cannot express (it isn't a topology
// signal at all -- no node/container/edge could ever author it). A
// separate provider fixture from awsProvider() on purpose: aws_vpc/
// aws_db_instance/aws_ecs_service there stay required-attribute-free so
// every pre-existing happy-path test (which never supplies config, by the
// lossy-medium rule) keeps resolving cleanly -- only this one type, used
// only by the new adversarial case, carries a Required attribute at all.
func awsIAMRoleProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "hashicorp/aws",
		Version: "6.54.0",
		Schema: &fakeSchema{
			types:    map[string]bool{"aws_iam_role": true},
			required: map[string]map[string]bool{"aws_iam_role": {"assume_role_policy": true}},
		},
	}
}

// awsIAMRoleFullProvider is UBI-91's own dedicated fixture: aws_iam_role
// with BOTH real Required attributes modeled (name, assume_role_policy --
// awsIAMRoleProvider, above, deliberately only models one, for UBI-90's
// own narrower test; kept untouched rather than widened, so this session
// adds its own fixture instead of risking a shared one drifting under two
// sessions' different needs). Matches the founder's own real
// aws_iam_role schema shape (provider/schemakeys_test.go's own
// awsIAMRoleLikeBlock) closely enough to exercise the multi-attribute
// ubx_required case realistically.
func awsIAMRoleFullProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "hashicorp/aws",
		Version: "6.54.0",
		Schema: &fakeSchema{
			types:    map[string]bool{"aws_iam_role": true},
			required: map[string]map[string]bool{"aws_iam_role": {"name": true, "assume_role_policy": true}},
		},
	}
}

// ambiguousProviders declares TWO providers that both own "widget" --
// row 2b, type ambiguous across providers.
func ambiguousProviders() []resolver.DeclaredProvider {
	return []resolver.DeclaredProvider{
		{Source: "acme/one", Version: "1.0.0", Schema: &fakeSchema{types: map[string]bool{"widget": true}}},
		{Source: "acme/two", Version: "1.0.0", Schema: &fakeSchema{types: map[string]bool{"widget": true}}},
	}
}

func mustParse(t *testing.T, src, stack string, providers []resolver.DeclaredProvider, opts Options) *resolver.IntentFile {
	t.Helper()
	intent, err := Parse("test.d2", strings.NewReader(src), stack, providers, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return intent
}

func resourceByName(t *testing.T, intent *resolver.IntentFile, name string) resolver.ResourceIntent {
	t.Helper()
	for _, r := range intent.Resources {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no resource named %q in %+v", name, intent.Resources)
	return resolver.ResourceIntent{}
}

func TestParse_TypedNodesAndEdge_BecomesResourcesAndDependsOn(t *testing.T) {
	src := `
classes: {
  aws_vpc: {}
  aws_db_instance: {}
}
payments: {
  vpc: main-vpc {
    class: aws_vpc
  }
  db: primary-db {
    class: aws_db_instance
  }
}
payments.db -> payments.vpc
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})

	if intent.SchemaVersion != 1 || intent.Kind != resolver.IntentFileKind || intent.Stack != "payments" {
		t.Fatalf("unexpected header: %+v", intent)
	}
	if len(intent.Resources) != 2 {
		t.Fatalf("resources = %+v, want 2", intent.Resources)
	}

	vpc := resourceByName(t, intent, "main-vpc")
	if vpc.Type != "aws_vpc" || vpc.Op != resolver.OpCreate {
		t.Fatalf("vpc = %+v", vpc)
	}
	db := resourceByName(t, intent, "primary-db")
	if db.Type != "aws_db_instance" {
		t.Fatalf("db = %+v", db)
	}
	if len(db.DependsOn) != 1 || db.DependsOn[0] != "payments.aws_vpc.main-vpc" {
		t.Fatalf("db.DependsOn = %v, want [payments.aws_vpc.main-vpc]", db.DependsOn)
	}
	if len(intent.Intent.Questions) != 0 {
		t.Fatalf("questions = %+v, want none", intent.Intent.Questions)
	}
}

func TestParse_ContainersArePureGrouping(t *testing.T) {
	// Deeply nested containers -- the leaf's own name is its label only,
	// never a container-path-prefixed name.
	src := `
classes: {
  aws_vpc: {}
}
payments: {
  networking: {
    subnet: {
      vpc: main-vpc {
        class: aws_vpc
      }
    }
  }
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want 1", intent.Resources)
	}
	if intent.Resources[0].Name != "main-vpc" {
		t.Fatalf("Name = %q, want \"main-vpc\" (container nesting must not be folded in)", intent.Resources[0].Name)
	}
}

func TestParse_NoClass_ExcludedWithBlockingQuestion(t *testing.T) {
	src := `
db: primary-db
`
	intent := mustParse(t, "classes: {}\n"+src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none (class-less node excluded)", intent.Resources)
	}
	if len(intent.Intent.Questions) != 1 {
		t.Fatalf("questions = %+v, want exactly 1", intent.Intent.Questions)
	}
	q := intent.Intent.Questions[0]
	if !q.Blocking {
		t.Fatalf("Blocking = false, want true")
	}
	if !strings.Contains(q.Text, "no class:") {
		t.Fatalf("question text = %q, want it to explain the missing class:", q.Text)
	}
	if len(q.Affects) != 1 || q.Affects[0] != "db" {
		t.Fatalf("Affects = %v, want [db]", q.Affects)
	}
}

func TestParse_AmbiguousType_ExcludedWithBlockingQuestion(t *testing.T) {
	src := `
classes: {
  widget: {}
}
w: primary {
  class: widget
}
`
	intent := mustParse(t, src, "payments", ambiguousProviders(), Options{})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none", intent.Resources)
	}
	if len(intent.Intent.Questions) != 1 || !intent.Intent.Questions[0].Blocking {
		t.Fatalf("questions = %+v, want exactly 1 blocking", intent.Intent.Questions)
	}
}

func TestParse_UnknownType_ExcludedWithBlockingQuestion(t *testing.T) {
	src := `
classes: {
  ghost_type: {}
}
g: primary {
  class: ghost_type
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none", intent.Resources)
	}
	if len(intent.Intent.Questions) != 1 || !intent.Intent.Questions[0].Blocking {
		t.Fatalf("questions = %+v, want exactly 1 blocking", intent.Intent.Questions)
	}
}

func TestParse_DuplicateLabelAcrossContainers_BothIncluded_NotDeduplicated(t *testing.T) {
	// The parser itself never deduplicates or disambiguates -- that's
	// core/resolver's own existing ErrDuplicateResource, reused
	// unchanged (docs/diagram-medium.md's own "Containment ambiguity"
	// section). Confirms the parser produces BOTH entries, letting
	// resolve() be the one that refuses.
	src := `
classes: {
  aws_db_instance: {}
}
primary: {
  db: same-name {
    class: aws_db_instance
  }
}
replica: {
  db: same-name {
    class: aws_db_instance
  }
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 2 {
		t.Fatalf("resources = %+v, want 2 (both present, letting resolve's own ErrDuplicateResource fire)", intent.Resources)
	}
	for _, r := range intent.Resources {
		if r.Name != "same-name" {
			t.Fatalf("resource = %+v, want Name \"same-name\"", r)
		}
	}
}

func TestParse_CrossStackReference_LabelForm(t *testing.T) {
	root := t.TempDir()
	diagramDir := filepath.Join(root, "payments")
	if err := os.MkdirAll(diagramDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "networking"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `
classes: {
  aws_db_instance: {}
  external: {}
}
vpc_ref: "@networking.aws_vpc.main" {
  class: external
}
payments: {
  db: primary-db {
    class: aws_db_instance
  }
}
payments.db -> vpc_ref
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{BaseDir: diagramDir})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want 1 (the reference node is never a resource)", intent.Resources)
	}
	if len(intent.Intent.Defaults) != 1 {
		t.Fatalf("defaults = %+v, want exactly 1 (the cross-stack edge note)", intent.Intent.Defaults)
	}
	note := intent.Intent.Defaults[0]
	if !strings.Contains(note.Text, "networking.aws_vpc.main") {
		t.Fatalf("note text = %q, want it to name the reference address", note.Text)
	}
	if len(note.Affects) != 1 || note.Affects[0] != "payments.aws_db_instance.primary-db" {
		t.Fatalf("note.Affects = %v", note.Affects)
	}
	// The reference itself must never become a depends_on entry -- it's
	// not a real wire-level dependency in v1 (see the note instead).
	db := resourceByName(t, intent, "primary-db")
	if len(db.DependsOn) != 0 {
		t.Fatalf("db.DependsOn = %v, want none", db.DependsOn)
	}
}

func TestParse_CrossStackReference_UnresolvableStack_Rejected(t *testing.T) {
	dir := t.TempDir() // no "networking" sibling directory created
	src := `
classes: {
  external: {}
}
vpc_ref: "@networking.aws_vpc.main" {
  class: external
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", []resolver.DeclaredProvider{awsProvider()}, Options{BaseDir: dir})
	if err == nil {
		t.Fatal("Parse: got nil error for an unresolvable stack reference, want an error")
	}
	if !strings.Contains(err.Error(), "networking") {
		t.Fatalf("error = %v, want it to name the unresolvable stack", err)
	}
}

func TestParse_CrossStackReference_NeighborLedgerOverride(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "somewhere-else")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `
classes: {
  external: {}
}
vpc_ref: "@networking.aws_vpc.main" {
  class: external
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", []resolver.DeclaredProvider{awsProvider()},
		Options{BaseDir: dir, NeighborLedgers: map[string]string{"networking": overrideDir}})
	if err != nil {
		t.Fatalf("Parse: %v, want success via the --neighbor-ledger override", err)
	}
}

func TestParse_D2SyntaxError_SurfacedVerbatim(t *testing.T) {
	_, err := Parse("bad.d2", strings.NewReader("a: {\n  unclosed"), "payments", nil, Options{})
	if err == nil {
		t.Fatal("Parse: got nil error for malformed D2 syntax, want an error")
	}
}

func TestParse_ClassLessLabelStartingWithAt_TreatedAsReference(t *testing.T) {
	// "Grammar, pinned: a node is a cross-stack reference ... when EITHER
	// its label starts with @ ... OR its class is exactly external" --
	// the label form alone, no class: external, must also work.
	root := t.TempDir()
	diagramDir := filepath.Join(root, "payments")
	if err := os.MkdirAll(diagramDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "networking"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `vpc_ref: "@networking.aws_vpc.main"`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{BaseDir: diagramDir})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none", intent.Resources)
	}
}

func TestParse_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	src := `
classes: {
  aws_vpc: {}
  aws_db_instance: {}
}
payments: {
  vpc: main-vpc { class: aws_vpc }
  db: primary-db { class: aws_db_instance }
  api: api-server { class: aws_db_instance }
}
payments.db -> payments.vpc
payments.api -> payments.vpc
payments.api -> payments.db
`
	first := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	for i := 0; i < 5; i++ {
		again := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
		if len(again.Resources) != len(first.Resources) {
			t.Fatalf("run %d: resource count differs", i)
		}
		for j := range first.Resources {
			if first.Resources[j].Name != again.Resources[j].Name || first.Resources[j].Type != again.Resources[j].Type {
				t.Fatalf("run %d: resource %d differs: %+v vs %+v", i, j, first.Resources[j], again.Resources[j])
			}
			if strings.Join(first.Resources[j].DependsOn, ",") != strings.Join(again.Resources[j].DependsOn, ",") {
				t.Fatalf("run %d: resource %d DependsOn differs: %v vs %v", i, j, first.Resources[j].DependsOn, again.Resources[j].DependsOn)
			}
		}
	}
}

// TestParse_UbxRequired_AllSuppliedResolvesClean is UBI-91's own happy
// path: every Required attribute (name -- a plain scalar; assume_role_
// policy -- a JSON policy document, authored via D2's own |md ... |
// block-string syntax) supplied via ubx_required.* on the node itself.
// Confirms two things at once: the node is STILL classified as a real
// resource (not silently demoted to a container by its own ubx_required
// child -- the exact failure mode confirmed empirically before writing
// sortedLeaves' own fix), and the values flow into Config correctly.
func TestParse_UbxRequired_AllSuppliedResolvesClean(t *testing.T) {
	src := `
classes: {
  aws_iam_role: {}
}
role: "ci-runner-v13" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
  ubx_required.assume_role_policy: |md
    {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "sts:AssumeRole"}]}
  |
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want exactly 1 (the ubx_required child must never be classified as its own resource/container)", intent.Resources)
	}
	role := resourceByName(t, intent, "ci-runner-v13")
	if role.Type != "aws_iam_role" || role.Op != resolver.OpCreate {
		t.Fatalf("role = %+v, want type aws_iam_role, op create", role)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(role.Config, &config); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	var name string
	if err := json.Unmarshal(config["name"], &name); err != nil || name != "ci-runner-v13" {
		t.Fatalf("config[name] = %s (err=%v), want \"ci-runner-v13\"", config["name"], err)
	}
	var policyText string
	if err := json.Unmarshal(config["assume_role_policy"], &policyText); err != nil {
		t.Fatalf("config[assume_role_policy] is not a JSON string: %s (err=%v)", config["assume_role_policy"], err)
	}
	// The attribute's own WIRE type is string (real aws_iam_role schema,
	// matching provider/schemakeys_test.go's own awsIAMRoleLikeBlock) --
	// its content happens to be JSON text, which must itself parse
	// cleanly, byte-faithful to what the D2 block-string authored.
	var policy map[string]interface{}
	if err := json.Unmarshal([]byte(policyText), &policy); err != nil {
		t.Fatalf("assume_role_policy content is not valid JSON: %s (err=%v)", policyText, err)
	}
	if policy["Version"] != "2012-10-17" {
		t.Fatalf("policy = %+v, want Version 2012-10-17", policy)
	}

	l := core.Open(t.TempDir())
	p, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v (expected ZERO missing-required-attribute refusal, all Required attrs were supplied via ubx_required)", err)
	}
	if len(p.Delta.Creates) != 1 {
		t.Fatalf("creates = %+v, want 1", p.Delta.Creates)
	}
}

// TestParse_UbxRequired_OneStillMissing_UBI90RefusalFires is the partial
// path: ubx_required supplies "name" but not "assume_role_policy" --
// Parse itself succeeds (parse only enforces the escape hatch's own
// scope discipline, never full schema completeness), but resolve still
// refuses with UBI-90's own exact ErrMissingRequiredAttribute, naming the
// one attribute genuinely still absent. Proves ubx_required is additive
// to UBI-90's own check, never a way to bypass it for an attribute that
// really is missing.
func TestParse_UbxRequired_OneStillMissing_UBI90RefusalFires(t *testing.T) {
	src := `
classes: {
  aws_iam_role: {}
}
role: "ci-runner-v13" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})

	l := core.Open(t.TempDir())
	_, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, intent, nil)
	if !errors.Is(err, resolver.ErrMissingRequiredAttribute) {
		t.Fatalf("resolve err = %v, want ErrMissingRequiredAttribute", err)
	}
	if !strings.Contains(err.Error(), "assume_role_policy") {
		t.Fatalf("resolve err = %v, want it to name the one attribute still missing (assume_role_policy)", err)
	}
}

// TestParse_UbxRequired_NonRequiredAttribute_Refused is the scope-
// discipline case, the ticket's own strict boundary: ubx_required is NOT
// a general attribute-authoring escape hatch. "tags" isn't in
// awsIAMRoleFullProvider's own Required set (Optional/Computed/nonexistent
// are all the same "not the escape hatch's business" answer here) --
// Parse itself hard-refuses immediately, never silently accepting it or
// deferring the problem to resolve.
func TestParse_UbxRequired_NonRequiredAttribute_Refused(t *testing.T) {
	src := `
classes: {
  aws_iam_role: {}
}
role: "ci-runner-v13" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
  ubx_required.tags: "prod"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})
	if err == nil {
		t.Fatal("Parse: expected a scope-discipline refusal, got success")
	}
	want := `"tags" is not a required attribute on aws_iam_role -- use --from-doc or an SDK program to set optional attributes`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Parse err = %v, want it to contain %q", err, want)
	}
}

// TestParse_UbxBlueprint_NodeBecomesBlueprintCall is UBI-74 Slice 5's own
// required hermetic proof for the diagram calling convention: a node
// classed ubx_blueprint is NEVER classified as a resource (zero AI, pure
// structural parsing, matching ubx_required's own established
// mechanism) -- its own "blueprint" attribute plus every other literal
// attribute become one resolver.BlueprintCall, verbatim, no provider/
// schema lookup involved at all (unlike an ordinary resource node,
// InferProvider is never even called for this node).
func TestParse_UbxBlueprint_NodeBecomesBlueprintCall(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
  repo_name: "payments-ci-artifacts"
  queue_name: "payments-notifications"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none -- a ubx_blueprint node must never be classified as a resource", intent.Resources)
	}
	if len(intent.BlueprintCalls) != 1 {
		t.Fatalf("blueprint_calls = %+v, want exactly 1", intent.BlueprintCalls)
	}
	call := intent.BlueprintCalls[0]
	if call.Name != "ci-platform call" {
		t.Errorf("Name = %q, want %q", call.Name, "ci-platform call")
	}
	if call.Blueprint != "../ci-platform" {
		t.Errorf("Blueprint = %q, want %q", call.Blueprint, "../ci-platform")
	}
	if call.Args["repo_name"] != "payments-ci-artifacts" || call.Args["queue_name"] != "payments-notifications" {
		t.Fatalf("Args = %+v, want repo_name/queue_name set verbatim", call.Args)
	}
	// blueprint/blueprint_ref/blueprint_path are reserved -- never
	// duplicated into Args.
	if _, ok := call.Args["blueprint"]; ok {
		t.Error("Args should not contain the reserved \"blueprint\" key")
	}
}

// TestParse_UbxBlueprint_GitRefAndPath confirms blueprint_ref/
// blueprint_path -- the git-source-only optional attributes -- parse
// into BlueprintCall.Ref/Path, distinct from ordinary call params.
func TestParse_UbxBlueprint_GitRefAndPath(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "https://github.com/example/blueprints.git"
  blueprint_ref: "v3"
  blueprint_path: "ci-platform"
  repo_name: "payments-ci-artifacts"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	call := intent.BlueprintCalls[0]
	if call.Ref != "v3" {
		t.Errorf("Ref = %q, want %q", call.Ref, "v3")
	}
	if call.Path != "ci-platform" {
		t.Errorf("Path = %q, want %q", call.Path, "ci-platform")
	}
	if _, ok := call.Args["blueprint_ref"]; ok {
		t.Error("Args should not contain the reserved \"blueprint_ref\" key")
	}
	if _, ok := call.Args["blueprint_path"]; ok {
		t.Error("Args should not contain the reserved \"blueprint_path\" key")
	}
}

// TestParse_UbxBlueprint_MissingBlueprintAttr_Refused confirms a
// ubx_blueprint node with no "blueprint:" attribute is a hard, named
// parse error -- never silently dropped or deferred to expansion time.
func TestParse_UbxBlueprint_MissingBlueprintAttr_Refused(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  repo_name: "payments-ci-artifacts"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", nil, Options{})
	if err == nil {
		t.Fatal("Parse: expected an error for a missing blueprint: attribute, got nil")
	}
	if !strings.Contains(err.Error(), "blueprint") {
		t.Fatalf("Parse err = %v, want it to name the missing blueprint attribute", err)
	}
}

// TestParse_UbxBlueprint_RelativePathResolvedAgainstBaseDir confirms a
// relative local blueprint: value is resolved against opts.BaseDir --
// the SAME "relative to what" convention this package's own neighbor-
// ledger resolution already establishes -- while a git URL passes
// through completely unchanged.
func TestParse_UbxBlueprint_RelativePathResolvedAgainstBaseDir(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
}
`
	intent := mustParse(t, src, "payments", nil, Options{BaseDir: "/home/user/stacks/payments"})
	got := intent.BlueprintCalls[0].Blueprint
	want := filepath.Join("/home/user/stacks/payments", "../ci-platform")
	if got != want {
		t.Errorf("Blueprint = %q, want %q", got, want)
	}

	src2 := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "https://github.com/example/blueprints.git"
}
`
	intent2 := mustParse(t, src2, "payments", nil, Options{BaseDir: "/home/user/stacks/payments"})
	if got := intent2.BlueprintCalls[0].Blueprint; got != "https://github.com/example/blueprints.git" {
		t.Errorf("a git URL must pass through BaseDir resolution unchanged, got %q", got)
	}
}

// TestParse_UbxBlueprint_MixedWithOrdinaryResource confirms a diagram
// with BOTH an ordinary typed resource node AND a ubx_blueprint node
// parses both correctly, independently -- the new node kind doesn't
// disturb the existing classification/edge-translation passes at all.
func TestParse_UbxBlueprint_MixedWithOrdinaryResource(t *testing.T) {
	src := `
classes: {
  aws_vpc: {}
}
network: "main-vpc" {
  class: aws_vpc
}
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
  repo_name: "payments-ci-artifacts"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want exactly 1 (the ordinary aws_vpc node)", intent.Resources)
	}
	if len(intent.BlueprintCalls) != 1 {
		t.Fatalf("blueprint_calls = %+v, want exactly 1", intent.BlueprintCalls)
	}
}

// TestParse_UbxBlueprint_ListParamCommaSeparated is UBI-129's own
// required proof of the diagram-medium list-representation DECISION
// (docs/blueprint.md's own "List-typed parameters + iteration" section
// has the full reasoning): a comma-separated string in a single D2
// attribute value, exactly like every OTHER call param already is --
// never repeated child nodes, a second attribute shape this package
// would otherwise need new code to parse. blueprintCallFromNode's own
// existing "every non-reserved child is one Arg, verbatim raw text"
// capture (parse.go) needs ZERO changes to carry this -- proven here by
// asserting the comma-separated text survives completely unmodified,
// through the SAME code path TestParse_UbxBlueprint_NodeBecomesBlueprintCall
// already exercises for an ordinary scalar param.
func TestParse_UbxBlueprint_ListParamCommaSeparated(t *testing.T) {
	src := `
platform: "vpc subnets call" {
  class: ubx_blueprint
  blueprint: "../vpc-subnets"
  vpc_id: "vpc-123"
  availability_zones: "eu-central-1a, eu-central-1b, eu-central-1c"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if len(intent.BlueprintCalls) != 1 {
		t.Fatalf("blueprint_calls = %+v, want exactly 1", intent.BlueprintCalls)
	}
	call := intent.BlueprintCalls[0]
	want := "eu-central-1a, eu-central-1b, eu-central-1c"
	if call.Args["availability_zones"] != want {
		t.Fatalf("Args[\"availability_zones\"] = %q, want %q verbatim (comma-separated text, never split or reshaped here -- splitting is blueprint.ExpandCalls' own job, at call-expansion time)", call.Args["availability_zones"], want)
	}
}
