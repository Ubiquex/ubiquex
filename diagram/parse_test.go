package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// fakeSchema is a hermetic resolver.SchemaInspector -- mirrors
// core/resolver's own test fake (package-private there, so this
// package needs its own).
type fakeSchema struct {
	types map[string]bool
}

func (f *fakeSchema) HasType(t string) bool           { return f.types[t] }
func (f *fakeSchema) IsComputed(t, path string) bool  { return false }
func (f *fakeSchema) IsSensitive(t, path string) bool { return false }

func awsProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "hashicorp/aws",
		Version: "6.54.0",
		Schema:  &fakeSchema{types: map[string]bool{"aws_db_instance": true, "aws_vpc": true, "aws_ecs_service": true}},
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
