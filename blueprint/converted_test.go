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

func TestValidate_ConvertedBlueprint_SaysSoAndNamesTheFix(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

converted_from: /modules/vpc

resources: |
  Converted mechanically (ubx blueprint convert, no AI) from the Terraform module at /modules/vpc. 1 resource(s).
`)
	_, _, err := Validate(dir)
	if err == nil {
		t.Fatal("expected a converted blueprint to be refused")
	}
	msg := err.Error()

	// The reader has to learn three things: what this is, why it cannot
	// be rebuilt, and what to run instead.
	for _, want := range []string{
		"converted from the Terraform module at /modules/vpc",
		"cannot be rebuilt",
		"ubx blueprint convert --from-terraform /modules/vpc",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not tell the reader %q:\n%s", want, msg)
		}
	}
	// And must not be the old offset-only report.
	if strings.Contains(msg, "looking for beginning of value") {
		t.Errorf("still reporting a JSON parse offset for a case it can identify outright:\n%s", msg)
	}
}

// Blueprints converted before converted_from existed carry no marker, so
// the shape of resources: is the only signal left. The message offers the
// possibility rather than asserting it, because an authored blueprint
// with malformed JSON reaches the same branch.
func TestValidate_ConvertedWithoutMarker_StillOffersTheRightFix(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

resources: |
  Converted mechanically (ubx blueprint convert, no AI) from the Terraform module at ./tf. 1 resource(s).
`)
	_, _, err := Validate(dir)
	if err == nil {
		t.Fatal("expected prose resources: to be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does not even begin as a JSON object") {
		t.Errorf("expected the message to name the shape problem:\n%s", msg)
	}
	if !strings.Contains(msg, "ubx blueprint convert") {
		t.Errorf("expected the message to raise conversion as the likely cause:\n%s", msg)
	}
	// Offered, not asserted: an authored blueprint lands here too.
	if !strings.Contains(msg, "If this blueprint was produced by") {
		t.Errorf("expected conversion to be offered as a possibility, not stated as fact:\n%s", msg)
	}
}

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

// converted_from is a new key and the Ubxfile decoder is strict
// (KnownFields), so an authored blueprint that has never heard of it must
// still parse, and the marker must survive a round trip through the file.
func TestParseUbxfile_ConvertedFromIsOptionalAndRoundTrips(t *testing.T) {
	withoutMarker := writeUbxfile(t, `lang: go

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"x","resources":[]}
`)
	uf, err := ParseUbxfile(withoutMarker)
	if err != nil {
		t.Fatalf("an Ubxfile with no converted_from must still parse: %v", err)
	}
	if uf.ConvertedFrom != "" {
		t.Errorf("ConvertedFrom = %q, want empty", uf.ConvertedFrom)
	}

	withMarker := writeUbxfile(t, `lang: go

converted_from: /modules/vpc

resources: |
  {"schema_version":1,"kind":"ubx:intent/v1","stack":"x","resources":[]}
`)
	uf2, err := ParseUbxfile(withMarker)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if uf2.ConvertedFrom != "/modules/vpc" {
		t.Errorf("ConvertedFrom = %q, want /modules/vpc", uf2.ConvertedFrom)
	}
}
