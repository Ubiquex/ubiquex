package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validateWithSources(t *testing.T, s IntentSource) error {
	t.Helper()
	return validateSourceFields(&Proposal{Intent: Intent{Sources: []IntentSource{s}}})
}

// TestValidateSourceFields_RefusesAWrongKindField is the rule's whole
// purpose: IntentSource is a union by convention, and until this nothing
// checked the convention at all.
func TestValidateSourceFields_RefusesAWrongKindField(t *testing.T) {
	cases := []struct {
		name   string
		source IntentSource
		want   []string
	}{
		{
			name:   "a cloudtrail field on a blueprint source",
			source: IntentSource{Kind: "blueprint", Ref: "bp:sha256:a", ActorARN: "arn:aws:iam::1:user/x"},
			want:   []string{"actor_arn", "blueprint", "declaration"},
		},
		{
			name:   "a blueprint field on a promotion source",
			source: IntentSource{Kind: "promotion", Ref: "p1", Declaration: "oci://x/y:v1"},
			want:   []string{"declaration", "promotion", "base"},
		},
		{
			name:   "args on a restore source, which carries no kind-specific fields",
			source: IntentSource{Kind: "restore", Ref: "head-abc", DeclaredArgs: map[string]string{"a": "b"}},
			want:   []string{"declared_args", "restore", "no kind-specific fields at all"},
		},
		{
			name:   "withheld args without a blueprint kind",
			source: IntentSource{Kind: "dialogue", Ref: "d-1", WithheldArgs: []string{"api_token"}},
			want:   []string{"withheld_args", "dialogue"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateWithSources(t, c.source)
			if err == nil {
				t.Fatal("a field the kind does not use must be refused")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the message does not mention %q: %v", w, err)
				}
			}
		})
	}
}

// TestValidateSourceFields_AcceptsEveryRealShape: the rule has to accept
// what the system actually produces, or it is a rule against the
// codebase rather than against mistakes.
func TestValidateSourceFields_AcceptsEveryRealShape(t *testing.T) {
	for _, s := range []IntentSource{
		{Kind: "dialogue", Ref: "d-1"},
		{Kind: "document", Ref: "doc.md", ContentHash: "sha256:a"},
		{Kind: "promotion", Ref: "p1", Base: "staging"},
		{Kind: "restore", Ref: "head-abc"},
		{Kind: "blueprint", Ref: "bp:sha256:a", Declaration: "oci://x/y:v1", DeclaredSource: "oci://x/y:v1",
			DeclaredArgs: map[string]string{"queue_name": "orders"}, WithheldArgs: []string{"api_token"}},
		{Kind: "cloudtrail", Ref: "e1", EventID: "e1", EventName: "TagResource", ActorARN: "arn:x", SourceIP: "1.2.3.4",
			SessionContext: json.RawMessage(`{}`)},
		{Kind: "gcp_audit", Ref: "e2", EventName: "x", ActorARN: "me@example.com"},
		{Kind: "audit_unattributed", Reason: ReasonNotLogged, Backend: "aws"},
		{Kind: "cloudtrail_unattributed", Reason: ReasonNoMatchingEvent, Backend: "aws"},
	} {
		if err := validateWithSources(t, s); err != nil {
			t.Errorf("kind %q is a real shape this system produces and was refused: %v", s.Kind, err)
		}
	}
}

// TestValidateSourceFields_IncompleteIsNotMalformed: attribution
// legitimately produces partial records, so the rule refuses only the
// reverse of what it might look like it checks.
func TestValidateSourceFields_IncompleteIsNotMalformed(t *testing.T) {
	if err := validateWithSources(t, IntentSource{Kind: "cloudtrail", Ref: "e1"}); err != nil {
		t.Errorf("a cloudtrail source with no event fields is incomplete, not malformed: %v", err)
	}
	if err := validateWithSources(t, IntentSource{Kind: "blueprint", Ref: "bp:sha256:a"}); err != nil {
		t.Errorf("a pre-UBI-282 blueprint source carries only a ref and must stay valid: %v", err)
	}
}

