package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// schemamodel_test.go covers step 5 of blueprints-as-code: the
// consumers that used to read an Ubxfile now read a derived schema
// (docs/blueprint.md, "Blueprint schema").
//
// The end-to-end test at the bottom is the one that matters. Everything
// else here can pass while a blueprint that is code is still
// uncallable, because the whole point of the schema is that something
// downstream uses it.

// sdkGoRootForSchemaTests resolves this repo's own sdk/go module root,
// so a fixture blueprint compiles against the local runtime rather than
// a published version that may not carry what it needs.
func sdkGoRootForSchemaTests(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "sdk", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected %s to be sdk/go's own module root: %v", root, err)
	}
	return root
}

// writeCodeBlueprint writes a real, compilable Go blueprint under the
// code model: one package, no Ubxfile, no generated bindings, a config
// struct, and a go.mod naming the module a caller imports.
func writeCodeBlueprint(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module github.com/ubx-blueprints/" + name + "\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRootForSchemaTests(t) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package widgets

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

// Config is the blueprint's parameters. TargetARN is deliberately
// acronym-cased: it is the case the schema's source_name exists for.
type Config struct {
	Name      string
	TargetARN *string
	Retention *int
}

type Outputs struct {
	WidgetID *sdk.Computed
}

func BuildWidget(cfg Config) Outputs {
	sdk.PushBlueprintSource("` + name + `")
	defer sdk.PopBlueprintSource()
	w := sdk.Resource(
		sdk.ResourceBinding{WireType: "fake_widget", Fields: sdk.FieldMap{
			"Name":      {WireName: "name"},
			"TargetARN": {WireName: "target_arn"},
			"Retention": {WireName: "retention"},
		}},
		"primary",
		config{Name: cfg.Name, TargetARN: cfg.TargetARN, Retention: cfg.Retention},
	)
	return Outputs{WidgetID: w.Field("id")}
}

// config is the wire-shaped struct the runtime serializes, with the
// nil-pointer omission that makes an unset optional genuinely absent.
type config struct {
	Name      string  ` + "`json:\"name\"`" + `
	TargetARN *string ` + "`json:\"target_arn,omitempty\"`" + `
	Retention *int    ` + "`json:\"retention,omitempty\"`" + `
}
`
	if err := os.WriteFile(filepath.Join(dir, "widgets.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Package is where the schema is derived, so packaging a code blueprint
// has to produce one, and the content hash has to cover it: a schema
// outside the hash could be swapped after the fact.
func TestPackage_DerivesTheSchemaAndHashesIt(t *testing.T) {
	dir := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	out := filepath.Join(t.TempDir(), "bp.tar.gz")

	manifest, err := Package(context.Background(), dir, out)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, SchemaFileName)); err != nil {
		t.Fatalf("package did not write %s: %v", SchemaFileName, err)
	}
	if _, covered := manifest.Files[SchemaFileName]; !covered {
		t.Errorf("%s is not in the manifest -- the content hash has to cover it", SchemaFileName)
	}

	s, ok, err := ReadSchema(dir)
	if err != nil || !ok {
		t.Fatalf("read back the written schema: ok=%v err=%v", ok, err)
	}
	if s.Entrypoint.Function != "BuildWidget" || s.Entrypoint.ConfigType != "Config" {
		t.Errorf("entrypoint = %+v", s.Entrypoint)
	}
	if s.Entrypoint.GoModule != "github.com/ubx-blueprints/ubx-fake-widget" {
		t.Errorf("go_module = %q", s.Entrypoint.GoModule)
	}
}

// Re-deriving rather than reusing is what makes the schema trustworthy:
// an edited function has to produce an edited schema, or the derived
// document is no better than a hand-written one.
func TestPackage_RederivesAStaleSchema(t *testing.T) {
	dir := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	out := filepath.Join(t.TempDir(), "bp.tar.gz")
	if _, err := Package(context.Background(), dir, out); err != nil {
		t.Fatal(err)
	}

	stale := &Schema{
		SchemaVersion: SchemaVersion,
		Name:          "ubx-fake-widget",
		Entrypoint:    Entrypoint{Language: "go", GoModule: "wrong", GoPackage: "wrong", Function: "GoneAway", ConfigType: "Nope"},
		Params:        []SchemaParam{{Name: "invented", SourceName: "Invented", Type: ParamString, Required: true}},
		Outputs:       []SchemaOutput{},
		Defaults:      DefaultsNotDerivable,
	}
	if err := WriteSchema(dir, stale); err != nil {
		t.Fatal(err)
	}

	if _, err := Package(context.Background(), dir, out); err != nil {
		t.Fatal(err)
	}
	s, _, err := ReadSchema(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.Function != "BuildWidget" {
		t.Errorf("function = %q, want the real one -- a stale schema was reused rather than re-derived", s.Entrypoint.Function)
	}
	for _, p := range s.Params {
		if p.Name == "invented" {
			t.Error("an invented param survived re-derivation")
		}
	}
}

// WriteSchema output is inside the hashed directory, so two identical
// derivations have to be byte-identical.
func TestWriteSchema_IsDeterministic(t *testing.T) {
	dirA := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	dirB := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	for _, d := range []string{dirA, dirB} {
		s, err := ExtractGo(d, "ubx-fake-widget")
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteSchema(d, s); err != nil {
			t.Fatal(err)
		}
	}
	a, err := os.ReadFile(filepath.Join(dirA, SchemaFileName))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dirB, SchemaFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("two derivations of identical source differ:\n%s\n%s", a, b)
	}
}

func TestDescribe_PrefersTheSchemaOverAnUbxfile(t *testing.T) {
	dir := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	s, err := ExtractGo(dir, "ubx-fake-widget")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSchema(dir, s); err != nil {
		t.Fatal(err)
	}
	// An Ubxfile that disagrees with the code. The derived one wins,
	// because it is the one that cannot be stale.
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte("lang: py\n\nparams:\n  from_the_ubxfile: string, required\n\nresources: |\n  placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != "schema" {
		t.Fatalf("described by %q, want the schema", d.Source)
	}
	if d.Lang != "go" {
		t.Errorf("lang = %q, want go -- the Ubxfile's own py was preferred", d.Lang)
	}
	for _, p := range d.Params {
		if p.Name == "from_the_ubxfile" {
			t.Error("an Ubxfile param leaked into a schema-described blueprint")
		}
	}
	if d.DefaultsKnown {
		t.Error("a schema carries no default values, so defaults are not known")
	}
}

func TestDescribe_FallsBackToTheUbxfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte("lang: go\n\nparams:\n  name: string, required\n  tag: string, default \"prod\"\n\nresources: |\n  placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Describe(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != UbxfileName || !d.DefaultsKnown {
		t.Fatalf("source=%q defaultsKnown=%v", d.Source, d.DefaultsKnown)
	}
	if len(d.Params) != 2 || d.Params[1].Default != "prod" {
		t.Errorf("params = %+v -- an Ubxfile's declared default is still reported", d.Params)
	}
}

func TestIsBlueprintDir_AcceptsEitherMarker(t *testing.T) {
	schemaDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(schemaDir, SchemaFileName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ubxDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ubxDir, UbxfileName), []byte("lang: go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsBlueprintDir(schemaDir) || !IsBlueprintDir(ubxDir) {
		t.Error("both markers identify a blueprint root")
	}
	if IsBlueprintDir(t.TempDir()) {
		t.Error("an empty directory is not a blueprint")
	}
}

// A malformed schema is a broken blueprint, not a missing one. Reporting
// it as missing sends whoever hits it looking for the wrong problem.
func TestIsBlueprintDir_AMalformedSchemaIsStillABlueprint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SchemaFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsBlueprintDir(dir) {
		t.Fatal("a directory with an unreadable schema is still a blueprint root")
	}
	if _, err := Describe(dir); err == nil {
		t.Error("want a parse error, not a silent success")
	}
}

