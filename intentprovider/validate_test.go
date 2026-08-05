package intentprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAndValidate_Valid(t *testing.T) {
	draft, errs := parseAndValidate(json.RawMessage(validPaymentsDraft), "payments", false)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if draft == nil {
		t.Fatal("expected a non-nil draft")
	}
	if string(draft.Resources[0].Config) != `{"instance_class":"db.t3.small"}` {
		t.Errorf("Config = %s, want the decoded JSON object, not the wire string", draft.Resources[0].Config)
	}
}

func TestParseAndValidate_RejectsCases(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string // substring expected somewhere in the returned errors
	}{
		{
			name:    "malformed JSON",
			raw:     `{not json`,
			wantErr: "invalid JSON",
		},
		{
			name:    "wrong schema_version",
			raw:     `{"schema_version":2,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "schema_version",
		},
		{
			name:    "wrong kind",
			raw:     `{"schema_version":1,"kind":"bogus","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "kind",
		},
		{
			name:    "stack mismatch",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"networking","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "stack",
		},
		{
			name:    "empty summary",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "intent.summary",
		},
		{
			name:    "invalid op",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[{"type":"aws_db_instance","name":"db","op":"destroy","config":"{}"}],"destroys":[]}`,
			wantErr: ".op:",
		},
		{
			name:    "empty resource type",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[{"type":"","name":"db","op":"create","config":"{}"}],"destroys":[]}`,
			wantErr: ".type:",
		},
		{
			name:    "config not valid JSON",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[{"type":"aws_db_instance","name":"db","op":"create","config":"not json"}],"destroys":[]}`,
			wantErr: ".config: not valid JSON",
		},
		{
			name:    "malformed destroy address",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":["not-an-address"]}`,
			wantErr: "not a well-formed",
		},
		{
			name:    "empty assumption text",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[{"text":"","affects":[]}],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "intent.assumptions[0].text",
		},
		{
			name:    "empty question text",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[{"text":"","affects":[],"blocking":false}]},"resources":[],"destroys":[]}`,
			wantErr: "intent.questions[0].text",
		},
		{
			name:    "unknown top-level field",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"sources":[{"kind":"document"}]}`,
			wantErr: "invalid JSON",
		},
		{
			// A real bug found live against the real Claude API (see
			// STATE.md): a substantive summary/assumptions describing a
			// resource, but an empty resources[] -- the model reasoned
			// about a change and never actually recorded it.
			name:    "empty resources and empty destroys",
			raw:     `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"provision a database like staging but smaller","assumptions":[{"text":"chose db.t3.small","affects":["aws_db_instance.payments.instance_class"]}],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`,
			wantErr: "no resources, no destroys, no blueprint_calls, and no overrides",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// allowEmptyDraft=false throughout this table -- every case
			// here represents drafting with NO known-resources context
			// (the pre-UBI-85 default), where an empty resources+destroys
			// draft is still always the original "forgot to declare it"
			// bug, never a legitimate outcome. See
			// TestParseAndValidate_EmptyDraftAllowedWithKnownContext for
			// the new, opposite case.
			draft, errs := parseAndValidate(json.RawMessage(tt.raw), "payments", false)
			if draft != nil {
				t.Error("expected a nil draft on validation failure")
			}
			if len(errs) == 0 {
				t.Fatal("expected at least one validation error, got none")
			}
			joined := strings.Join(errs, " | ")
			if !strings.Contains(joined, tt.wantErr) {
				t.Errorf("errors = %v, want one containing %q", errs, tt.wantErr)
			}
		})
	}
}

// TestParseAndValidate_EmptyDraftAllowedWithKnownContext is UBI-85's own
// regression test for the validation relaxation: the exact same
// empty-resources-and-destroys shape TestParseAndValidate_RejectsCases's
// own "empty resources and empty destroys" case still rejects when
// allowEmptyDraft is false is now VALID when true -- the target stack's
// own existing state was actually given as context, so "everything
// already matches" is a legitimate, correct outcome, not the original
// forgot-to-declare-it bug.
func TestParseAndValidate_EmptyDraftAllowedWithKnownContext(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"No changes: all resources already match their recorded state","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[]}`
	draft, errs := parseAndValidate(json.RawMessage(raw), "payments", true)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors with allowEmptyDraft=true: %v", errs)
	}
	if draft == nil {
		t.Fatal("expected a non-nil draft")
	}
	if len(draft.Resources) != 0 || len(draft.Destroys) != 0 {
		t.Fatalf("expected an empty no-op draft, got resources=%+v destroys=%+v", draft.Resources, draft.Destroys)
	}
}