// TestValidateSourceFields_CoversModifiesAndDestroys: sources live on
// three holders, and a rule covering one of them would be a rule with
// two holes in it.
func TestValidateSourceFields_CoversModifiesAndDestroys(t *testing.T) {
	bad := IntentSource{Kind: "blueprint", Ref: "bp:sha256:a", Base: "staging"}

	if err := validateSourceFields(&Proposal{Delta: Delta{Modifies: []Modification{{Sources: []IntentSource{bad}}}}}); err == nil {
		t.Error("a modify's sources are not checked")
	} else if !strings.Contains(err.Error(), "delta.modifies[0]") {
		t.Errorf("the message does not locate the source: %v", err)
	}

	if err := validateSourceFields(&Proposal{Delta: Delta{Destroys: []DestroyEntry{{Sources: []IntentSource{bad}}}}}); err == nil {
		t.Error("a destroy's sources are not checked")
	} else if !strings.Contains(err.Error(), "delta.destroys[0]") {
		t.Errorf("the message does not locate the source: %v", err)
	}
}

// TestSourceFieldsByKind_CoversEveryKindThatHasFields guards the list
// against the type growing past it.
//
// The failure this prevents is silent: a new kind-specific field added
// without a matching entry here is simply never allowed on any kind, so
// every proposal carrying it is refused. That would be found, loudly, by
// whoever added it. The worse direction is a new KIND added without an
// entry, which is silently allowed to carry nothing, and that is what
// this checks.
func TestSourceFieldsByKind_CoversEveryKindThatHasFields(t *testing.T) {
	// Every kind this codebase produces, from IntentSource's own doc
	// comment. Listed here rather than derived, because deriving it from
	// the map under test would compare the map with itself.
	known := []string{
		"dialogue", "manual_edit", "issue", "cloudtrail", "cloudtrail_unattributed",
		"gcp_audit", "audit_unattributed", "document", "intent_provider",
		"promotion", "restore", "blueprint",
	}
	for _, k := range known {
		fields, listed := sourceFieldsByKind[k]
		if listed && len(fields) == 0 {
			t.Errorf("kind %q is listed with no fields, which is what leaving it out already means", k)
		}
	}
	// And the reverse: nothing in the map that is not a real kind.
	isKnown := map[string]bool{}
	for _, k := range known {
		isKnown[k] = true
	}
	for k := range sourceFieldsByKind {
		if !isKnown[k] {
			t.Errorf("sourceFieldsByKind names %q, which is not a kind this system produces", k)
		}
	}
}

// TestValidate_RejectsAWrongKindField calls the real Validate rather
// than the rule directly.
//
// Every other test in this file starts one level too low: they call
// validateSourceFields, so removing its call site from Validate leaves
// them all green while nothing in the system checks anything. Found by
// reverting exactly that and watching the suite pass.
//
// A rule that is not wired in is not a rule, and the wiring is the part
// a future refactor is most likely to drop.
func TestValidate_RejectsAWrongKindField(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Kind:          KindChange,
		Stack:         "payments",
		Intent: Intent{
			Summary: "x",
			Sources: []IntentSource{{Kind: "blueprint", Ref: "bp:sha256:a", ActorARN: "arn:aws:iam::1:user/x"}},
		},
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted a blueprint source carrying a cloudtrail field")
	}
	if !errors.Is(err, ErrInvalidProposal) {
		t.Errorf("error is not an ErrInvalidProposal: %v", err)
	}
	if !strings.Contains(err.Error(), "actor_arn") {
		t.Errorf("Validate's message does not name the offending field: %v", err)
	}

	// And the same proposal without the stray field validates, so the
	// test is about the field rather than about the rest of the shape.
	p.Intent.Sources[0].ActorARN = ""
	if err := Validate(p); err != nil {
		t.Fatalf("the same proposal without the stray field must validate: %v", err)
	}
}
