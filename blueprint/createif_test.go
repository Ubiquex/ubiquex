package blueprint

import (
	"strings"
	"testing"
)

// create_if: a resource that may not exist at all.
//
// Terraform's `count = var.create ? 1 : 0` is the house style across
// terraform-aws-modules and blocked conversion of essentially that whole
// ecosystem: converting terraform-aws-sqs produced zero of eight
// resources, every one guarded by that shape.
//
// It is not a new capability. A blueprint's resource set already varies
// with a caller's argument, including down to zero, via for_each over an
// empty list (UBI-129). create_if is the same idea reached from a bool
// rather than a list, and is its own field rather than a desugaring into
// for_each so the generated signature takes a bool where the module took
// a bool, instead of making callers write ["x"] to mean true.

const createIfUbxfile = `lang: go

params:
  create: bool, default true
  queue_name: string, required

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"bp","intent":{"summary":"s"},
   "resources":[{"type":"aws_sqs_queue","name":"this","op":"create","create_if":["create"],
                 "config":{"queue_name":"{queue_name}"}}]}
`

func TestCreateIf_GeneratesAConditional(t *testing.T) {
	dir := writeUbxfile(t, createIfUbxfile)
	uf, draft, err := Validate(dir)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	files, err := GenerateGo("bp", uf, draft)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	var src string
	for name, content := range files {
		if strings.HasSuffix(name, "bp.go") {
			src = content
		}
	}
	if src == "" {
		t.Fatal("no bp.go generated")
	}
	if !strings.Contains(src, "if cfg.create {") {
		t.Fatalf("expected a conditional around the resource, got:\n%s", src)
	}
	// The resource must be inside the conditional, not merely near it.
	idx := strings.Index(src, "if cfg.create {")
	call := strings.Index(src, "sdk.Resource(This,")
	if call < idx {
		t.Fatalf("the resource call is not inside the conditional:\n%s", src)
	}
}

func TestCreateIf_RejectsUndeclaredParam(t *testing.T) {
	dir := writeUbxfile(t, strings.Replace(createIfUbxfile, `"create_if":["create"]`, `"create_if":["nope"]`, 1))
	_, _, err := Validate(dir)
	if err == nil || !strings.Contains(err.Error(), "names no declared param") {
		t.Fatalf("expected an undeclared-param refusal, got: %v", err)
	}
}

func TestCreateIf_RejectsNonBoolParam(t *testing.T) {
	dir := writeUbxfile(t, strings.Replace(createIfUbxfile, `"create_if":["create"]`, `"create_if":["queue_name"]`, 1))
	_, _, err := Validate(dir)
	if err == nil || !strings.Contains(err.Error(), "must name a bool param") {
		t.Fatalf("expected a non-bool refusal, got: %v", err)
	}
}

// The boundary that keeps this honest: a resource that may not exist
// cannot be referenced, because a reference to an absent resource has no
// representation. The three SDK runtimes each mishandled one differently
// (a nil-pointer panic in Go, a silent null in TypeScript, a silent
// omission in Python), which is exactly why this is refused at build time
// rather than emitted and left to fail per-language at runtime.
func TestCreateIf_RejectsAReferencedConditionalResource(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

params:
  create: bool, default true

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"bp","intent":{"summary":"s"},
   "resources":[{"type":"aws_sqs_queue","name":"this","op":"create","create_if":["create"],"config":{}},
                {"type":"aws_sqs_queue_policy","name":"p","op":"create",
                 "config":{"queue_url":{"$ref":{"to":"bp.aws_sqs_queue.this.queue_url"}}}}]}
`)
	_, _, err := Validate(dir)
	if err == nil {
		t.Fatal("expected a referenced conditional resource to be refused")
	}
	for _, want := range []string{"is conditional", "referenced by another resource", "has no representation yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
}

// A conditional iteration is expressible but has no Terraform shape
// asking for it yet, and would double the codegen surface in every
// language. Refused rather than half-supported.
func TestCreateIf_RejectsCombinationWithForEach(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

params:
  create: bool, default true
  names: list(string), required

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"bp","intent":{"summary":"s"},
   "resources":[{"type":"aws_sqs_queue","name":"q-{names}","op":"create",
                 "for_each":"names","create_if":["create"],"config":{}}]}
`)
	_, _, err := Validate(dir)
	if err == nil || !strings.Contains(err.Error(), "create_if and for_each cannot both be set") {
		t.Fatalf("expected the combination to be refused, got: %v", err)
	}
}

// An unconditional blueprint must generate byte-identically to before
// this field existed: create_if empty is every resource ever produced
// until now.
func TestCreateIf_AbsentChangesNothing(t *testing.T) {
	dir := writeUbxfile(t, strings.Replace(createIfUbxfile, `"create_if":["create"],`, "", 1))
	uf, draft, err := Validate(dir)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	files, err := GenerateGo("bp", uf, draft)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	for _, content := range files {
		if strings.Contains(content, "if cfg.create {") {
			t.Fatalf("an unconditional resource generated a conditional:\n%s", content)
		}
	}
}

// Two types sharing a name is legal in Terraform and now legal here.
//
// A Terraform label is unique per TYPE, so aws_sqs_queue.this and
// aws_sqs_queue_policy.this are both legal and distinct. An identifier
// derived from the name alone collapsed them and codegen refused the
// pair. "this" is the near-universal Terraform label, and
// terraform-aws-sqs has five resources named it, so any real module hit
// this the moment more than one of its resources converted.
func TestIdentifierCollision_TwoTypesOneName_Qualifies(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"bp","intent":{"summary":"s"},
   "resources":[{"type":"aws_sqs_queue","name":"this","op":"create","config":{}},
                {"type":"aws_sqs_queue_policy","name":"this","op":"create","config":{}}]}
`)
	uf, draft, err := Validate(dir)
	if err != nil {
		t.Fatalf("two types sharing a name must build: %v", err)
	}
	files, err := GenerateGo("bp", uf, draft)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	var bindings string
	for name, content := range files {
		if strings.HasSuffix(name, "bindings.go") {
			bindings = content
		}
	}
	for _, want := range []string{"AwsSqsQueueThis", "AwsSqsQueuePolicyThis"} {
		if !strings.Contains(bindings, want) {
			t.Errorf("expected a type-qualified identifier %q:\n%s", want, bindings)
		}
	}
}

// The other side of the same rule, and the one that keeps this from being
// a silent rename: a blueprint WITHOUT a collision must keep the exact
// identifiers it had before, because those are an exported, supported
// surface (UBI-225: importing a blueprint's bindings directly is a real
// case, pinned by cli/blueprint_binding_provenance_test.go). Qualifying
// only on collision is what makes this change unable to rename anything
// that compiles today.
func TestIdentifierCollision_NoCollision_NamesUnchanged(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"bp","intent":{"summary":"s"},
   "resources":[{"type":"aws_sqs_queue","name":"primary","op":"create","config":{}},
                {"type":"aws_sqs_queue_policy","name":"policy","op":"create","config":{}}]}
`)
	uf, draft, err := Validate(dir)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	files, err := GenerateGo("bp", uf, draft)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	var bindings string
	for name, content := range files {
		if strings.HasSuffix(name, "bindings.go") {
			bindings = content
		}
	}
	for _, want := range []string{"var Primary =", "var Policy ="} {
		if !strings.Contains(bindings, want) {
			t.Errorf("a non-colliding blueprint's identifiers changed, want %q:\n%s", want, bindings)
		}
	}
	if strings.Contains(bindings, "AwsSqsQueuePrimary") {
		t.Errorf("a non-colliding resource was type-qualified anyway:\n%s", bindings)
	}
}
