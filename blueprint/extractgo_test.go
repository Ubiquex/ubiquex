package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint/spec"
)

// The Go extractor reads declarations only: no build, no type checker,
// no execution. A published blueprint's schema has to be derivable from
// its files alone, before its dependencies are fetched.

func writeBlueprint(t *testing.T, src string) string {
	t.Helper()
	dir := writeBlueprintWithoutGoMod(t, src)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/ubx-blueprints/ubx-aws-sqs/go\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeBlueprintWithoutGoMod(t *testing.T, src string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ubx-aws-sqs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sqs.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const goodBlueprint = `package ubxawssqs

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

type Config struct {
	Name              string
	VisibilityTimeout *int
	CreateQueuePolicy *bool
	SourceQueueArns   *[]string
	PeerVpc           *sdk.CrossMarker
}

type Outputs struct {
	QueueUrl  *sdk.Computed
	QueueName *sdk.Computed
}

func UbxAwsSqs(cfg Config) Outputs { return Outputs{} }
`

// The convention in one sentence: a non-pointer field is required, a
// pointer field is optional.
func TestExtractGo_RequiredIsNonPointer(t *testing.T) {
	s, err := ExtractGo(writeBlueprint(t, goodBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := []SchemaParam{
		{Name: "name", SourceName: "Name", Type: spec.ParamString, Required: true},
		{Name: "visibility_timeout", SourceName: "VisibilityTimeout", Type: spec.ParamNumber, Required: false},
		{Name: "create_queue_policy", SourceName: "CreateQueuePolicy", Type: spec.ParamBool, Required: false},
		{Name: "source_queue_arns", SourceName: "SourceQueueArns", Type: spec.ParamListString, Required: false},
		{Name: "peer_vpc", SourceName: "PeerVpc", Type: spec.ParamCrossRef, Required: false},
	}
	if len(s.Params) != len(want) {
		t.Fatalf("got %d params, want %d: %+v", len(s.Params), len(want), s.Params)
	}
	for i, w := range want {
		if s.Params[i] != w {
			t.Errorf("param %d = %+v, want %+v", i, s.Params[i], w)
		}
	}
}

// Declaration order, not sorted: a derived per-language wrapper needs a
// stable order and struct field order is the only one all three
// languages agree on.
func TestExtractGo_ParamsAreInDeclarationOrder(t *testing.T) {
	s, err := ExtractGo(writeBlueprint(t, goodBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatal(err)
	}
	if s.Params[0].Name != "name" || s.Params[len(s.Params)-1].Name != "peer_vpc" {
		t.Fatalf("declaration order not preserved: %+v", s.Params)
	}
}

// A blueprint that returns nothing is legal and has no outputs. The
// alternative, requiring an Outputs struct, would force every blueprint
// to invent one.
func TestExtractGo_NoOutputsIsLegal(t *testing.T) {
	s, err := ExtractGo(writeBlueprint(t, `package bp

type Config struct{ Name string }

func Run(cfg Config) {}
`), "bp")
	if err != nil {
		t.Fatalf("a blueprint with no outputs must extract: %v", err)
	}
	if len(s.Outputs) != 0 {
		t.Fatalf("expected no outputs, got %+v", s.Outputs)
	}
}

// Every refusal names the fix. A schema that quietly describes
// something the function does not do would make every downstream
// consumer wrong together: argument binding, describe, provenance.
func TestExtractGo_RefusalsNameTheFix(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"unrecognised type",
			"package bp\ntype Config struct{ Name string; Tags map[string]string }\nfunc Run(cfg Config) {}\n",
			"not a param type",
		},
		{
			"unexported field",
			"package bp\ntype Config struct{ Name string; secret string }\nfunc Run(cfg Config) {}\n",
			"cannot set it",
		},
		{
			"two candidate entrypoints",
			"package bp\ntype Config struct{ Name string }\nfunc Run(cfg Config) {}\nfunc Also(cfg Config) {}\n",
			"exactly one is the blueprint",
		},
		{
			"no entrypoint",
			"package bp\ntype Config struct{ Name string }\n",
			"no exported function",
		},
		{
			"embedded field",
			"package bp\ntype Base struct{ A string }\ntype Config struct{ Base; Name string }\nfunc Run(cfg Config) {}\n",
			"embedded field",
		},
		{
			"multiple return values",
			"package bp\nimport sdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\ntype Config struct{ Name string }\nfunc Run(cfg Config) (*sdk.Computed, error) { return nil, nil }\n",
			"returns one Outputs struct, or nothing",
		},
		{
			"output that is not Computed",
			"package bp\ntype Config struct{ Name string }\ntype Outputs struct{ Url string }\nfunc Run(cfg Config) Outputs { return Outputs{} }\n",
			"every output is a *sdk.Computed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractGo(writeBlueprint(t, tc.src), "bp")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should contain %q, got: %v", tc.want, err)
			}
		})
	}
}

