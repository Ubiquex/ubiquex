package core

import (
	"bytes"
	"encoding/json"
	"testing"
)

func declaredBlueprint() IntentSource {
	return IntentSource{
		Kind:           "blueprint",
		Ref:            "ci-platform:sha256:abc123",
		Declaration:    "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci",
		DeclaredSource: "https://github.com/ubiquex/bps.git",
		DeclaredRev:    "v2.1.0",
		DeclaredPath:   "ci",
	}
}

// TestDeclaration_RoundTripOnEveryTypedHolder is the check UBI-282's own
// cost analysis originally said was unnecessary.
//
// It reasoned that creates are []json.RawMessage and preserve their bytes
// verbatim, so a field added inside sources could not break a chain. That
// was true when only creates carried sources. Modification.Sources
// (UBI-281) and DestroyEntry.Sources (UBI-284) are typed and are not
// covered by that argument, so each is checked here rather than assumed.
func TestDeclaration_RoundTripOnEveryTypedHolder(t *testing.T) {
	cases := []struct {
		name  string
		delta Delta
		read  func(*Proposal) []IntentSource
	}{
		{
			name:  "create, opaque bytes",
			delta: Delta{Creates: []json.RawMessage{mustCreateNode(t)}},
			read: func(p *Proposal) []IntentSource {
				var node struct {
					Sources []IntentSource `json:"sources"`
				}
				if err := json.Unmarshal(p.Delta.Creates[0], &node); err != nil {
					t.Fatal(err)
				}
				return node.Sources
			},
		},
		{
			name: "modify, typed",
			delta: Delta{Modifies: []Modification{{
				Target:  Address{Stack: "s", Type: "t", Name: "m"},
				Sources: []IntentSource{declaredBlueprint()},
			}}},
			read: func(p *Proposal) []IntentSource { return p.Delta.Modifies[0].Sources },
		},
		{
			name: "destroy, typed",
			delta: Delta{Destroys: []DestroyEntry{{
				Address: Address{Stack: "s", Type: "t", Name: "d"},
				State:   json.RawMessage(`{"id":"d-1"}`),
				Sources: []IntentSource{declaredBlueprint()},
			}}},
			read: func(p *Proposal) []IntentSource { return p.Delta.Destroys[0].Sources },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &Proposal{
				SchemaVersion: SchemaVersion,
				Kind:          "change",
				Stack:         "s",
				Intent:        Intent{Summary: "x"},
				Delta:         c.delta,
			}
			before, err := Hash(p)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var read Proposal
			if err := json.Unmarshal(stored, &read); err != nil {
				t.Fatal(err)
			}
			after, err := Hash(&read)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("a declaration did not survive the ledger round trip here, so every chain containing one would verify as broken:\n  written:    %s\n  recomputed: %s", before, after)
			}
			got := c.read(&read)
			if len(got) != 1 {
				t.Fatalf("sources came back as %+v", got)
			}
			if got[0].Declaration != declaredBlueprint().Declaration ||
				got[0].DeclaredSource != declaredBlueprint().DeclaredSource ||
				got[0].DeclaredRev != declaredBlueprint().DeclaredRev ||
				got[0].DeclaredPath != declaredBlueprint().DeclaredPath {
				t.Fatalf("declaration came back as %+v", got[0])
			}
		})
	}
}

func mustCreateNode(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"stack": "s", "type": "t", "name": "c",
		"config":  map[string]interface{}{},
		"sources": []IntentSource{declaredBlueprint()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDeclaration_AbsentIsByteIdentical is the backward half: every
// proposal written before these fields existed, and every non-blueprint
// source now, must produce identical canonical bytes.
//
// This is what keeps the claim "nothing already stored is revalued" true,
// and it is the only half of UBI-282's original cost analysis that
// survived contact with the typed holders above.
func TestDeclaration_AbsentIsByteIdentical(t *testing.T) {
	old := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"intent":{"summary":"x","sources":[{"kind":"dialogue","ref":"d-1"}]},` +
		`"delta":{"modifies":[{"target":{"stack":"s","type":"t","name":"m"},` +
		`"sources":[{"kind":"blueprint","ref":"ci:sha256:aaa"}]}]}}`)

	var p Proposal
	if err := json.Unmarshal(old, &p); err != nil {
		t.Fatal(err)
	}
	canon, err := canonicalProposalBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"declaration"`, `"declared_source"`, `"declared_rev"`, `"declared_path"`} {
		if bytes.Contains(canon, []byte(key)) {
			t.Fatalf("canonical content gained %s where nothing declared one:\n%s", key, canon)
		}
	}
}

// TestDeclaration_StrictDecodeSeesIt is the evidence behind the
// correction to UBI-282's cost analysis: an older binary drops these and
// mis-hashes, and UBI-285's advisory is what makes that audible.
func TestDeclaration_StrictDecodeSeesIt(t *testing.T) {
	withField := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"delta":{"modifies":[{"target":{"stack":"s","type":"t","name":"m"},` +
		`"sources":[{"kind":"blueprint","ref":"ci:sha256:aaa",` +
		`"declaration":"oci://ghcr.io/x/y:v1"}]}]}}`)

	var lenient Proposal
	if err := json.Unmarshal(withField, &lenient); err != nil {
		t.Fatal(err)
	}
	if lenient.SchemaVersion != SchemaVersion {
		t.Fatalf("an additive field must not move SchemaVersion, got %d", lenient.SchemaVersion)
	}

	dec := json.NewDecoder(bytes.NewReader(withField))
	dec.DisallowUnknownFields()
	var strict Proposal
	if err := dec.Decode(&strict); err != nil {
		t.Fatalf("this binary knows the field, so a strict decode must pass: %v", err)
	}
}
