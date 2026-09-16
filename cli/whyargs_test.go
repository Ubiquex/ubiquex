package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

func renderArgs(t *testing.T, s core.IntentSource) string {
	t.Helper()
	var buf bytes.Buffer
	writeBlueprintArgs(&buf, "  ", s)
	return buf.String()
}

// TestWriteBlueprintArgs_WithheldIsNotAbsent is the distinction this
// rendering exists to preserve.
//
// "This call passed nothing for api_token" and "this call passed
// something for api_token that ubx will not repeat" are different
// answers to the same question. Collapsing them would make a
// credential's absence look like a parameter nobody set.
func TestWriteBlueprintArgs_WithheldIsNotAbsent(t *testing.T) {
	withheld := renderArgs(t, core.IntentSource{
		DeclaredArgs: map[string]string{"queue_name": "orders"},
		WithheldArgs: []string{"api_token"},
	})
	for _, want := range []string{
		"called with", `queue_name = "orders"`, // what was given
		"api_token", "declared sensitive", "never recorded", // and what was given but not repeated
	} {
		if !strings.Contains(withheld, want) {
			t.Errorf("output does not contain %q:\n%s", want, withheld)
		}
	}

	// A call that simply did not set api_token says nothing about it at
	// all, which is what makes the line above meaningful.
	absent := renderArgs(t, core.IntentSource{DeclaredArgs: map[string]string{"queue_name": "orders"}})
	if strings.Contains(absent, "api_token") {
		t.Errorf("a parameter nobody set was reported as withheld:\n%s", absent)
	}
	if strings.Contains(absent, "sensitive") {
		t.Errorf("nothing was withheld, so nothing should mention sensitivity:\n%s", absent)
	}
}

// TestWriteBlueprintArgs_NamesEachWithheldArgument: a count tells a
// reader something is missing without telling them what, which is the
// position this whole arc exists to get people out of.
func TestWriteBlueprintArgs_NamesEachWithheldArgument(t *testing.T) {
	out := renderArgs(t, core.IntentSource{WithheldArgs: []string{"api_token", "deploy_key"}})
	for _, want := range []string{"api_token", "deploy_key", "were declared sensitive", "their values were never recorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	// And the singular reads correctly, since it is the common case.
	one := renderArgs(t, core.IntentSource{WithheldArgs: []string{"api_token"}})
	for _, want := range []string{"api_token was declared sensitive", "its value was never recorded"} {
		if !strings.Contains(one, want) {
			t.Errorf("singular output does not read correctly, missing %q:\n%s", want, one)
		}
	}
}

// TestWriteBlueprintArgs_StableOrder: the stored arguments are a map,
// which has no order of its own. A listing that reshuffled between runs
// would read as change where nothing changed.
func TestWriteBlueprintArgs_StableOrder(t *testing.T) {
	src := core.IntentSource{DeclaredArgs: map[string]string{
		"zebra": "1", "alpha": "2", "middle": "3", "beta": "4", "yankee": "5",
	}}
	first := renderArgs(t, src)
	for i := 0; i < 20; i++ {
		if got := renderArgs(t, src); got != first {
			t.Fatalf("output is not stable across runs:\n  %s  %s", first, got)
		}
	}
	if !strings.Contains(first, `alpha = "2", beta = "4", middle = "3", yankee = "5", zebra = "1"`) {
		t.Errorf("arguments are not in sorted order:\n%s", first)
	}
}

// TestWriteBlueprintArgs_SilentWhenNothingRecorded covers the case this
// deliberately does not editorialise about.
//
// Both fields empty means either the call passed no arguments or the
// proposal predates UBI-287, and a source carries nothing that tells the
// two apart. Saying "no arguments recorded" would be a guess in one of
// those cases, so it says nothing and lets the declaration line above
// carry the age of the record.
func TestWriteBlueprintArgs_SilentWhenNothingRecorded(t *testing.T) {
	if out := renderArgs(t, core.IntentSource{Kind: "blueprint", Ref: "bp:sha256:a"}); out != "" {
		t.Errorf("a source with no recorded arguments produced:\n%s", out)
	}
}

// TestRenderIntentSource_ShowsArgs goes through the function `ubx why`
// actually calls, rather than the helper the rendering lives in.
//
// Every other test in this file calls writeBlueprintArgs directly, so
// deleting its call site from renderIntentSource would leave them all
// green while the command showed nothing. That exact failure happened in
// this codebase two hours before this test was written, in
// core/sourcefields_test.go, and is recorded on UBI-252.
//
// A rendering that is not wired in is not a rendering, and the wiring is
// one line with no behaviour of its own to attract a test.
func TestRenderIntentSource_ShowsArgs(t *testing.T) {
	var buf bytes.Buffer
	renderIntentSource(&buf, &styler{}, core.IntentSource{
		Kind:           "blueprint",
		Ref:            "ci-platform:sha256:9f2a1c7b4e8d3a6f5b0c9e2d7a4f1b8c3e6d9a2f5b8c1e4d7a0f3b6c9e2d5a8f",
		Declaration:    "oci://ghcr.io/ubx-blueprints/ci-platform:v2",
		DeclaredSource: "oci://ghcr.io/ubx-blueprints/ci-platform:v2",
		DeclaredArgs:   map[string]string{"queue_name": "payments-orders"},
		WithheldArgs:   []string{"api_token"},
	}, "  ")

	out := buf.String()
	for _, want := range []string{
		"blueprint ci-platform",                                   // which bytes
		"declared as oci://ghcr.io/ubx-blueprints/ci-platform:v2", // what asked for them
		`called with queue_name = "payments-orders"`,              // what it was given
		"api_token was declared sensitive",                        // and what it was given that is not repeated
	} {
		if !strings.Contains(out, want) {
			t.Errorf("`ubx why` does not show %q:\n%s", want, out)
		}
	}
}
