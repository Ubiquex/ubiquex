package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestRenderModifies_ShowsProvenance is the gap a walkthrough found: a
// create explained itself and the very next modify, same blueprint, same
// call, said nothing at all.
//
// The recording was correct throughout. core/resolver populates
// Modification.Sources from a blueprint-expanded modify, verified in
// core/resolver's own TestResolve_ModifyFromABlueprintCallCarriesProvenance.
// This renderer simply was never taught about the field when UBI-281
// added it.
func TestRenderModifies_ShowsProvenance(t *testing.T) {
	var buf bytes.Buffer
	renderModifies(&buf, &styler{}, []core.Modification{{
		Target: core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"},
		Before: map[string]json.RawMessage{"cidr_block": json.RawMessage(`"10.0.0.0/16"`)},
		After:  map[string]json.RawMessage{"cidr_block": json.RawMessage(`"10.1.0.0/16"`)},
		Sources: []core.IntentSource{{
			Kind:           "blueprint",
			Ref:            "network:sha256:1b84872b22d4a6f5b0c9e2d7a4f1b8c3e6d9a2f5b8c1e4d7a0f3b6c9e2d5a8f1",
			Declaration:    "oci://ghcr.io/ubx-blueprints/network:v2",
			DeclaredSource: "oci://ghcr.io/ubx-blueprints/network:v2",
			DeclaredArgs:   map[string]string{"cidr": "10.1.0.0/16"},
			WithheldArgs:   []string{"api_token"},
		}},
	}}, "  ")

	out := buf.String()
	for _, want := range []string{
		"aws_vpc.main change", // the change itself
		"blueprint network",   // which bytes
		"declared as oci://ghcr.io/ubx-blueprints/network:v2", // what asked for them
		`called with cidr = "10.1.0.0/16"`,                    // what it was given
		"api_token was declared sensitive",                    // and what it was given that is not repeated
		"10.1.0.0/16",                                         // and the attribute that moved
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a modify does not show %q:\n%s", want, out)
		}
	}
}

// TestRenderModifies_SilentWithoutSources: a hand-written modify has no
// provenance and must not gain a line saying so, or every ordinary change
// grows an empty section.
func TestRenderModifies_SilentWithoutSources(t *testing.T) {
	var buf bytes.Buffer
	renderModifies(&buf, &styler{}, []core.Modification{{
		Target: core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"},
		After:  map[string]json.RawMessage{"cidr_block": json.RawMessage(`"10.1.0.0/16"`)},
	}}, "  ")
	out := buf.String()
	if strings.Contains(out, "source") || strings.Contains(out, "blueprint") {
		t.Errorf("a hand-written modify gained a provenance line:\n%s", out)
	}
	if !strings.Contains(out, "aws_vpc.main change") {
		t.Errorf("the change itself no longer renders:\n%s", out)
	}
}

// TestRenderCreatesAndModifies_AgreeOnProvenance pins the asymmetry that
// caused this, rather than only its symptom.
//
// The two renderers sit next to each other and only one looped over
// sources. Nothing in the code showed that, and nothing failed. A reader
// comparing them was the only detector, which is why the same blueprint
// could explain a create and say nothing about the modify right under it.
func TestRenderCreatesAndModifies_AgreeOnProvenance(t *testing.T) {
	src := core.IntentSource{Kind: "blueprint", Ref: "network:sha256:abc123", Declaration: "oci://ghcr.io/x/network:v2"}

	node, err := json.Marshal(map[string]interface{}{
		"stack": "payments", "type": "aws_vpc", "name": "main",
		"config":  map[string]interface{}{"cidr_block": "10.0.0.0/16"},
		"sources": []core.IntentSource{src},
	})
	if err != nil {
		t.Fatal(err)
	}

	var creates, modifies bytes.Buffer
	renderCreates(&creates, &styler{}, []json.RawMessage{node}, "  ")
	renderModifies(&modifies, &styler{}, []core.Modification{{
		Target:  core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"},
		After:   map[string]json.RawMessage{"cidr_block": json.RawMessage(`"10.1.0.0/16"`)},
		Sources: []core.IntentSource{src},
	}}, "  ")

	for _, want := range []string{"blueprint network", "declared as oci://ghcr.io/x/network:v2"} {
		if !strings.Contains(creates.String(), want) {
			t.Errorf("creates no longer show %q:\n%s", want, creates.String())
		}
		if !strings.Contains(modifies.String(), want) {
			t.Errorf("modifies do not show %q, so the two renderers disagree again:\n%s", want, modifies.String())
		}
	}
}
