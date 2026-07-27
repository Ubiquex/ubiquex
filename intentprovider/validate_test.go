package intentprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAndValidate_Valid(t *testing.T) {
	draft, errs := parseAndValidate(json.RawMessage(validPaymentsDraft), "payments")
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft, errs := parseAndValidate(json.RawMessage(tt.raw), "payments")
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
