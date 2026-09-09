package blueprint

import (
	"strings"
	"testing"
)

// A converted blueprint cannot be rebuilt, and has to say so itself.
//
// `ubx blueprint convert` writes a prose summary into resources: rather
// than a pre-resolved intent/v1 document, by design: the generated
// packages are a converted blueprint's source of truth, and the Terraform
// module it came from is what you edit. So `ubx blueprint build` against
// one can only ever fail.
//
// It used to fail by reporting where the JSON parser stopped:
//
//	resources: is not a valid pre-resolved intent/v1 document (inline):
//	invalid character 'C' looking for beginning of value
//
// The 'C' is the first letter of "Converted". Accurate, and it tells the
// reader nothing about what to do. ResourcesSource could not distinguish
// the two cases either: it reads "inline" for an authored blueprint with
// inline JSON and for a converted one alike, and is recomputed at parse
// time rather than read from the file, so it carries nothing across a
// write. converted_from is the marker that actually does.

// An authored blueprint whose JSON is genuinely broken must keep getting
// the JSON error, and must never be told it was converted.
func TestValidate_AuthoredMalformedJSON_KeepsTheJSONError(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

resources: |
  {"schema_version": 1, "stack": "x", "resources": [
`)
	_, _, err := Validate(dir)
	if err == nil {
		t.Fatal("expected malformed JSON to be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not a valid pre-resolved intent/v1 document") {
		t.Errorf("expected the JSON-specific error:\n%s", msg)
	}
	if strings.Contains(msg, "blueprint convert") {
		t.Errorf("an authored blueprint was told it might have been converted:\n%s", msg)
	}
}

// Prose where a document belongs says so, rather than reporting where
// the parser stopped.
func TestValidate_ProseResources_NamesTheShapeProblem(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

resources: |
  One queue and a policy for it.
`)
	_, _, err := Validate(dir)
	if err == nil {
		t.Fatal("expected prose resources: to be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does not even begin as a JSON object") {
		t.Errorf("expected the shape problem named:\n%s", msg)
	}
	if !strings.Contains(msg, "ubx resolve --out") {
		t.Errorf("expected the message to say what resources: must be:\n%s", msg)
	}
	// The Terraform converter was removed from ubx (UBI-125), so nothing
	// here may point a reader at it.
	if strings.Contains(msg, "blueprint convert") {
		t.Errorf("message references a command that no longer exists:\n%s", msg)
	}
}
