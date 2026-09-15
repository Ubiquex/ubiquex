package core

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestDestroyEntrySources_RoundTrip is the same check UBI-281 ran for the
// modify half, for the same reason: DestroyEntry is typed, so unlike a
// create it has no json.RawMessage protecting its bytes. A field that does
// not survive marshal/unmarshal/re-hash makes every chain containing one
// verify as broken.
func TestDestroyEntrySources_RoundTrip(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Kind:          "change",
		Stack:         "s",
		Intent:        Intent{Summary: "x"},
		Delta: Delta{Destroys: []DestroyEntry{{
			Address: Address{Stack: "s", Type: "t", Name: "d"},
			State:   json.RawMessage(`{"id":"d-1"}`),
			Sources: []IntentSource{{Kind: "blueprint", Ref: "queue:sha256:ccc"}},
		}}},
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
		t.Fatalf("a destroy's sources did not survive the ledger round trip, so every chain containing one would verify as broken:\n  written:    %s\n  recomputed: %s", before, after)
	}
	if got := read.Delta.Destroys[0].Sources; len(got) != 1 || got[0].Ref != "queue:sha256:ccc" {
		t.Fatalf("sources came back as %+v", got)
	}
}

// TestDestroyEntrySources_AbsentIsByteIdentical is the backward half: every
// ledger written before this field existed must verify unchanged, and the
// canonical bytes the hash is taken over must not gain a key.
func TestDestroyEntrySources_AbsentIsByteIdentical(t *testing.T) {
	old := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"intent":{"summary":"x"},` +
		`"delta":{"destroys":[{"address":{"stack":"s","type":"t","name":"d"},` +
		`"state":{"id":"d-1"}}]}}`)

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
	canon, err := canonicalProposalBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canon, []byte(`"sources"`)) {
		t.Fatalf("canonical content gained a sources key:\n%s", canon)
	}
}

// TestDestroyEntrySources_SortKeyUnaffected guards the one thing a destroy
// has that a modify does not: deltaSortKey reads DestroyEntry's fields
// directly to order the array before hashing (canonical.go). Adding a field
// it does not read must not move anything, or two proposals differing only
// in provenance would canonicalize into different orders.
func TestDestroyEntrySources_SortKeyUnaffected(t *testing.T) {
	mk := func(sources []IntentSource) *Proposal {
		return &Proposal{
			SchemaVersion: SchemaVersion,
			Kind:          "change",
			Stack:         "s",
			Intent:        Intent{Summary: "x"},
			Delta: Delta{Destroys: []DestroyEntry{
				{Address: Address{Stack: "s", Type: "t", Name: "b"}, State: json.RawMessage(`{}`), Sources: sources},
				{Address: Address{Stack: "s", Type: "t", Name: "a"}, State: json.RawMessage(`{}`)},
			}},
		}
	}
	withProv, err := canonicalProposalBytes(mk([]IntentSource{{Kind: "blueprint", Ref: "q:sha256:1"}}))
	if err != nil {
		t.Fatal(err)
	}
	without, err := canonicalProposalBytes(mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	// Same ordering: "a" before "b" in both, provenance or not.
	posA, posB := bytes.Index(withProv, []byte(`"name":"a"`)), bytes.Index(withProv, []byte(`"name":"b"`))
	if posA < 0 || posB < 0 || posA > posB {
		t.Fatalf("destroys are no longer address-ordered:\n%s", withProv)
	}
	if bytes.Index(without, []byte(`"name":"a"`)) > bytes.Index(without, []byte(`"name":"b"`)) {
		t.Fatalf("destroys are no longer address-ordered without provenance:\n%s", without)
	}
}
