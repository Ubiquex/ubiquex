package core

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// swapLedgerWarn captures the advisory and resets the once-guard, so each
// test observes a fresh process.
func swapLedgerWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldW, oldOnce := ledgerFormatWarnWriter, warnedNewerLedger
	ledgerFormatWarnWriter = &buf
	warnedNewerLedger = new(sync.Once)
	t.Cleanup(func() { ledgerFormatWarnWriter, warnedNewerLedger = oldW, oldOnce })
	return &buf
}

// TestWarnIfNewerLedgerFormat_NamesTheField is the whole point: a binary
// older than the ledger it reads must say so, because ubx verify will
// otherwise report the chain as broken and be believed.
func TestWarnIfNewerLedgerFormat_NamesTheField(t *testing.T) {
	buf := swapLedgerWarn(t)

	// What a future ubx might write: valid, well-formed, and carrying a
	// field these structs do not declare.
	newer := []byte(`{"schema_version":2,"kind":"change","stack":"s",` +
		`"delta":{"modifies":[{"target":{"stack":"s","type":"t","name":"m"},` +
		`"some_future_field":"x"}]}}`)

	warnIfNewerLedgerFormat(newer, "abcdef0123456789")

	out := buf.String()
	if out == "" {
		t.Fatal("a ledger this binary cannot fully read must not be read silently")
	}
	for _, want := range []string{
		"newer ubx",             // what happened
		"some_future_field",     // which field, so it is actionable
		"abcdef012345",          // which proposal
		"broken when it is not", // the consequence that matters
		"Upgrade ubx",           // what to do
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the advisory does not say %q:\n%s", want, out)
		}
	}
}

// TestWarnIfNewerLedgerFormat_SilentOnAKnownLedger: every ordinary read
// must stay silent, or the advisory becomes noise and joins the things
// people skip.
func TestWarnIfNewerLedgerFormat_SilentOnAKnownLedger(t *testing.T) {
	buf := swapLedgerWarn(t)

	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Kind:          "change",
		Stack:         "s",
		Intent:        Intent{Summary: "x"},
		Delta: Delta{Modifies: []Modification{{
			Target:   Address{Stack: "s", Type: "t", Name: "m"},
			Provider: &ProviderRef{Source: "hashicorp/aws", Version: "5.0.0"},
			Sources:  []IntentSource{{Kind: "blueprint", Ref: "bp:sha256:aaa"}},
		}}},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	warnIfNewerLedgerFormat(raw, "abcdef0123456789")
	if got := buf.String(); got != "" {
		t.Fatalf("a proposal this binary wrote must read silently, got:\n%s", got)
	}
}

// TestWarnIfNewerLedgerFormat_OncePerProcess: a chain of five hundred
// entries written by a newer ubx would otherwise print five hundred
// advisories, which is its own kind of silence.
func TestWarnIfNewerLedgerFormat_OncePerProcess(t *testing.T) {
	buf := swapLedgerWarn(t)
	newer := []byte(`{"schema_version":2,"kind":"change","stack":"s","some_future_field":1}`)
	for i := 0; i < 50; i++ {
		warnIfNewerLedgerFormat(newer, "abcdef0123456789")
	}
	if n := strings.Count(buf.String(), "newer ubx"); n != 1 {
		t.Fatalf("advisory printed %d times, want exactly 1", n)
	}
}

// TestUnknownFieldPrefix_MatchesTheRealDecoder pins the one fragile part.
//
// encoding/json exports no typed error for a rejected unknown field, so
// the detection is a string prefix. If a future Go rewords it, the check
// silently stops firing, which is exactly the silence it exists to end.
// Asserted against the real decoder rather than against a copy of the
// string, so the toolchain itself is what fails the test.
func TestUnknownFieldPrefix_MatchesTheRealDecoder(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`{"nope":1}`))
	dec.DisallowUnknownFields()
	var p Proposal
	err := dec.Decode(&p)
	if err == nil {
		t.Fatal("a strict decode must reject an undeclared field")
	}
	if !strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		t.Fatalf("this Go reports an unknown field as %q, which no longer starts with %q, so the advisory has stopped firing", err, unknownFieldPrefix)
	}
}
