package blueprint

import (
	"context"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestBlueprintSource_DeclaredVsDirect pins the distinction UBI-282 turns
// on: Declaration means "the blueprints table said exactly this", and a
// call that names its source inline has no table entry to quote.
//
// Echoing call.Blueprint into Declaration for a direct call would be the
// easy mistake, and it would make any future table reconciliation believe
// an entry existed where none ever did.
func TestBlueprintSource_DeclaredVsDirect(t *testing.T) {
	const ref = "ci-platform:sha256:abc123"

	t.Run("declared in the blueprints table", func(t *testing.T) {
		got := blueprintSource(ref, resolver.BlueprintCall{Name: "c"}, ResolvedDep{
			Dep: Declaration{
				Name:   "ci-platform",
				URL:    "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci",
				Source: "https://github.com/ubiquex/bps.git",
				Ref:    "v2.1.0",
				Path:   "ci",
			},
		})
		if got.Ref != ref {
			t.Errorf("Ref = %q, want the content-hash ref untouched", got.Ref)
		}
		if got.Declaration != "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci" {
			t.Errorf("Declaration = %q, want the table's own entry verbatim", got.Declaration)
		}
		if got.DeclaredSource != "https://github.com/ubiquex/bps.git" || got.DeclaredRev != "v2.1.0" || got.DeclaredPath != "ci" {
			t.Errorf("derived triple = %q / %q / %q, want how Pull parsed the declaration",
				got.DeclaredSource, got.DeclaredRev, got.DeclaredPath)
		}
	})

	t.Run("called directly, no table entry", func(t *testing.T) {
		got := blueprintSource(ref, resolver.BlueprintCall{
			Name:      "c",
			Blueprint: "./blueprints/ci-platform",
			Ref:       "main",
			Path:      "sub",
		}, ResolvedDep{})
		if got.Declaration != "" {
			t.Errorf("Declaration = %q, want empty: no blueprints table entry exists to quote", got.Declaration)
		}
		if got.DeclaredSource != "./blueprints/ci-platform" || got.DeclaredRev != "main" || got.DeclaredPath != "sub" {
			t.Errorf("derived triple = %q / %q / %q, want the call's own three fields",
				got.DeclaredSource, got.DeclaredRev, got.DeclaredPath)
		}
	})

	t.Run("oci embeds its own tag, so rev and path stay empty", func(t *testing.T) {
		got := blueprintSource(ref, resolver.BlueprintCall{Name: "c"}, ResolvedDep{
			Dep: Declaration{
				Name:   "ts-bp",
				URL:    "oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0",
				Source: "oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0",
			},
		})
		if got.DeclaredRev != "" || got.DeclaredPath != "" {
			t.Errorf("rev/path = %q / %q, want both empty for an oci reference", got.DeclaredRev, got.DeclaredPath)
		}
		if got.Declaration != "oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0" {
			t.Errorf("Declaration = %q", got.Declaration)
		}
	})
}

// TestExpandCalls_DeclarationStamped is the end-to-end half: the unit
// test above checks the shape in isolation, this runs a real blueprint
// through the real pipeline and confirms the declaration reaches the
// resource a proposal is built from.
//
// A direct call, which is the shape writeCallableBlueprint produces: no
// blueprints table is involved, so Declaration must stay empty while the
// source the author actually wrote is recorded.
func TestExpandCalls_DeclarationStamped(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if _, err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	sources := intent.Resources[0].Sources
	if len(sources) != 1 {
		t.Fatalf("expected exactly 1 source, got %+v", sources)
	}
	got := sources[0]
	if got.DeclaredSource != dir {
		t.Errorf("DeclaredSource = %q, want the source the call actually named, %q", got.DeclaredSource, dir)
	}
	if got.Declaration != "" {
		t.Errorf("Declaration = %q, want empty: this call named its source inline, no blueprints table entry exists", got.Declaration)
	}
	// The ref is untouched by any of this: which bytes and what asked for
	// them are two separate answers, and UBI-282 adds the second without
	// disturbing the first.
	if !strings.HasPrefix(got.Ref, "ci-platform:sha256:") {
		t.Errorf("Ref = %q, want the content-hash ref unchanged", got.Ref)
	}
}
