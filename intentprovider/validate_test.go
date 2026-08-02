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
			wantErr: "no resources and no destroys",
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
