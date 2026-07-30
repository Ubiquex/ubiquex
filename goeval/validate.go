package goeval

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
)

// validateIntentShape strict-unmarshals canon against the real
// core/resolver.IntentFile type -- the same reuse sdkeval's own
// validateIntentShape already established as correct (one canonical
// source of truth for the wire shape, not a second, hand-maintained
// schema that could silently drift from it). Duplicated rather than
// shared for the same small-and-language-agnostic reasoning
// provenance.go's own doc comment gives.
func validateIntentShape(canon []byte) error {
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.DisallowUnknownFields()
	var doc resolver.IntentFile
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("goeval: evaluated output does not match ubx:intent/v1's own shape: %w", err)
	}

	if doc.SchemaVersion != 1 {
		return fmt.Errorf("goeval: evaluated output has schema_version %d, want 1", doc.SchemaVersion)
	}
	if doc.Kind != "ubx:intent/v1" {
		return fmt.Errorf("goeval: evaluated output has kind %q, want \"ubx:intent/v1\"", doc.Kind)
	}
	if doc.Stack == "" {
		return fmt.Errorf("goeval: evaluated output has an empty stack name")
	}
	if doc.Intent.Summary == "" {
		return fmt.Errorf("goeval: evaluated output has an empty intent.summary")
	}

	for i, r := range doc.Resources {
		if r.Type == "" || r.Name == "" {
			return fmt.Errorf("goeval: resources[%d]: type and name are both required", i)
		}
		if r.Op != resolver.OpCreate {
			return fmt.Errorf("goeval: resources[%d] (%s.%s): op %q -- a Go SDK program can only ever emit \"create\" in v1 (a hermetic, describe-only program has no way to know an address already exists, so it has no basis to ever choose \"modify\")", i, r.Type, r.Name, r.Op)
		}
		var configObj map[string]json.RawMessage
		if err := json.Unmarshal(r.Config, &configObj); err != nil {
			return fmt.Errorf("goeval: resources[%d] (%s.%s): config is not a JSON object: %w", i, r.Type, r.Name, err)
		}
	}

	return nil
}
