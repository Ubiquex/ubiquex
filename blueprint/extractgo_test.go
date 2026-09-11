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
		{Name: "name", Type: spec.ParamString, Required: true},
		{Name: "visibility_timeout", Type: spec.ParamNumber, Required: false},
		{Name: "create_queue_policy", Type: spec.ParamBool, Required: false},
		{Name: "source_queue_arns", Type: spec.ParamListString, Required: false},
		{Name: "peer_vpc", Type: spec.ParamCrossRef, Required: false},
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

// KNOWN GAP, reported rather than silently patched: the schema records
// only the snake_case param name, and that does not round-trip back to
// the Go field name for any identifier containing an acronym.
//
// A caller that constructs a Config literal (which blueprint_calls and
// the HCL block must do) needs the exact field name. TargetARN becomes
// target_arn becomes TargetArn, which does not compile.
//
// This test pins the current, lossy behaviour so the format fix has a
// target and so nobody mistakes the gap for an extraction bug. It should
// be rewritten, not deleted, when the schema carries the source
// identifier.
func TestExtractGo_SchemaNameDoesNotRoundTripToTheGoFieldName(t *testing.T) {
	dir := writeBlueprint(t, `package bp

type Config struct {
	TargetARN    string
	HTTPEndpoint *string
}

func Run(cfg Config) {}
`)
	s, err := ExtractGo(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ schemaName, goField string }{
		{"target_arn", "TargetARN"},
		{"http_endpoint", "HTTPEndpoint"},
	} {
		back, err := pascalCase(tc.schemaName)
		if err != nil {
			t.Fatal(err)
		}
		if back == tc.goField {
			t.Fatalf("%s now round-trips to %s -- the gap is closed, so this test should be replaced by one asserting the schema carries the source identifier", tc.schemaName, tc.goField)
		}
	}
	if s.Params[0].Name != "target_arn" {
		t.Fatalf("expected the snake_case name in the schema, got %q", s.Params[0].Name)
	}
}
