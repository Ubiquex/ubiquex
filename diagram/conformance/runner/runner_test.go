// Package runner is docs/diagram-medium.md's own conformance suite
// (UBI-47 slice 5), mirroring sdk/conformance/runner's own golden-shape
// discipline exactly: evaluate the real, unmodified producer, canonicalize,
// byte-compare against golden/ -- a real, ongoing regression test, not a
// one-time check. "payments" is fixture #1, same name every other
// medium's own conformance fixture already uses.
//
// This package covers the PARSE direction only (golden/payments.d2 ->
// diagram.Parse -> Topology, compared against golden/payments-topology.json)
// -- fully self-contained, no subprocess, no real provider binary needed
// at all, since diagram.Parse's own type inference only ever calls
// SchemaInspector.HasType (never IsComputed/IsSensitive -- those matter
// only once a resource's own config VALUES get resolved, and a diagram
// never authors any). The RENDER direction (a real ledger, built by
// shipping the identical topology through the hermetic fakeprovider
// binary, emitted and compared against golden/payments-rendered.d2) lives
// in cli/render_conformance_test.go instead -- a deliberate package
// split, not an oversight: Emit needs a REAL, shipped Fleet entry
// (FoldState only reports "live" after a real apply), which needs the
// full core/executor.Applier adapter (Schema/Configure/ReadResource/
// PlanResourceChange/ApplyResourceChange, with real redaction and
// diagnostic-to-TerminalError classification) that already exists,
// correct and tested, in cli/stateadapter.go -- reimplementing an
// independent copy of that adapter here purely to keep both directions
// under one roof would risk a real, silent divergence from the one
// implementation every other ship path already relies on, for no benefit
// over importing the fixtures by relative path instead (both directions
// read/write the SAME golden/ directory regardless of which package
// drives them).
package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/diagram"
)

// fakeSchema is a hermetic resolver.SchemaInspector declaring only
// "fake_widget" -- the one type the golden fixtures use throughout (a
// deliberate, documented departure from every other medium's own
// "payments" fixture, which uses aws_vpc/aws_db_instance: this session's
// own explicit instruction is "hermetic only -- no real cloud this
// slice," and a real hashicorp/aws schema fetch, however read-only and
// safe in isolation, is exactly the kind of real-provider touch that led
// to UBI-47 session 4's own real AWS incident when the NEXT step in a
// session happened to go further than intended. fake_widget is real code
// this repository already owns (provider/internal/fakeprovider), not a
// guessed-at schema shape, and lets the render direction's own fixture
// (cli/render_conformance_test.go) describe the exact same topology
// through a real, shippable apply -- reconciling this fixture's own
// values with the aws_* golden values every other medium already
// converged on is slice 6's own job (the "live finale"), not this one's.
type fakeSchema struct{ types map[string]bool }

func (f *fakeSchema) HasType(t string) bool           { return f.types[t] }
func (f *fakeSchema) IsComputed(t, path string) bool  { return false }
func (f *fakeSchema) IsSensitive(t, path string) bool { return false }

func fakeWidgetProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "fake/widget",
		Version: "0.1.0",
		Schema:  &fakeSchema{types: map[string]bool{"fake_widget": true}},
	}
}

// TestPaymentsGoldenCase_Parse is the suite's own first case (docs/
// diagram-medium.md's slice 5): golden/payments.d2, parsed for real
// through the real, unmodified diagram.Parse, its own topology
// (diagram.Topology, UBI-47 slice 5's own first real code for the
// "topology hash" concept docs/diagram-medium.md already named) byte-
// compared against golden/payments-topology.json after canonicalization
// on both sides (the committed fixture is pretty-printed for
// reviewability, never assumed to already be in canonical form itself --
// the same posture sdk/conformance's own golden/payments.json holds).
//
// This test's own passing is the ongoing, automated half of "a diagram's
// own topology extraction stays correct": if a future change to Parse,
// Topology, or the D2 library itself ever drifts from the golden shape,
// this test catches it immediately, not just at whichever session
// happens to check by hand.
func TestPaymentsGoldenCase_Parse(t *testing.T) {
	d2Path := filepath.Join("..", "golden", "payments.d2")
	f, err := os.Open(d2Path)
	if err != nil {
		t.Fatalf("open %s: %v", d2Path, err)
	}
	defer f.Close()

	intent, err := diagram.Parse("payments.d2", f, "payments", []resolver.DeclaredProvider{fakeWidgetProvider()}, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse(%s): %v", d2Path, err)
	}
	if len(intent.Intent.Questions) != 0 {
		t.Fatalf("golden/payments.d2 produced blocking ambiguity, want none: %+v", intent.Intent.Questions)
	}

	got, err := diagram.Topology(intent)
	if err != nil {
		t.Fatalf("diagram.Topology: %v", err)
	}

	topoPath := filepath.Join("..", "golden", "payments-topology.json")
	goldenRaw, err := os.ReadFile(topoPath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", topoPath, err)
	}
	want, err := core.CanonicalJSONBytes(goldenRaw)
	if err != nil {
		t.Fatalf("canonicalize golden fixture: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("parsed topology does not match the golden fixture, byte for byte after canonicalization:\ngot:  %s\nwant: %s", got, want)
	}
}

// TestPaymentsGoldenCase_Parse_Deterministic guards the same property
// diagram/emit_test.go's own determinism tests already check for Emit --
// parsing the identical golden diagram twice must produce byte-identical
// topology, not merely "usually."
func TestPaymentsGoldenCase_Parse_Deterministic(t *testing.T) {
	d2Path := filepath.Join("..", "golden", "payments.d2")
	raw, err := os.ReadFile(d2Path)
	if err != nil {
		t.Fatalf("read %s: %v", d2Path, err)
	}

	var first string
	for i := 0; i < 3; i++ {
		intent, err := diagram.Parse("payments.d2", strings.NewReader(string(raw)), "payments", []resolver.DeclaredProvider{fakeWidgetProvider()}, diagram.Options{})
		if err != nil {
			t.Fatalf("diagram.Parse: %v", err)
		}
		got, err := diagram.Topology(intent)
		if err != nil {
			t.Fatalf("diagram.Topology: %v", err)
		}
		if i == 0 {
			first = string(got)
			continue
		}
		if string(got) != first {
			t.Fatalf("Topology changed across repeated parses of the identical golden diagram")
		}
	}
}
