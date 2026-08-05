package diagram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestParse_UbxBlueprint_CallNameIsBareID confirms UBI-128's own implicit
// naming rule: a ubx_blueprint node's own CallName (resolver.BlueprintCall.
// CallName) is always its bare D2 identifier, the SAME identifier a ref:
// sigil already resolves an ordinary sibling resource by (never a second,
// separately-authored naming scheme) -- set unconditionally, whether or
// not this call's own outputs ever end up referenced.
func TestParse_UbxBlueprint_CallNameIsBareID(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if got := intent.BlueprintCalls[0].CallName; got != "platform" {
		t.Errorf("CallName = %q, want %q (the node's own bare D2 identifier)", got, "platform")
	}
}

// TestParse_UbxRequired_RefSigil_ResolvesBlueprintCallOutput is UBI-128's
// own live-verified worked example, hermetically reproduced: a
// ubx_required attribute value of "ref:<blueprint-node>.<outputKey>"
// resolves into a PROVISIONAL resolver.BlueprintOutputRefPrefix marker --
// the SAME "ref:" sigil and $ref wire shape UBI-95 already established
// for an ordinary sibling-resource reference, reusing resolveRefTarget's
// own identifier resolution unchanged, never a second reference
// mechanism. blueprint.ExpandCalls (blueprint/outputs.go) is the only
// place this marker is ever rewritten into a real resolved address --
// this package has no opinion on whether "repo_arn" is actually a real
// declared output of the "../ci-platform" blueprint at all (it has no
// access to that blueprint's own Ubxfile, by design).
func TestParse_UbxRequired_RefSigil_ResolvesBlueprintCallOutput(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
}
role: "ci-role" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
  ubx_required.assume_role_policy: "ref:platform.repo_arn"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})
	ri := resourceByName(t, intent, "ci-role")

	var cfg map[string]any
	if err := json.Unmarshal(ri.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	refObj, ok := cfg["assume_role_policy"].(map[string]any)
	if !ok {
		t.Fatalf("Config[assume_role_policy] = %v, want a $ref object", cfg["assume_role_policy"])
	}
	inner, ok := refObj["$ref"].(map[string]any)
	if !ok {
		t.Fatalf("assume_role_policy = %v, want a real {\"$ref\":...} object", refObj)
	}
	want := "$blueprint_output:platform:repo_arn"
	if got := inner["to"]; got != want {
		t.Fatalf("assume_role_policy.$ref.to = %v, want %q (the provisional output marker, resolver.BlueprintOutputRefPrefix + CallName + \":\" + outputKey)", got, want)
	}
	if got, want := inner["to"].(string), resolver.BlueprintOutputRefPrefix; !strings.HasPrefix(got, want) {
		t.Fatalf("assume_role_policy.$ref.to = %q, want it to start with resolver.BlueprintOutputRefPrefix %q", got, want)
	}
}

// TestParse_UbxOverride_RefSigil_ResolvesBlueprintCallOutput confirms
// ubx_override's own config values resolve a blueprint-call ref: exactly
// like ubx_required does -- both share ubxRequiredAttrValue/
// resolveRefTarget unchanged, so this comes for free, never a third
// reference mechanism.
func TestParse_UbxOverride_RefSigil_ResolvesBlueprintCallOutput(t *testing.T) {
	src := `
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
}
fix: "fix downstream" {
  class: ubx_override
  address: "payments.aws_iam_role_policy.downstream"
  target_arn: "ref:platform.repo_arn"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	ov := intent.Overrides[0]
	if !strings.Contains(string(ov.Config["target_arn"]), `"$blueprint_output:platform:repo_arn"`) {
		t.Fatalf("Config[target_arn] = %s, want a $ref to $blueprint_output:platform:repo_arn", ov.Config["target_arn"])
	}
}

// TestParse_UbxRequired_RefSigil_BlueprintCallNestedInContainer confirms
// a blueprint-call ref: resolves via byBareID exactly like an ordinary
// resource ref: already does (parse_test.go's own established coverage)
// when the ubx_blueprint node is nested inside a D2 container, not just
// when it's top-level.
func TestParse_UbxRequired_RefSigil_BlueprintCallNestedInContainer(t *testing.T) {
	src := `
platform_group: "grouping" {
  platform: "ci-platform call" {
    class: ubx_blueprint
    blueprint: "../ci-platform"
  }
}
role: "ci-role" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
  ubx_required.assume_role_policy: "ref:platform.repo_arn"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})
	ri := resourceByName(t, intent, "ci-role")
	if !strings.Contains(string(ri.Config), `"$blueprint_output:platform:repo_arn"`) {
		t.Fatalf("Config = %s, want the nested platform_group.platform node's own bare ID (\"platform\") to resolve via byBareID, same as an ordinary nested resource ref already does", ri.Config)
	}
}

