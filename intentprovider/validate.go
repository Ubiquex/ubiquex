package intentprovider

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// wireIntentFile is the exact shape an Adapter's own structured output is
// constrained to (schema.go's own IntentDraftJSONSchema) -- identical to
// resolver.IntentFile/core.Intent in every field except one: Config is a
// JSON-ENCODED STRING, not a nested JSON value.
//
// This is not a stylistic choice, checked directly against the
// documented structured-output constraint before writing it this way
// (Claude's own structured-outputs support requires "additionalProperties:
// false" on every object schema node, including ones with no declared
// "properties"): a resource's own config is fundamentally open-shaped --
// whatever attributes that resource type's schema needs, unknowable to
// this package -- and there is no way to express "any object shape at
// all" under a schema that must close every object node. Not
// live-verified against the real API this session (no credentials in
// this environment) -- flagged here explicitly, to be confirmed on the
// first real live run, not assumed correct from documentation alone. If
// it turns out wrong, only schema.go's own IntentDraftJSONSchema and this
// file's own toIntentFile need to change -- the API-level schema is an
// optimization DraftWithRetry never trusts alone (see driver.go); a
// config-as-object schema, once confirmed workable, would simply make
// step-2 (this file's own validation) reject fewer attempts, not change
// what "valid" means.
//
// Sources is deliberately absent from this shape (and from the schema
// handed to an Adapter): document/intent_provider provenance is ubx's
// own authority, never the model's -- it has no way to correctly compute
// a content_hash or reliably know its own adapter/model identity string.
// sources.go's PopulateSources fills this in after a draft validates.
type wireIntentFile struct {
	SchemaVersion  int64                `json:"schema_version"`
	Kind           string               `json:"kind"`
	Stack          string               `json:"stack"`
	Intent         wireIntent           `json:"intent"`
	Resources      []wireResourceIntent `json:"resources"`
	Destroys       []string             `json:"destroys"`
	BlueprintCalls []wireBlueprintCall  `json:"blueprint_calls"`
	Overrides      []wireOverride       `json:"overrides"`
}

// wireOverride is UBI-86 Part 2's own md calling-convention wire shape --
// mirrors wireResourceIntent's own "Config is a JSON-encoded STRING"
// workaround exactly, for the identical reason (an override's own
// config is fundamentally open-shaped, field name -> new raw value, and
// a structured-output schema must close every object node -- schema.go's
// own IntentDraftJSONSchema has the full account). This is the ONE call
// site in this whole package that needs AI at all for UBI-86's override
// mechanism (docs/blueprint.md's own "Override mechanism" section) -- a
// thin mapping step, never a re-drafting of the target resource.
type wireOverride struct {
	Address string `json:"address"`
	Config  string `json:"config"` // JSON-encoded object string, see wireIntentFile's own doc comment
}