func TestReadSchema_RefusesAFutureVersion(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"schema_version": SchemaVersion + 1, "name": "x"})
	if err := os.WriteFile(filepath.Join(dir, SchemaFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadSchema(dir)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("want a version refusal naming the field, got %v", err)
	}
}

func TestDetectLanguage_RefusesMixedSource(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.go", "b.py"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := DetectLanguage(dir)
	if err == nil || !strings.Contains(err.Error(), "one language") {
		t.Errorf("want a refusal naming the rule, got %v", err)
	}
}

// UBI-261: a schema-described blueprint's outputs resolve from what the
// evaluation REPORTED, since nothing outside it can derive the address.
func TestResolveCallOutputs_SchemaOutputsComeFromTheEvaluation(t *testing.T) {
	desc := &Description{
		Outputs: []Output{{Name: "widget_id"}},
		Schema:  &Schema{SchemaVersion: SchemaVersion},
		Source:  "schema",
	}
	reported := map[string]string{"widget_id": "payments.fake_widget.primary.id"}
	addr, err := resolveCallOutputs("payments", desc, []resolver.ResourceIntent{{Type: "fake_widget", Name: "primary"}}, reported)
	if err != nil {
		t.Fatal(err)
	}
	if addr["widget_id"] != "payments.fake_widget.primary.id" {
		t.Errorf("widget_id = %q", addr["widget_id"])
	}

	intent := &resolver.IntentFile{Resources: []resolver.ResourceIntent{{
		Type: "fake_widget", Name: "consumer",
		Config: json.RawMessage(`{"upstream":{"$ref":{"to":"` + blueprintOutputRefPrefix + `widget_id"}}}`),
	}}}
	if err := rewriteBlueprintOutputRefs(intent, addr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(intent.Resources[0].Config), "payments.fake_widget.primary.id") {
		t.Errorf("the reference was not rewritten: %s", intent.Resources[0].Config)
	}
}

