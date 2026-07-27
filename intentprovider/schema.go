package intentprovider

// IntentDraftJSONSchema returns the hand-maintained JSON Schema handed to
// an Adapter's own API-level structured-output constraint (e.g. Claude's
// output_config.format, docs/intent-provider.md's own "The Claude
// adapter" section) -- the API-level half of the two-layer validation
// DraftWithRetry performs; validate.go's parseAndValidate is the
// ubx-side half that always runs regardless of what an adapter's own
// vendor claims to enforce.
//
// Deliberately NOT derived from resolver.IntentFile/core.Intent by
// reflection -- one canonical, hand-authored spec, the same discipline
// docs/schema.md's own wire formats already follow (no reflection-based
// JSON-Schema generator exists anywhere in this codebase, and this
// document doesn't introduce one). Two deliberate divergences from
// resolver.IntentFile's real wire shape, both explained in full in
// validate.go's wireIntentFile doc comment: a resource's own "config" is
// a JSON-encoded STRING, not a nested object (structured-output schemas
// cannot express an open-ended object shape); "sources" is entirely
// absent (ubx's own authority, never the model's).
func IntentDraftJSONSchema() map[string]any {
	ambiguityNote := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"text", "affects"},
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Plain-language statement of the interpretation made or the gap filled.",
			},
			"affects": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "Canonical <type>.<name>.<path> config paths this note is about " +
					"(e.g. \"aws_db_instance.payments-db.instance_class\") -- empty if it doesn't map to one specific value.",
			},
		},
	}
	question := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"text", "affects", "blocking"},
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Plain-language open question or stated tension, naming both sides of any contradiction.",
			},
			"affects": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"blocking": map[string]any{
				"type": "boolean",
				"description": "true when this question represents a genuine, unresolved " +
					"contradiction the draft still had to pick one side of -- a review-affordance " +
					"signal only, never a reason to omit or refuse producing a complete draft.",
			},
		},
	}
	resource := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "name", "op", "config"},
		"properties": map[string]any{
			"type": map[string]any{
				"type":        "string",
				"description": "The provider resource type name, e.g. \"aws_db_instance\". Never invent a plausible-sounding type you're not confident exists -- if uncertain, still pick your best answer here, but record the uncertainty as a question.",
			},
			"name": map[string]any{"type": "string"},
			"op":   map[string]any{"type": "string", "enum": []string{"create", "modify"}},
			"config": map[string]any{
				"type": "string",
				"description": "A JSON-encoded object string -- the resource's full desired " +
					"config, exactly as it would appear as a hand-written ubx:intent/v1 file's own " +
					"resources[].config value, e.g. " +
					"\"{\\\"instance_class\\\":\\\"db.t3.medium\\\"}\". Must decode as valid JSON.",
			},
		},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schema_version", "kind", "stack", "intent", "resources", "destroys"},
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "const": 1},
			"kind":           map[string]any{"type": "string", "const": "ubx:intent/v1"},
			"stack":          map[string]any{"type": "string"},
			"intent": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"summary", "assumptions", "defaults", "questions"},
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "One-sentence, human-readable summary of the whole change.",
					},
					"assumptions": map[string]any{
						"type":  "array",
						"items": ambiguityNote,
						"description": "Every place the source document was ambiguous and you had " +
							"to interpret it -- empty array if genuinely none. Never omit an entry " +
							"just because you're confident in the interpretation; confidence isn't " +
							"the test, ambiguity in the source is.",
					},
					"defaults": map[string]any{
						"type":  "array",
						"items": ambiguityNote,
						"description": "Every place the source document said nothing at all and " +
							"you filled the gap from context or convention -- empty array if none.",
					},
					"questions": map[string]any{
						"type":  "array",
						"items": question,
						"description": "Contradictions between stated requirements, and any " +
							"ambiguity too open-ended to responsibly guess at -- empty array if none.",
					},
				},
			},
			"resources": map[string]any{"type": "array", "items": resource},
			"destroys": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Canonical <stack>.<type>.<name> addresses to remove. Never inferred from a resource's absence -- only from an explicit removal request in the document.",
			},
		},
	}
}
