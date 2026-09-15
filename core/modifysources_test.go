package core

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestModificationSources_RoundTrip is the check UBI-281 is actually
// about, and the reason the field was not simply added.
//
// ubx verify recomputes the chain by re-hashing the DESERIALISED
// proposal, so any field the reading structs do not know is silently
// dropped and the chain reports as broken. Creates are immune because
// Delta.Creates is []json.RawMessage and preserves their bytes verbatim.
// Modification is typed and has no such protection.
//
// So the field existing is not the work. Proving it survives the round
// trip is.
func TestModificationSources_RoundTrip(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Kind:          "change",
		Stack:         "s",
		Intent:        Intent{Summary: "x"},
		Delta: Delta{Modifies: []Modification{{
			Target:  Address{Stack: "s", Type: "t", Name: "m"},
			Sources: []IntentSource{{Kind: "blueprint", Ref: "bp:sha256:bbb"}},
		}}},
	}

	before, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly what the ledger does: marshal on write, unmarshal on read,
	// re-hash to check the chain.
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
		t.Fatalf("a modify's sources did not survive the ledger round trip, so every chain containing one would verify as broken:\n  written:    %s\n  recomputed: %s", before, after)
	}
	if got := read.Delta.Modifies[0].Sources; len(got) != 1 || got[0].Ref != "bp:sha256:bbb" {
		t.Fatalf("sources came back as %+v", got)
	}
}

// TestModificationSources_AbsentIsByteIdentical is the backward half:
// every ledger written before this field existed must verify unchanged.
//
// Proven rather than assumed, because "omitempty means it cannot matter"
// is exactly the kind of reasoning that is true until a field is
// declared as a non-pointer struct or a zero-length non-nil slice.
func TestModificationSources_AbsentIsByteIdentical(t *testing.T) {
	// The bytes a pre-UBI-281 ubx would have written: no sources key.
	old := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"intent":{"summary":"x"},` +
		`"delta":{"modifies":[{"target":{"stack":"s","type":"t","name":"m"}}]}}`)

	var p Proposal
	if err := json.Unmarshal(old, &p); err != nil {
		t.Fatal(err)
	}
	back, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(back, []byte(`"sources"`)) {
		t.Fatalf("an absent sources must stay absent, not reappear as null or []:\n%s", back)
	}

	// And the canonical bytes the hash is taken over are unchanged, which
	// is the property that actually keeps an old ledger verifying.
	canon, err := canonicalProposalBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canon, []byte(`"sources"`)) {
		t.Fatalf("canonical content gained a sources key:\n%s", canon)
	}
}

// TestModificationSources_StrictDecodeSeesIt documents the other half of
// the same coin, and is the evidence behind UBI-285.
//
// A binary that does NOT know this field drops it silently. There is no
// version signal to catch that: an additive field does not bump
// SchemaVersion, by the rule the destroys amendment established. A strict
// decode is what names the real condition.
func TestModificationSources_StrictDecodeSeesIt(t *testing.T) {
	withField := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"delta":{"modifies":[{"target":{"stack":"s","type":"t","name":"m"},` +
		`"sources":[{"kind":"blueprint","ref":"bp:sha256:bbb"}]}]}}`)

	// Lenient, as the ledger reads today: accepted, version looks fine.
	var lenient Proposal
	if err := json.Unmarshal(withField, &lenient); err != nil {
		t.Fatal(err)
	}
	if lenient.SchemaVersion != SchemaVersion {
		t.Fatalf("an additive field must not move SchemaVersion, got %d", lenient.SchemaVersion)
	}

	// Strict: this binary knows the field, so it decodes cleanly. A
	// binary without it would fail here with `unknown field "sources"`,
	// which is the signal UBI-285 is about.
	dec := json.NewDecoder(bytes.NewReader(withField))
	dec.DisallowUnknownFields()
	var strict Proposal
	if err := dec.Decode(&strict); err != nil {
		t.Fatalf("this binary knows the field, so a strict decode must pass: %v", err)
	}
}
