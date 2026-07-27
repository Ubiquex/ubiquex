package intentprovider

import (
	"encoding/json"
	"testing"
)

// TestIntentDraftJSONSchema_MarshalsAndHasExpectedShape is a light sanity
// check, not a claim of API-level correctness (schema.go's own doc
// comment names the config-as-string divergence as unverified against
// the real API this session). It exists to catch a schema.go typo (a
// stray Go value structured-output can't marshal, a missing required
// key) before it ever reaches a real request.
func TestIntentDraftJSONSchema_MarshalsAndHasExpectedShape(t *testing.T) {
	schema := IntentDraftJSONSchema()

	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("schema does not marshal to JSON: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("marshaled schema does not round-trip: %v", err)
	}

	for _, key := range []string{"schema_version", "kind", "stack", "intent", "resources", "destroys"} {
		props, ok := round["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema has no top-level \"properties\" object")
		}
		if _, ok := props[key]; !ok {
			t.Errorf("schema is missing top-level property %q", key)
		}
	}

	if round["additionalProperties"] != false {
		t.Error(`top-level schema must set "additionalProperties": false`)
	}
}
