package blueprint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint/spec"
	"github.com/ubiquex/ubiquex/core"
)

// The schema is derived and feeds a content hash, so its serialization
// has to be canonical and its shape has to be stable.

func sampleSchema() Schema {
	return Schema{
		SchemaVersion: SchemaVersion,
		Name:          "ubx-aws-sqs",
		Entrypoint: Entrypoint{
			Language: "go", GoModule: "github.com/ubx-blueprints/ubx-aws-sqs/go",
			GoPackage: "ubxawssqs", Function: "UbxAwsSqs",
			ConfigType: "Config", OutputsType: "Outputs",
		},
		Params: []SchemaParam{
			{Name: "name", SourceName: "Name", Type: spec.ParamString, Required: true},
			{Name: "visibility_timeout", SourceName: "VisibilityTimeout", Type: spec.ParamNumber, Required: false},
		},
		Outputs:    []SchemaOutput{{Name: "queue_url", SourceName: "QueueURL"}, {Name: "queue_arn", SourceName: "QueueARN"}},
		Defaults:   DefaultsNotDerivable,
		Derivation: Derivation{Assumptions: []string{}},
	}
}

// It is written into a packaged directory whose content hash covers it,
// so the same schema must serialize to the same bytes every time.
func TestSchema_CanonicalizesDeterministically(t *testing.T) {
	first, err := core.CanonicalJSON(sampleSchema())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := core.CanonicalJSON(sampleSchema())
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("canonical form is not stable:\n%s\n%s", first, again)
		}
	}
}

// Parameter order is declaration order, not sorted. A derived
// per-language wrapper needs a stable order, and Go's field order is the
// only ordering all three languages agree on, so canonicalization must
// not reorder the array.
func TestSchema_ParamOrderSurvivesCanonicalization(t *testing.T) {
	canon, err := core.CanonicalJSON(sampleSchema())
	if err != nil {
		t.Fatal(err)
	}
	name := strings.Index(string(canon), `"name"`)
	timeout := strings.Index(string(canon), `"visibility_timeout"`)
	if name < 0 || timeout < 0 || name > timeout {
		t.Fatalf("declaration order was not preserved:\n%s", canon)
	}
}

// A param carries no default, and the absence is stated in the document
// rather than left to be inferred. Under the Ubxfile the default was
// declared and describe_blueprint reported it; under the config-struct
// convention it lives in the function body where no extractor can see
// it. A reader has to be able to tell that decision from a failed
// extraction.
func TestSchema_DefaultAbsenceIsStatedNotImplied(t *testing.T) {
	data, err := json.Marshal(sampleSchema())
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}

	params := generic["params"].([]any)
	for _, p := range params {
		if _, present := p.(map[string]any)["default"]; present {
			t.Fatalf("a param must carry no default key at all, got %s", data)
		}
	}

	defaults := generic["defaults"].(map[string]any)
	if defaults["derivable"] != false {
		t.Fatalf("defaults.derivable must be present and false, got %s", data)
	}
	if reason, _ := defaults["reason"].(string); !strings.Contains(reason, "function body") {
		t.Fatalf("defaults.reason must say where the default actually lives, got %q", reason)
	}
}

// The assumption list exists so a TS-derived schema can record what it
// asserted without being able to verify it. Go and Python carry none.
func TestSchema_AssumptionsAreRecordedNotDropped(t *testing.T) {
	s := sampleSchema()
	s.Entrypoint.Language = "ts"
	s.Derivation.Assumptions = []string{TSNumberIsIntAssumption}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "TypeScript's number type cannot express this") {
		t.Fatalf("the TS int assumption must survive serialization, got %s", data)
	}
}

// The schema file is the blueprint marker now, replacing the Ubxfile in
// that role for list_blueprints and for resolve's provenance walk.
func TestSchemaFileName_IsTheMarker(t *testing.T) {
	if SchemaFileName != "blueprint.schema.json" {
		t.Fatalf("consumers look for this exact name, got %q", SchemaFileName)
	}
	if SchemaFileName == UbxfileName {
		t.Fatal("the marker must be distinct from the Ubxfile it replaces")
	}
}
