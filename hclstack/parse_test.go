package hclstack

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestParseBytes_ValidFile(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source  = "acme/postgres"
  version = "1.2.0"
  path    = "modules/postgres"

  size    = "db.r6g.large"
  storage = 100
  ha      = true
  zones   = ["a", "b", "c"]
  vpc_id  = "@network.aws_vpc.main.id"
}
`
	intent, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if intent.Kind != resolver.IntentFileKind {
		t.Fatalf("Kind = %q, want %q", intent.Kind, resolver.IntentFileKind)
	}
	if intent.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", intent.SchemaVersion)
	}
	if intent.Stack != "payments" {
		t.Fatalf("Stack = %q, want payments", intent.Stack)
	}
	if len(intent.Resources) != 0 {
		t.Fatalf("expected zero Resources (blueprint calls only), got %d", len(intent.Resources))
	}
	if len(intent.BlueprintCalls) != 1 {
		t.Fatalf("expected 1 BlueprintCall, got %d", len(intent.BlueprintCalls))
	}
	call := intent.BlueprintCalls[0]
	if call.Blueprint != "acme/postgres" {
		t.Fatalf("Blueprint = %q, want acme/postgres", call.Blueprint)
	}
	if call.Ref != "1.2.0" {
		t.Fatalf("Ref = %q, want 1.2.0", call.Ref)
	}
	if call.Path != "modules/postgres" {
		t.Fatalf("Path = %q, want modules/postgres", call.Path)
	}
	if call.Name != "postgres.primary" || call.CallName != "postgres.primary" {
		t.Fatalf("Name/CallName = %q/%q, want postgres.primary", call.Name, call.CallName)
	}
	want := map[string]string{
		"size":    "db.r6g.large",
		"storage": "100",
		"ha":      "true",
		"zones":   "a, b, c",
		"vpc_id":  "@network.aws_vpc.main.id",
	}
	for k, v := range want {
		if got := call.Args[k]; got != v {
			t.Fatalf("Args[%q] = %q, want %q", k, got, v)
		}
	}
	if len(call.Args) != len(want) {
		t.Fatalf("Args = %+v, want exactly %+v", call.Args, want)
	}
}

func TestParseBytes_MultipleCallsAndFloat(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  size   = 3.5
}

blueprint "service" "api" {
  source   = "../service"
  replicas = 3
}
`
	intent, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(intent.BlueprintCalls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(intent.BlueprintCalls))
	}
	if intent.BlueprintCalls[0].Args["size"] != "3.5" {
		t.Fatalf("size = %q, want 3.5", intent.BlueprintCalls[0].Args["size"])
	}
	if intent.BlueprintCalls[1].Args["replicas"] != "3" {
		t.Fatalf("replicas = %q, want 3", intent.BlueprintCalls[1].Args["replicas"])
	}
}

func TestParseBytes_MissingStack(t *testing.T) {
	src := `
blueprint "postgres" "primary" {
  source = "../postgres"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "\"stack\" attribute is required") {
		t.Fatalf("expected a missing-stack error, got: %v", err)
	}
}

func TestParseBytes_UnexpectedTopLevelAttribute(t *testing.T) {
	src := `
stack = "payments"
region = "us-east-1"
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "unexpected top-level attribute \"region\"") {
		t.Fatalf("expected an unexpected-attribute error, got: %v", err)
	}
}

func TestParseBytes_UnexpectedTopLevelBlock(t *testing.T) {
	src := `
stack = "payments"

locals {
  x = 1
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "unexpected top-level block \"locals\"") {
		t.Fatalf("expected an unexpected-block error, got: %v", err)
	}
}

func TestParseBytes_MissingSource(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  size = "db.r6g.large"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "\"source\" is required") {
		t.Fatalf("expected a missing-source error, got: %v", err)
	}
}

func TestParseBytes_DuplicateBlock(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
}

blueprint "postgres" "primary" {
  source = "../postgres2"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "duplicate blueprint block \"postgres\" \"primary\"") {
		t.Fatalf("expected a duplicate-block error, got: %v", err)
	}
}

func TestParseBytes_NestedBlockRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  nested {
    x = 1
  }
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "nested \"nested\" blocks are not allowed") {
		t.Fatalf("expected a nested-block error, got: %v", err)
	}
}

func TestParseBytes_ObjectLiteralRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  tags   = { env = "prod" }
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "object values are not supported") {
		t.Fatalf("expected an object-literal refusal, got: %v", err)
	}
}

func TestParseBytes_ArithmeticRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source          = "../postgres"
  retention_days  = 14 * 86400
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "no expressions, functions, or interpolation") {
		t.Fatalf("expected an arithmetic refusal, got: %v", err)
	}
}

func TestParseBytes_FunctionCallRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  name   = upper("primary")
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "no expressions, functions, or interpolation") {
		t.Fatalf("expected a function-call refusal, got: %v", err)
	}
}

func TestParseBytes_InterpolationRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  name   = "prefix-${stack}"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "string interpolation (${...}) is not allowed") {
		t.Fatalf("expected an interpolation refusal, got: %v", err)
	}
}

// TestParseBytes_SiblingOutputReferenceRefused is the central finding
// this package's own doc comment records: the ticket's own worked
// example does not compile. This proves the exact syntax it used is
// recognized as a real reference, then refused with a named error
// pointing at the SDK, never silently treated as a literal string.
func TestParseBytes_SiblingOutputReferenceRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
}

blueprint "service" "api" {
  source       = "../service"
  database_url = blueprint.postgres.primary.connection_string
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil {
		t.Fatal("expected a refusal, got success")
	}
	msg := err.Error()
	for _, want := range []string{
		"blueprint.postgres.primary.connection_string",
		"cannot consume another call's output",
		"author this stack via the SDK",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing expected substring %q", msg, want)
		}
	}
}

func TestParseBytes_UnrelatedTraversalRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  size   = some.other.thing
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "is not a valid reference here") {
		t.Fatalf("expected an unrelated-traversal refusal, got: %v", err)
	}
}

func TestParseBytes_SourceMustBeString(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = 5
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "\"source\" must be a string") {
		t.Fatalf("expected a source-type refusal, got: %v", err)
	}
}

func TestParseBytes_MalformedLabels(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" {
  source = "../postgres"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "requires exactly two labels") {
		t.Fatalf("expected a malformed-labels error, got: %v", err)
	}
}

func TestParseBytes_ArithmeticInsideListRefused(t *testing.T) {
	src := `
stack = "payments"

blueprint "postgres" "primary" {
  source = "../postgres"
  zones  = ["a", 1 + 1]
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "list element") {
		t.Fatalf("expected a list-element refusal, got: %v", err)
	}
}

func TestParseBytes_EmptyStackRefused(t *testing.T) {
	src := `
stack = ""

blueprint "postgres" "primary" {
  source = "../postgres"
}
`
	_, err := ParseBytes([]byte(src), "test.ubx.hcl")
	if err == nil || !strings.Contains(err.Error(), "must be a non-empty string") {
		t.Fatalf("expected an empty-stack error, got: %v", err)
	}
}
