package blueprint

import (
	"context"
	"encoding/json"
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
		}, nil)
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
		}, ResolvedDep{}, nil)
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
		}, nil)
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

// TestSplitCallArgs_WithholdsOnlyWhatTheBlueprintDeclares is the rule
// that decides what reaches the ledger.
//
// Sensitivity comes from the blueprint's declared params, which is the
// only authority for it: a caller does not know which of the values they
// passed is a credential, and the blueprint author does.
func TestSplitCallArgs_WithholdsOnlyWhatTheBlueprintDeclares(t *testing.T) {
	params := []Param{
		{Name: "queue_name", Type: ParamString, Required: true},
		{Name: "api_token", Type: ParamString, Required: true, Sensitive: true},
		{Name: "deploy_key", Type: ParamString, Required: true, Sensitive: true},
	}
	args := map[string]string{
		"queue_name": "payments-orders",
		"api_token":  "ghp_realcredential",
		"deploy_key": "-----BEGIN KEY-----",
	}

	recorded, withheld := splitCallArgs(args, params)

	if got := recorded["queue_name"]; got != "payments-orders" {
		t.Errorf("an ordinary argument was not recorded: %q", got)
	}
	for _, secret := range []string{"api_token", "deploy_key"} {
		if v, present := recorded[secret]; present {
			t.Errorf("%s reached the ledger as %q: this is permanent signed content and cannot be edited afterwards", secret, v)
		}
	}
	// Sorted, because this is hashed content and map iteration is not.
	if len(withheld) != 2 || withheld[0] != "api_token" || withheld[1] != "deploy_key" {
		t.Errorf("withheld = %v, want the two sensitive names in sorted order", withheld)
	}
}

// TestSplitCallArgs_NoValueAppearsAnywhere is the assertion that matters
// most, so it looks at the whole serialised source rather than at the
// fields it expects to be wrong.
//
// Checking that recorded["api_token"] is absent only proves the key I
// thought of is absent. This proves the secret is not in the bytes at
// all, which is the actual requirement.
func TestSplitCallArgs_NoValueAppearsAnywhere(t *testing.T) {
	const secret = "ghp_averyrecognisablecredential"
	s := blueprintSource("bp:sha256:a",
		resolver.BlueprintCall{Name: "c", Blueprint: "./bp", Args: map[string]string{
			"queue_name": "orders",
			"api_token":  secret,
		}},
		ResolvedDep{},
		[]Param{
			{Name: "queue_name", Type: ParamString, Required: true},
			{Name: "api_token", Type: ParamString, Required: true, Sensitive: true},
		})

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the credential is in the serialised source:\n%s", raw)
	}
	if !strings.Contains(string(raw), "withheld_args") || !strings.Contains(string(raw), "api_token") {
		t.Errorf("the fact that an argument was withheld is not recorded:\n%s", raw)
	}
	if !strings.Contains(string(raw), "orders") {
		t.Errorf("an ordinary argument was lost:\n%s", raw)
	}
}

// TestSplitCallArgs_NoArgsRecordsNothing: a blueprint taking no
// parameters must not gain an empty map in hashed content.
func TestSplitCallArgs_NoArgsRecordsNothing(t *testing.T) {
	recorded, withheld := splitCallArgs(nil, nil)
	if recorded != nil || withheld != nil {
		t.Errorf("recorded=%v withheld=%v, want both nil", recorded, withheld)
	}
	raw, err := json.Marshal(blueprintSource("bp:sha256:a", resolver.BlueprintCall{Name: "c"}, ResolvedDep{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"declared_args", "withheld_args"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("a call with no arguments gained %s:\n%s", key, raw)
		}
	}
}
