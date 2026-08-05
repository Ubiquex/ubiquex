package diagram

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestParse_UbxOverride_NodeBecomesOverride mirrors TestParse_UbxBlueprint_
// NodeBecomesBlueprintCall exactly (parse_test.go) -- same structural-
// attribute-read mechanism, UBI-86 Part 2's own worked example verbatim.
func TestParse_UbxOverride_NodeBecomesOverride(t *testing.T) {
	src := `
fix: "fix pipeline-events" {
  class: ubx_override
  address: "payments.aws_sqs_queue.pipeline-events"
  some_hardcoded_field: "new_value"
}
`
	intent := mustParse(t, src, "payments", nil, Options{})
	if len(intent.Resources) != 0 {
		t.Fatalf("resources = %+v, want none -- a ubx_override node must never be classified as a resource", intent.Resources)
	}
	if len(intent.Overrides) != 1 {
		t.Fatalf("overrides = %+v, want exactly 1", intent.Overrides)
	}
	ov := intent.Overrides[0]
	if ov.Address != "payments.aws_sqs_queue.pipeline-events" {
		t.Errorf("Address = %q, want %q", ov.Address, "payments.aws_sqs_queue.pipeline-events")
	}
	if string(ov.Config["some_hardcoded_field"]) != `"new_value"` {
		t.Fatalf("Config[some_hardcoded_field] = %s, want a JSON string \"new_value\"", ov.Config["some_hardcoded_field"])
	}
	// "address" is reserved -- never duplicated into Config.
	if _, ok := ov.Config["address"]; ok {
		t.Error("Config should not contain the reserved \"address\" key")
	}
}

func TestParse_UbxOverride_MissingAddressAttr_Refused(t *testing.T) {
	src := `
fix: "fix" {
  class: ubx_override
  some_field: "x"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", nil, Options{})
	if err == nil || !strings.Contains(err.Error(), `missing a "address" attribute`) {
		t.Fatalf("expected a missing-address refusal, got %v", err)
	}
}

func TestParse_UbxOverride_MalformedAddress_Refused(t *testing.T) {
	src := `
fix: "fix" {
  class: ubx_override
  address: "not-a-valid-address"
  some_field: "x"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "is not a valid") {
		t.Fatalf("expected a malformed-address refusal, got %v", err)
	}
}

func TestParse_UbxOverride_NoConfigAttrs_Refused(t *testing.T) {
	src := `
fix: "fix" {
  class: ubx_override
  address: "payments.aws_sqs_queue.q"
}
`
	_, err := Parse("test.d2", strings.NewReader(src), "payments", nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "no config attributes to override") {
		t.Fatalf("expected a no-config-attributes refusal, got %v", err)
	}
}

// TestParse_UbxOverride_RefSigilResolvesSiblingNode proves an override's
// own config value can reference another node in the SAME diagram via
// the identical "ref:<node>.<attr-path>" sigil ubx_required already
// established (UBI-95) -- never a second value-parsing mechanism.
func TestParse_UbxOverride_RefSigilResolvesSiblingNode(t *testing.T) {
	src := `
primary: "primary" {
  class: aws_vpc
}
fix: "fix mirror" {
  class: ubx_override
  address: "payments.aws_vpc.mirror"
  owner_ref: "ref:primary.id"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	ov := intent.Overrides[0]
	if !strings.Contains(string(ov.Config["owner_ref"]), `"$ref"`) {
		t.Fatalf("Config[owner_ref] = %s, want a real $ref object", ov.Config["owner_ref"])
	}
	if !strings.Contains(string(ov.Config["owner_ref"]), "payments.aws_vpc.primary.id") {
		t.Fatalf("Config[owner_ref] = %s, want it to resolve to primary's own address", ov.Config["owner_ref"])
	}
}

// TestParse_UbxOverride_MixedWithBlueprintCallAndResource mirrors
// TestParse_UbxBlueprint_MixedWithOrdinaryResource's own coverage
// (parse_test.go): all three node kinds -- an ordinary resource, a
// blueprint call, and an override -- coexist in one diagram without
// interfering with each other's own classification.
func TestParse_UbxOverride_MixedWithBlueprintCallAndResource(t *testing.T) {
	src := `
standalone: "standalone" {
  class: aws_vpc
}
platform: "platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
  repo_name: "payments-ci-artifacts"
}
fix: "fix pipeline-events" {
  class: ubx_override
  address: "payments.aws_sqs_queue.pipeline-events"
  some_hardcoded_field: "new_value"
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want exactly 1 (standalone only)", intent.Resources)
	}
	if len(intent.BlueprintCalls) != 1 {
		t.Fatalf("blueprint_calls = %+v, want exactly 1", intent.BlueprintCalls)
	}
	if len(intent.Overrides) != 1 {
		t.Fatalf("overrides = %+v, want exactly 1", intent.Overrides)
	}
}