// wireBlueprintCall is UBI-74 Slice 5's own md calling-convention wire
// shape -- mirrors wireResourceIntent's own "Config is a JSON-encoded
// STRING" workaround exactly, for the identical reason (Args is
// fundamentally open-shaped, param-name -> raw value, and a structured-
// output schema must close every object node -- schema.go's own
// IntentDraftJSONSchema has the full account).
type wireBlueprintCall struct {
	Name      string `json:"name"`
	CallName  string `json:"call_name"` // UBI-128: the document's own "as '<name>'" alias, "" if none given
	Blueprint string `json:"blueprint"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	Args      string `json:"args"` // JSON-encoded object string, param name -> raw string value
}

type wireIntent struct {
	Summary     string               `json:"summary"`
	Assumptions []core.AmbiguityNote `json:"assumptions"`
	Defaults    []core.AmbiguityNote `json:"defaults"`
	Questions   []core.Question      `json:"questions"`
}

type wireResourceIntent struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Op     string `json:"op"`
	Config string `json:"config"` // JSON-encoded object string, see wireIntentFile's own doc comment
}

// parseAndValidate decodes raw against wireIntentFile, translates it into
// a real resolver.IntentFile (decoding each resource's own config string
// into json.RawMessage), and runs every structural check that doesn't
// require ledger/provider access -- mirroring (never literally sharing
// code with, since resolve's own checks are inline and ledger-coupled,
// and an Adapter must never gain ledger/provider access even indirectly
// through a shared validator) the same "well-formed addresses, op in
// {create, modify}, no duplicate resource address" rules ubx resolve
// itself enforces before it will touch a proposal. Ledger/provider-
// dependent checks (does this address already exist, does a declared
// provider own this type) remain exclusively ubx resolve's own job, run
// later, unchanged.
//
// Returns a non-nil *resolver.IntentFile only when the returned error
// slice is empty. Every message is field-path-prefixed (e.g.
// "resources[1].op: ...") so DraftWithRetry can feed it back to an
// Adapter verbatim as PriorErrors, and a human reading a hard-fail's own
// error can find exactly what was wrong.
//
// allowEmptyDraft (UBI-85) is DraftWithRetry's own len(knownResources) >
// 0 -- see that function's own doc comment for why real existing-state
// context makes an all-empty resources+destroys draft a legitimate
// outcome (everything already matches) rather than the original "model
// reasoned about a change and forgot to declare it" bug the check below
// exists to catch.
func parseAndValidate(raw json.RawMessage, stack string, allowEmptyDraft bool) (*resolver.IntentFile, []string) {
	var wire wireIntentFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return nil, []string{fmt.Sprintf("invalid JSON: %v", err)}
	}

	var errs []string

	if wire.SchemaVersion != 1 {
		errs = append(errs, fmt.Sprintf("schema_version: want 1, got %d", wire.SchemaVersion))
	}
	if wire.Kind != "ubx:intent/v1" {
		errs = append(errs, fmt.Sprintf("kind: want \"ubx:intent/v1\", got %q", wire.Kind))
	}
	if wire.Stack != stack {
		errs = append(errs, fmt.Sprintf("stack: want %q, got %q", stack, wire.Stack))
	}
	if wire.Intent.Summary == "" {
		errs = append(errs, "intent.summary: must not be empty")
	}
	// A real bug found live, not assumed away: a draft with a
	// substantive summary/assumptions describing a resource, but an
	// EMPTY resources[] -- the model reasoned about a change and then
	// never actually recorded it. Confirmed against the real Claude API
	// (see STATE.md's own account of this session): a real response
	// carried a fully-reasoned intent.assumptions entry naming
	// aws_db_instance.payments.instance_class while resources[] was
	// simply []. Nothing else in this function catches "described but
	// never declared" -- a totally empty draft that changes nothing is
	// never a valid change proposal in THAT scenario, so this stayed a
	// hard requirement.
	//
	// UBI-85: allowEmptyDraft carves out the one legitimate exception --
	// a re-plan against a stack whose existing state was actually given
	// as context, where the document genuinely describes nothing that
	// differs from what's already recorded. That's a real, correct "no
	// changes" outcome (the whole point of feeding known-resources
	// context in the first place), not the original bug -- rejecting it
	// would just reintroduce, one layer up, the exact "every unchanged
	// resource drafts as a fresh create" defect UBI-85 exists to fix.
	if len(wire.Resources) == 0 && len(wire.Destroys) == 0 && len(wire.BlueprintCalls) == 0 && len(wire.Overrides) == 0 && !allowEmptyDraft {
		errs = append(errs, "draft has no resources, no destroys, no blueprint_calls, and no overrides -- nothing to propose; if you described a resource in your reasoning, you must also add it to resources[] (or, for a blueprint invocation, to blueprint_calls[]; for an override statement, to overrides[])")
	}

	seen := map[string]bool{}
	resources := make([]resolver.ResourceIntent, 0, len(wire.Resources))
	for i, r := range wire.Resources {
		path := fmt.Sprintf("resources[%d]", i)
		if r.Type == "" {
			errs = append(errs, path+".type: must not be empty")
		}
		if r.Name == "" {
			errs = append(errs, path+".name: must not be empty")
		}
		if r.Op != resolver.OpCreate && r.Op != resolver.OpModify {
			errs = append(errs, fmt.Sprintf("%s.op: must be %q or %q, got %q", path, resolver.OpCreate, resolver.OpModify, r.Op))
		}
		addr := r.Type + "." + r.Name
		if seen[addr] {
			errs = append(errs, fmt.Sprintf("%s: duplicate resource address %q within this draft", path, addr))
		}
		seen[addr] = true

		var config json.RawMessage
		switch {
		case r.Config == "":
			errs = append(errs, path+".config: must not be empty")
		default:
			if err := json.Unmarshal([]byte(r.Config), &config); err != nil {
				errs = append(errs, fmt.Sprintf("%s.config: not valid JSON: %v", path, err))
			}
		}

		resources = append(resources, resolver.ResourceIntent{
			Type:   r.Type,
			Name:   r.Name,
			Op:     r.Op,
			Config: config,
		})
	}

	for i, d := range wire.Destroys {
		if _, ok := core.ParseAddress(d); !ok {
			errs = append(errs, fmt.Sprintf("destroys[%d]: %q is not a well-formed <stack>.<type>.<name> address", i, d))
		}
	}

	blueprintCalls := make([]resolver.BlueprintCall, 0, len(wire.BlueprintCalls))
	seenCallName := map[string]int{} // call_name -> the blueprint_calls[] index that first claimed it
	for i, c := range wire.BlueprintCalls {
		path := fmt.Sprintf("blueprint_calls[%d]", i)
		if c.Name == "" {
			errs = append(errs, path+".name: must not be empty")
		}
		if c.Blueprint == "" {
			errs = append(errs, path+".blueprint: must not be empty")
		}
		// UBI-128: two calls sharing one call_name would silently collide
		// once blueprint.ExpandCalls aggregates every call's own outputs
		// into one document-wide "<call_name>:<output_key> -> address" map
		// (blueprint/invoke.go) -- caught here, at draft-validation time,
		// rather than surfacing later as a wrong/overwritten output
		// reference with no clear cause.
		if c.CallName != "" {
			if other, ok := seenCallName[c.CallName]; ok {
				errs = append(errs, fmt.Sprintf("%s.call_name: %q is also used by blueprint_calls[%d] -- each call this document names for later output reference needs its own distinct call_name", path, c.CallName, other))
			} else {
				seenCallName[c.CallName] = i
			}
		}
		args := map[string]string{}
		if c.Args != "" {
			if err := json.Unmarshal([]byte(c.Args), &args); err != nil {
				errs = append(errs, fmt.Sprintf("%s.args: not a valid JSON object of string values: %v", path, err))
			}
		}
		blueprintCalls = append(blueprintCalls, resolver.BlueprintCall{
			Name:      c.Name,
			CallName:  c.CallName,
			Blueprint: c.Blueprint,
			Ref:       c.Ref,
			Path:      c.Path,
			Args:      args,
		})
	}

	overrides := make([]resolver.Override, 0, len(wire.Overrides))
	for i, ov := range wire.Overrides {
		path := fmt.Sprintf("overrides[%d]", i)
		if ov.Address == "" {
			errs = append(errs, path+".address: must not be empty")
		} else if _, ok := core.ParseAddress(ov.Address); !ok {
			errs = append(errs, fmt.Sprintf("%s.address: %q is not a well-formed <stack>.<type>.<name> address", path, ov.Address))
		}
		config := map[string]json.RawMessage{}
		switch {
		case ov.Config == "":
			errs = append(errs, path+".config: must not be empty")
		default:
			if err := json.Unmarshal([]byte(ov.Config), &config); err != nil {
				errs = append(errs, fmt.Sprintf("%s.config: not a valid JSON object: %v", path, err))
			}
		}
		overrides = append(overrides, resolver.Override{Address: ov.Address, Config: config})
	}

	for i, a := range wire.Intent.Assumptions {
		if a.Text == "" {
			errs = append(errs, fmt.Sprintf("intent.assumptions[%d].text: must not be empty", i))
		}
	}
	for i, d := range wire.Intent.Defaults {
		if d.Text == "" {
			errs = append(errs, fmt.Sprintf("intent.defaults[%d].text: must not be empty", i))
		}
	}
	for i, q := range wire.Intent.Questions {
		if q.Text == "" {
			errs = append(errs, fmt.Sprintf("intent.questions[%d].text: must not be empty", i))
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return &resolver.IntentFile{
		SchemaVersion: wire.SchemaVersion,
		Kind:          wire.Kind,
		Stack:         wire.Stack,
		Intent: core.Intent{
			Summary:     wire.Intent.Summary,
			Assumptions: wire.Intent.Assumptions,
			Defaults:    wire.Intent.Defaults,
			Questions:   wire.Intent.Questions,
		},
		Resources:      resources,
		Destroys:       wire.Destroys,
		BlueprintCalls: blueprintCalls,
		Overrides:      overrides,
	}, nil
}