// A declared output the blueprint never set cannot be referenced, and
// the refusal says so rather than reporting the output as undeclared.
func TestResolveCallOutputs_SchemaOutputNeverSetIsRefused(t *testing.T) {
	desc := &Description{
		Outputs: []Output{{Name: "widget_id"}},
		Schema:  &Schema{SchemaVersion: SchemaVersion},
		Source:  "schema",
	}
	_, err := resolveCallOutputs("payments", desc, nil, map[string]string{})
	if err == nil {
		t.Fatal("want a refusal for a declared output the blueprint never set")
	}
	if !strings.Contains(err.Error(), "returned no value") {
		t.Errorf("the refusal has to say what happened, got: %v", err)
	}
}

// TestExpandCalls_CodeBlueprint_RealGoEvaluation is the one that
// matters: a blueprint written as ordinary Go, packaged so its schema
// is derived, then CALLED through the real ExpandCalls path, compiled
// and evaluated by the real goeval sandbox.
//
// Every earlier test in this file could pass while a code blueprint
// remained uncallable, because they only check that the schema is
// written and read. This checks that the schema is enough to call
// something with, which is the only reason it exists.
//
// It also pins the two things the Ubxfile model could not do. The
// function is BuildWidget and the package is widgets, neither derivable
// from the blueprint's own name, where the old caller writers derived
// both from it and an author had no say. And TargetARN is passed by its
// real identifier, which no case conversion of "target_arn" produces.
func TestExpandCalls_CodeBlueprint_RealGoEvaluation(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and evaluates a real Go program")
	}
	dir := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatal(err)
	}

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          "intent",
		Stack:         "payments",
		Intent:        core.Intent{Summary: "call a blueprint that is code"},
		BlueprintCalls: []resolver.BlueprintCall{{
			Name:      "widget",
			Blueprint: dir,
			Args: map[string]string{
				"name":       "primary-widget",
				"target_arn": "arn:aws:sns:eu-west-1:123456789012:topic",
			},
		}},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("expand a code blueprint's own call: %v", err)
	}

	if len(intent.Resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(intent.Resources), intent.Resources)
	}
	ri := intent.Resources[0]
	if ri.Type != "fake_widget" || ri.Name != "primary" {
		t.Errorf("resource = %s.%s", ri.Type, ri.Name)
	}

	var cfg map[string]any
	if err := json.Unmarshal(ri.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["name"] != "primary-widget" {
		t.Errorf("name = %v", cfg["name"])
	}
	// Passed as TargetARN in the generated config literal. Reaching the
	// provider at all proves source_name was used rather than a
	// PascalCased wire name, which would not have compiled.
	if cfg["target_arn"] != "arn:aws:sns:eu-west-1:123456789012:topic" {
		t.Errorf("target_arn = %v", cfg["target_arn"])
	}
	// retention was never given, and an omitted optional has to be
	// genuinely absent rather than present-and-null: a provider that
	// rejects an attribute outright fails the whole apply on a null it
	// was never sent before (the real fifo_queue finding).
	if v, present := cfg["retention"]; present && v != nil {
		t.Errorf("retention = %v, want absent", v)
	}
	if _, isNull := cfg["retention"]; isNull && cfg["retention"] == nil {
		t.Error("an un-given optional reached the provider as an explicit null")
	}

	var blueprintRef string
	for _, src := range ri.Sources {
		if src.Kind == "blueprint" {
			blueprintRef = src.Ref
		}
	}
	if blueprintRef == "" {
		t.Fatal("no blueprint provenance -- a code blueprint's resources have to be traceable to it like any other")
	}
	if !strings.HasPrefix(blueprintRef, "ubx-fake-widget:sha256:") {
		t.Errorf("provenance ref = %q, want <name>:<content hash>", blueprintRef)
	}
}

