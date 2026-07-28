package sdkeval

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
)

// validateIntentShape is docs/sdk.md's own adversarial row 5 ("output
// exceeding the intent/v1 schema") -- refined during implementation from
// the original design's own sketch. Session 1's docs/sdk.md said this
// would reuse "the identical schema --from-doc's own structured-output
// validation already uses" (intentprovider.IntentDraftJSONSchema) --
// checked directly while building this, and that turned out to be
// wrong: that schema is DELIBERATELY a different, incompatible shape
// (its own doc comment says so explicitly) -- a resource's own "config"
// is a JSON-encoded STRING there, not a real nested object; "sources" is
// entirely absent; intent.assumptions/defaults/questions are REQUIRED
// even when empty. None of that matches what @ubx/sdk's own runtime
// actually emits. Reusing it here would either reject every valid SDK
// document or validate the wrong shape entirely.
//
// Instead: strict-unmarshal against core/resolver.IntentFile -- the
// REAL, load-bearing Go type "ubx resolve" itself already parses a
// hand-written intent file into. One canonical source of truth for the
// wire shape, not a second, hand-maintained JSON Schema that could
// silently drift from it -- a better reuse than what was originally
// planned, not a downgrade from it.
func validateIntentShape(canon []byte) error {
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.DisallowUnknownFields()
	var doc resolver.IntentFile
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("sdkeval: evaluated output does not match ubx:intent/v1's own shape: %w", err)
	}

	if doc.SchemaVersion != 1 {
		return fmt.Errorf("sdkeval: evaluated output has schema_version %d, want 1", doc.SchemaVersion)
	}
	if doc.Kind != "ubx:intent/v1" {
		return fmt.Errorf("sdkeval: evaluated output has kind %q, want \"ubx:intent/v1\"", doc.Kind)
	}
	if doc.Stack == "" {
		return fmt.Errorf("sdkeval: evaluated output has an empty stack name")
	}
	if doc.Intent.Summary == "" {
		return fmt.Errorf("sdkeval: evaluated output has an empty intent.summary")
	}

	for i, r := range doc.Resources {
		if r.Type == "" || r.Name == "" {
			return fmt.Errorf("sdkeval: resources[%d]: type and name are both required", i)
		}
		if r.Op != resolver.OpCreate {
			return fmt.Errorf("sdkeval: resources[%d] (%s.%s): op %q -- an SDK program can only ever emit \"create\" in v1 (docs/sdk.md: a hermetic, describe-only program has no way to know an address already exists, so it has no basis to ever choose \"modify\")", i, r.Type, r.Name, r.Op)
		}
		var configObj map[string]json.RawMessage
		if err := json.Unmarshal(r.Config, &configObj); err != nil {
			return fmt.Errorf("sdkeval: resources[%d] (%s.%s): config is not a JSON object: %w", i, r.Type, r.Name, err)
		}
	}

	return nil
}