// TestParse_UbxBlueprint_ChildrenNeverIndependentlyClassified is a real,
// live-found regression test (UBI-128's own required live diagram
// verification caught this against a real `ubx plan --from-diagram` run,
// not a unit test): a ubx_blueprint node's own attribute children (e.g.
// "blueprint", "widget_name" -- separate *d2graph.Object values in the
// underlying graph, distinct from the parent node they're declared
// under) must never be independently classified as ordinary topology
// nodes needing their own class: attribute. Before this fix,
// sortedLeaves' own hasClass(ubxBlueprintClass) check only ever matched
// the classed node ITSELF, never excluded its children the way
// inUbxRequiredSubtree already does for an ordinary resource's own
// ubx_required block -- so every ubx_blueprint (and ubx_override) node's
// own children fell through to the ordinary "no class: attribute"
// refusal, producing spurious BLOCKING Questions on every diagram ever
// using either node kind. Silently undetected before this ticket because
// every prior test asserted only that intent.Resources stayed empty
// (which it always did anyway, coincidentally -- nodeKindUnresolved never
// appends to Resources either), never that intent.Intent.Questions was
// also empty.
func TestParse_UbxBlueprint_ChildrenNeverIndependentlyClassified(t *testing.T) {
	src := `
platform: "widget-lib call" {
  class: ubx_blueprint
  blueprint: "../widget-lib"
  widget_name: "primary"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if len(intent.Intent.Questions) != 0 {
		t.Fatalf("Questions = %+v, want none -- a ubx_blueprint node's own attribute children must never be independently classified as ordinary topology nodes", intent.Intent.Questions)
	}
}

// TestParse_UbxOverride_ChildrenNeverIndependentlyClassified is
// TestParse_UbxBlueprint_ChildrenNeverIndependentlyClassified's own
// mirror for ubx_override -- the identical bug class, the identical fix
// (inBlueprintOrOverrideSubtree, parse.go), confirmed separately since
// ubx_override has its own independent hasClass branch in sortedLeaves.
func TestParse_UbxOverride_ChildrenNeverIndependentlyClassified(t *testing.T) {
	src := `
fix: "fix downstream" {
  class: ubx_override
  address: "payments.fake_widget.downstream"
  some_field: "x"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if len(intent.Intent.Questions) != 0 {
		t.Fatalf("Questions = %+v, want none -- a ubx_override node's own attribute children must never be independently classified as ordinary topology nodes", intent.Intent.Questions)
	}
}

// TestParse_UbxRequired_RefSigil_UnknownIdentifier_StillRefusedNotBlueprint
// confirms a ref: naming an identifier that is NEITHER a resource NOR a
// ubx_blueprint node is still refused exactly as before -- the new
// blueprint-call branch inside resolveRefTarget doesn't loosen the
// existing "must name a real topology node" refusal at all.
func TestParse_UbxRequired_RefSigil_UnknownIdentifier_StillRefusedNotBlueprint(t *testing.T) {
	src := `
role: "ci-role" {
  class: aws_iam_role
  ubx_required.name: "ci-runner-v13"
  ubx_required.assume_role_policy: "ref:nonexistent.repo_arn"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", []resolver.DeclaredProvider{awsIAMRoleFullProvider()}, Options{})
	if err == nil || !strings.Contains(err.Error(), "not a resource or ubx_blueprint node") {
		t.Fatalf("Parse err = %v, want a refusal naming both resource and ubx_blueprint as the legal target kinds", err)
	}
}