// TestExpandCalls_CodeBlueprintOutputsAreReferenceable is UBI-261's own
// end-to-end proof: a resource the CALLER writes consumes an output a
// code blueprint returned, resolved to a real address.
//
// This is the thing the schema model could not do. The address is
// produced inside the blueprint's own evaluation, reported by the
// synthesized caller through sdk.BlueprintOutputs, and substituted into
// the referencing config here.
func TestExpandCalls_CodeBlueprintOutputsAreReferenceable(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and evaluates a real Go program")
	}
	dir := writeCodeBlueprint(t, t.TempDir(), "ubx-fake-widget")
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "bp.tar.gz")); err != nil {
		t.Fatal(err)
	}

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          "intent",
		Stack:         "payments",
		Intent:        core.Intent{Summary: "consume a blueprint's own output"},
		Resources: []resolver.ResourceIntent{{
			Type: "fake_widget", Name: "consumer", Op: "create",
			Config: json.RawMessage(`{"name":"consumer","upstream":{"$ref":{"to":"` + blueprintOutputRefPrefix + `widget:widget_id"}}}`),
		}},
		BlueprintCalls: []resolver.BlueprintCall{{
			Name:      "widget",
			CallName:  "widget",
			Blueprint: dir,
			Args:      map[string]string{"name": "primary-widget"},
		}},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("expand: %v", err)
	}

	var consumer *resolver.ResourceIntent
	for i := range intent.Resources {
		if intent.Resources[i].Name == "consumer" {
			consumer = &intent.Resources[i]
		}
	}
	if consumer == nil {
		t.Fatal("the caller's own resource vanished")
	}
	var cfg map[string]any
	if err := json.Unmarshal(consumer.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	ref, ok := cfg["upstream"].(map[string]any)
	if !ok {
		t.Fatalf("upstream = %v, want a $ref object", cfg["upstream"])
	}
	inner, _ := ref["$ref"].(map[string]any)
	to, _ := inner["to"].(string)
	if to != "payments.fake_widget.primary.id" {
		t.Errorf("resolved to %q, want the blueprint's own widget address", to)
	}
}
