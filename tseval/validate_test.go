package tseval

import "testing"

// Adversarial row 5 ("output exceeding the intent/v1 schema") is
// covered here at the Go level, hermetically, with hand-crafted JSON
// bytes -- not by a real deno subprocess. @ubx/sdk's own runtime
// (sdk/ts/runtime, slice 3) already preemptively rejects every one of
// these shapes at its own API boundary (Collector.setIntent/addResource
// throw on an empty summary/name; op is hardcoded to "create" by the
// collector itself, never read from a program's own input) -- there is
// no realistic, honest way to make a normal SDK program reach this
// Go-side check with malformed output through the legitimate
// resource()/intent() surface. This function exists as real
// defense-in-depth (a bug in a future runtime change, or an older/
// differently-versioned @ubx/sdk than this harness expects), and is
// tested directly, the same way sdk/codegen/ir's own tests fabricate a
// provider.Schema rather than needing a real provider binary.
func TestValidateIntentShape_Valid(t *testing.T) {
	valid := []byte(`{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "payments",
		"intent": {"summary": "a valid document"},
		"resources": [{"type": "fake_widget", "name": "primary", "op": "create", "config": {"name": "x"}}]
	}`)
	if err := validateIntentShape(valid); err != nil {
		t.Fatalf("validateIntentShape(valid): %v", err)
	}
}

func TestValidateIntentShape_WrongSchemaVersion(t *testing.T) {
	bad := []byte(`{"schema_version": 2, "kind": "ubx:intent/v1", "stack": "payments", "intent": {"summary": "x"}, "resources": []}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for schema_version 2, want an error")
	}
}

func TestValidateIntentShape_WrongKind(t *testing.T) {
	bad := []byte(`{"schema_version": 1, "kind": "something:else", "stack": "payments", "intent": {"summary": "x"}, "resources": []}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for a wrong kind, want an error")
	}
}

func TestValidateIntentShape_EmptyStack(t *testing.T) {
	bad := []byte(`{"schema_version": 1, "kind": "ubx:intent/v1", "stack": "", "intent": {"summary": "x"}, "resources": []}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for an empty stack, want an error")
	}
}

func TestValidateIntentShape_EmptySummary(t *testing.T) {
	bad := []byte(`{"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments", "intent": {"summary": ""}, "resources": []}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for an empty intent.summary, want an error")
	}
}

func TestValidateIntentShape_ModifyOpRejected(t *testing.T) {
	// resolver.IntentFile's own Go shape legally allows "modify" (a
	// hand-written intent file can use it) -- but an SDK-evaluated
	// document must never contain one, per this session's own real,
	// load-bearing decision (docs/sdk.md: a hermetic, describe-only
	// program has no way to know an address already exists).
	bad := []byte(`{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
		"intent": {"summary": "x"},
		"resources": [{"type": "fake_widget", "name": "primary", "op": "modify", "config": {}}]
	}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for op \"modify\", want an error")
	}
}

func TestValidateIntentShape_ConfigNotAnObject(t *testing.T) {
	bad := []byte(`{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
		"intent": {"summary": "x"},
		"resources": [{"type": "fake_widget", "name": "primary", "op": "create", "config": "not-an-object"}]
	}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for a string config, want an error")
	}
}

func TestValidateIntentShape_MissingResourceNameOrType(t *testing.T) {
	bad := []byte(`{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
		"intent": {"summary": "x"},
		"resources": [{"type": "", "name": "primary", "op": "create", "config": {}}]
	}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for an empty resource type, want an error")
	}
}

func TestValidateIntentShape_UnknownTopLevelField(t *testing.T) {
	// DisallowUnknownFields -- a genuinely different shape (e.g. output
	// from a mismatched/future runtime version adding a field this
	// harness doesn't know about) is a hard failure, not silently
	// accepted.
	bad := []byte(`{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
		"intent": {"summary": "x"}, "resources": [],
		"totally_unknown_field": true
	}`)
	if err := validateIntentShape(bad); err == nil {
		t.Fatal("validateIntentShape: got nil error for an unknown top-level field, want an error")
	}
}

func TestValidateIntentShape_MalformedJSON(t *testing.T) {
	if err := validateIntentShape([]byte(`{not valid json`)); err == nil {
		t.Fatal("validateIntentShape: got nil error for malformed JSON, want an error")
	}
}