// The schema carries the source identifier, because the snake_case name
// cannot be converted back into it.
//
// A caller that constructs a config literal, which blueprint_calls and
// the HCL block both must do, needs the exact field name. PascalCasing
// the schema name does not recover it for any identifier containing an
// acronym: TargetARN becomes target_arn becomes TargetArn, which does
// not compile. AWS naming makes that the common case rather than an
// edge (ARN, ID, URL, KMS, HTTP), so a schema carrying only the
// snake_case name would break almost every real blueprint.
//
// This replaces an earlier test that pinned the lossy behaviour while
// the format gap was reported; the assertion is inverted rather than
// removed so the reason stays attached to the code.
func TestExtractGo_SchemaCarriesTheSourceIdentifier(t *testing.T) {
	dir := writeBlueprint(t, `package bp

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

type Config struct {
	TargetARN    string
	KmsKeyID     *string
	HTTPEndpoint *string
}

type Outputs struct{ QueueURL *sdk.Computed }

func Run(cfg Config) Outputs { return Outputs{} }
`)
	s, err := ExtractGo(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}

	wantParams := []struct{ name, source string }{
		{"target_arn", "TargetARN"},
		{"kms_key_id", "KmsKeyID"},
		{"http_endpoint", "HTTPEndpoint"},
	}
	if len(s.Params) != len(wantParams) {
		t.Fatalf("got %d params, want %d", len(s.Params), len(wantParams))
	}
	for i, w := range wantParams {
		if s.Params[i].Name != w.name || s.Params[i].SourceName != w.source {
			t.Errorf("param %d = %q/%q, want %q/%q", i, s.Params[i].Name, s.Params[i].SourceName, w.name, w.source)
		}
		// The property that matters: the conversion is one-way, so the
		// source name has to be carried rather than recomputed.
		if back, _ := pascalCase(w.name); back == w.source {
			t.Errorf("%s round-trips to %s, so this test no longer proves why SourceName exists", w.name, w.source)
		}
	}

	if len(s.Outputs) != 1 || s.Outputs[0].Name != "queue_url" || s.Outputs[0].SourceName != "QueueURL" {
		t.Fatalf("outputs = %+v, want queue_url/QueueURL", s.Outputs)
	}
}

// The Go import path is the module path, not the package name, and the
// two are separate fields because they are different kinds of value. A
// package name is not importable, so one shared "package" field would
// have been an import specifier in TypeScript and Python and a symbol
// namespace in Go.
func TestExtractGo_EntrypointCarriesModuleAndPackage(t *testing.T) {
	s, err := ExtractGo(writeBlueprint(t, goodBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.GoModule != "github.com/ubx-blueprints/ubx-aws-sqs/go" {
		t.Errorf("go_module = %q", s.Entrypoint.GoModule)
	}
	if s.Entrypoint.GoPackage != "ubxawssqs" {
		t.Errorf("go_package = %q", s.Entrypoint.GoPackage)
	}
	if s.Entrypoint.TSEntry != "" || s.Entrypoint.PyModule != "" {
		t.Errorf("only the language's own specifier is set, got ts=%q py=%q", s.Entrypoint.TSEntry, s.Entrypoint.PyModule)
	}
}

// Without a module path a Go blueprint cannot be imported by anything,
// so a schema claiming to describe how to call it would describe
// something uncallable.
func TestExtractGo_MissingGoModIsRefused(t *testing.T) {
	_, err := ExtractGo(writeBlueprintWithoutGoMod(t, goodBlueprint), "bp")
	if err == nil {
		t.Fatal("expected a refusal when go.mod is absent")
	}
	if !strings.Contains(err.Error(), "how a caller imports it") {
		t.Fatalf("the error should say why go.mod is needed, got: %v", err)
	}
}