// TestParseAndValidate_BlueprintCall is UBI-74 Slice 5's own required
// hermetic proof for the md calling convention's wire shape: a draft
// naming ONLY a blueprint_calls entry, no resources[] at all, is a
// legitimate, complete draft (not the "described but never declared"
// bug empty-resources normally catches) -- args decodes from its own
// JSON-encoded string form into a real map[string]string.
func TestParseAndValidate_BlueprintCall(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"call the ci-platform blueprint","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"blueprint_calls":[{"name":"ci-platform call","blueprint":"../ci-platform","ref":"","path":"","args":"{\"repo_name\":\"payments-ci-artifacts\",\"queue_name\":\"payments-notifications\"}"}]}`
	draft, errs := parseAndValidate(json.RawMessage(raw), "payments", false)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if draft == nil {
		t.Fatal("expected a non-nil draft")
	}
	if len(draft.Resources) != 0 {
		t.Fatalf("expected zero resources[] for a blueprint-call-only draft, got %+v", draft.Resources)
	}
	if len(draft.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly one blueprint call, got %d", len(draft.BlueprintCalls))
	}
	call := draft.BlueprintCalls[0]
	if call.Blueprint != "../ci-platform" {
		t.Errorf("Blueprint = %q, want %q", call.Blueprint, "../ci-platform")
	}
	if call.Args["repo_name"] != "payments-ci-artifacts" || call.Args["queue_name"] != "payments-notifications" {
		t.Fatalf("Args decoded wrong: %+v", call.Args)
	}
}

// TestParseAndValidate_BlueprintCallMissingBlueprint confirms a
// blueprint_calls entry with no blueprint reference is a hard, named
// validation error -- never silently dropped or defaulted.
func TestParseAndValidate_BlueprintCallMissingBlueprint(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"blueprint_calls":[{"name":"x","blueprint":"","ref":"","path":"","args":"{}"}]}`
	_, errs := parseAndValidate(json.RawMessage(raw), "payments", false)
	if len(errs) == 0 {
		t.Fatal("expected a validation error for a blueprint_calls entry with no blueprint reference, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "blueprint_calls[0].blueprint") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error naming blueprint_calls[0].blueprint, got: %v", errs)
	}
}

// TestParseAndValidate_Override is UBI-86 Part 2's own required hermetic
// proof for the md calling convention's own thin-mapping wire shape --
// mirrors TestParseAndValidate_BlueprintCall exactly: a draft naming
// ONLY an overrides entry, no resources[] at all, is a legitimate,
// complete draft (the target resource already exists elsewhere -- often
// via a blueprint call this same document also makes), and config
// decodes from its own JSON-encoded string form into a real
// map[string]json.RawMessage.
func TestParseAndValidate_Override(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"override the pipeline-events queue's some_hardcoded_field","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"blueprint_calls":[],"overrides":[{"address":"payments.aws_sqs_queue.pipeline-events","config":"{\"some_hardcoded_field\":\"new_value\"}"}]}`
	draft, errs := parseAndValidate(json.RawMessage(raw), "payments", false)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if draft == nil {
		t.Fatal("expected a non-nil draft")
	}
	if len(draft.Resources) != 0 {
		t.Fatalf("expected zero resources[] for an override-only draft, got %+v", draft.Resources)
	}
	if len(draft.Overrides) != 1 {
		t.Fatalf("expected exactly one override, got %d", len(draft.Overrides))
	}
	ov := draft.Overrides[0]
	if ov.Address != "payments.aws_sqs_queue.pipeline-events" {
		t.Errorf("Address = %q, want %q", ov.Address, "payments.aws_sqs_queue.pipeline-events")
	}
	if string(ov.Config["some_hardcoded_field"]) != `"new_value"` {
		t.Fatalf("Config decoded wrong: %+v", ov.Config)
	}
}

// TestParseAndValidate_OverrideMalformedAddress confirms an overrides
// entry with a malformed address is a hard, named validation error --
// never silently dropped or defaulted.
func TestParseAndValidate_OverrideMalformedAddress(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"blueprint_calls":[],"overrides":[{"address":"not-a-valid-address","config":"{\"x\":1}"}]}`
	_, errs := parseAndValidate(json.RawMessage(raw), "payments", false)
	if len(errs) == 0 {
		t.Fatal("expected a validation error for a malformed override address, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "overrides[0].address") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error naming overrides[0].address, got: %v", errs)
	}
}

// TestParseAndValidate_OverrideEmptyConfig confirms an overrides entry
// with an empty config is a hard, named validation error.
func TestParseAndValidate_OverrideEmptyConfig(t *testing.T) {
	raw := `{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x","assumptions":[],"defaults":[],"questions":[]},"resources":[],"destroys":[],"blueprint_calls":[],"overrides":[{"address":"payments.aws_sqs_queue.q","config":""}]}`
	_, errs := parseAndValidate(json.RawMessage(raw), "payments", false)
	if len(errs) == 0 {
		t.Fatal("expected a validation error for an empty override config, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "overrides[0].config") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error naming overrides[0].config, got: %v", errs)
	}
}
