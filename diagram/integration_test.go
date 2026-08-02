package diagram

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// These tests prove the two adversarial-table claims docs/diagram-
// medium.md makes about reused, unmodified resolver mechanisms are
// actually true end to end -- Parse() on its own deliberately does NOT
// detect either case (that's the whole point: one shared dependency
// graph and one shared duplicate-address check, not two).

func TestParseThenResolve_CycleInEdges_Rejected(t *testing.T) {
	src := `
classes: {
  aws_db_instance: {}
}
a: node-a { class: aws_db_instance }
b: node-b { class: aws_db_instance }
c: node-c { class: aws_db_instance }
a -> b
b -> c
c -> a
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 3 {
		t.Fatalf("resources = %+v, want 3 (Parse itself never detects the cycle)", intent.Resources)
	}

	l := core.Open(t.TempDir())
	_, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsProvider()}, intent, nil)
	if !errors.Is(err, resolver.ErrCycleDetected) {
		t.Fatalf("resolve err = %v, want ErrCycleDetected", err)
	}
}

func TestParseThenResolve_ContainmentAmbiguity_Rejected(t *testing.T) {
	src := `
classes: {
  aws_db_instance: {}
}
primary: {
  db: same-name { class: aws_db_instance }
}
replica: {
  db: same-name { class: aws_db_instance }
}
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	if len(intent.Resources) != 2 {
		t.Fatalf("resources = %+v, want 2 (Parse itself never deduplicates)", intent.Resources)
	}

	l := core.Open(t.TempDir())
	_, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsProvider()}, intent, nil)
	if !errors.Is(err, resolver.ErrDuplicateResource) {
		t.Fatalf("resolve err = %v, want ErrDuplicateResource", err)
	}
}

// TestParseThenResolve_MissingRequiredAttribute_Refused is UBI-90's own
// permanent conformance case, added alongside the cycle/containment rows
// above (docs/diagram-medium.md's own adversarial table gets a new row):
// a diagram-authored aws_iam_role -- topology only, config always `{}` per
// the medium's own founding "lossy-medium rule" (UBI-47) -- has no way to
// ever supply the real schema's own Required "assume_role_policy". Parse()
// itself doesn't and can't know this (no schema-completeness check lives
// there, only type inference); resolve must refuse it, confirmed here end
// to end through the real, unmodified pipeline, never discoverable only at
// a real ApplyResourceChange call (the founder's own live incident,
// playground-13: aws_iam_role/aws_ecr_repository shipped with the gap
// silently present, failed mid-ship instead).
func TestParseThenResolve_MissingRequiredAttribute_Refused(t *testing.T) {
	src := `
classes: {
  aws_iam_role: {}
}
role: my-role { class: aws_iam_role }
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsIAMRoleProvider()}, Options{})
	if len(intent.Resources) != 1 {
		t.Fatalf("resources = %+v, want 1 (Parse itself never checks schema completeness)", intent.Resources)
	}

	l := core.Open(t.TempDir())
	_, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsIAMRoleProvider()}, intent, nil)
	if !errors.Is(err, resolver.ErrMissingRequiredAttribute) {
		t.Fatalf("resolve err = %v, want ErrMissingRequiredAttribute", err)
	}
	if !strings.Contains(err.Error(), "assume_role_policy") {
		t.Fatalf("resolve err = %v, want it to name assume_role_policy", err)
	}
}

func TestParseThenResolve_HappyPath_FullDraftResolvesCleanly(t *testing.T) {
	src := `
classes: {
  aws_vpc: {}
  aws_db_instance: {}
}
payments: {
  vpc: main-vpc { class: aws_vpc }
  db: primary-db { class: aws_db_instance }
}
payments.db -> payments.vpc
`
	intent := mustParse(t, src, "payments", []resolver.DeclaredProvider{awsProvider()}, Options{})
	intent.Intent.Summary = "a real diagram-authored stack"

	l := core.Open(t.TempDir())
	p, err := resolver.Resolve(l, []resolver.DeclaredProvider{awsProvider()}, intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 2 {
		t.Fatalf("creates = %d, want 2", len(p.Delta.Creates))
	}
	if !strings.Contains(string(p.Delta.Creates[1]), `"depends_on":["payments.aws_vpc.main-vpc"]`) {
		t.Fatalf("second create missing the diagram-authored depends_on: %s", p.Delta.Creates[1])
	}
}

// platformProvider is UBI-91's own live-finale fixture: the founder's own
// exact five-resource platform.d2 topology (playground-13, UBI-90's own
// live repro) -- aws_sqs_queue (needed no ubx_required at all originally;
// UBI-90's own finding was "few required attrs beyond what topology
// implies"), aws_ecr_repository/aws_iam_role/aws_iam_role_policy/
// aws_iam_role_policy_attachment (the four UBI-90's own resolve-time
// refusal flagged), modeled with the exact five real attribute names the
// founder's own live incident named: name, assume_role_policy, policy,
// role, policy_arn.
func platformProvider() resolver.DeclaredProvider {
	return resolver.DeclaredProvider{
		Source:  "hashicorp/aws",
		Version: "6.54.0",
		Schema: &fakeSchema{
			types: map[string]bool{
				"aws_sqs_queue":                  true,
				"aws_ecr_repository":             true,
				"aws_iam_role":                   true,
				"aws_iam_role_policy":            true,
				"aws_iam_role_policy_attachment": true,
			},
			required: map[string]map[string]bool{
				"aws_ecr_repository":             {"name": true},
				"aws_iam_role":                   {"assume_role_policy": true},
				"aws_iam_role_policy":            {"policy": true},
				"aws_iam_role_policy_attachment": {"role": true, "policy_arn": true},
			},
		},
	}
}

// TestParseThenResolve_PlatformD2_UBI91LiveFinale_AllRequiredSupplied_ResolvesClean
// is UBI-91's own live finale, hermetic (this repository's own standing
// rule, CLAUDE.md: never a real cloud provider for live verification --
// see also the ubx_required end-to-end ship+terminate proof,
// cli/diagram_ubx_required_test.go, which drives the real fakeprovider
// subprocess through the full resolve/plan/ship/terminate cycle; this
// test proves the RESOLVE-time claim specifically, with the founder's own
// exact real attribute names, which fakeprovider's own single shippable
// type (fake_widget) can't individually model): the exact five-resource
// topology that produced UBI-90's own partially_shipped incident, this
// time with ubx_required.* supplying every attribute that refusal
// flagged. ZERO missing-required-attribute refusal -- resolve succeeds
// with exactly 5 creates, pure diagram authoring, no md/SDK fallback
// anywhere.
func TestParseThenResolve_PlatformD2_UBI91LiveFinale_AllRequiredSupplied_ResolvesClean(t *testing.T) {
	src := `
classes: {
  aws_sqs_queue: {}
  aws_ecr_repository: {}
  aws_iam_role: {}
  aws_iam_role_policy: {}
  aws_iam_role_policy_attachment: {}
}
platform: {
  queue: "ci-notifications-v13" { class: aws_sqs_queue }
  repo: "app-image-v13" {
    class: aws_ecr_repository
    ubx_required.name: "app-image-v13"
  }
  role: "ci-runner-v13" {
    class: aws_iam_role
    ubx_required.assume_role_policy: |md
      {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "ecs-tasks.amazonaws.com"}, "Action": "sts:AssumeRole"}]}
    |
  }
  policy: "ci-runner-policy-v13" {
    class: aws_iam_role_policy
    ubx_required.policy: |md
      {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "ecr:GetDownloadUrlForLayer", "Resource": "*"}]}
    |
  }
  attachment: "ci-runner-attach-v13" {
    class: aws_iam_role_policy_attachment
    ubx_required.role: "ci-runner-v13"
    ubx_required.policy_arn: "arn:aws:iam::aws:policy/ReadOnlyAccess"
  }
}
platform.policy -> platform.role
platform.attachment -> platform.role
platform.attachment -> platform.policy
`
	providers := []resolver.DeclaredProvider{platformProvider()}
	intent := mustParse(t, src, "ubx-playground-13", providers, Options{})
	if len(intent.Intent.Questions) != 0 {
		t.Fatalf("platform.d2 produced blocking ambiguity, want none: %+v", intent.Intent.Questions)
	}
	if len(intent.Resources) != 5 {
		t.Fatalf("resources = %+v, want exactly 5", intent.Resources)
	}

	l := core.Open(t.TempDir())
	p, err := resolver.Resolve(l, providers, intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v (expected ZERO missing-required-attribute refusal -- every UBI-90-flagged attribute was supplied via ubx_required)", err)
	}
	if len(p.Delta.Creates) != 5 {
		t.Fatalf("creates = %d, want 5 -- ALL five resources, pure diagram authoring", len(p.Delta.Creates))
	}

	// The exact five attribute values the founder's own live incident
	// needed, each present and correctly typed (JSON policy documents
	// parse as valid JSON; plain scalars round-trip verbatim).
	byType := map[string]json.RawMessage{}
	for _, raw := range p.Delta.Creates {
		var node struct {
			Type   string                     `json:"type"`
			Config map[string]json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("unmarshal create node: %v", err)
		}
		for k, v := range node.Config {
			byType[node.Type+"."+k] = v
		}
	}
	checkString := func(key, want string) {
		t.Helper()
		raw, ok := byType[key]
		if !ok {
			t.Fatalf("missing %s in resolved config", key)
		}
		var got string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s is not a JSON string: %s (err=%v)", key, raw, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	checkJSON := func(key string) {
		t.Helper()
		raw, ok := byType[key]
		if !ok {
			t.Fatalf("missing %s in resolved config", key)
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			t.Fatalf("%s is not a JSON string: %s (err=%v)", key, raw, err)
		}
		var policy map[string]interface{}
		if err := json.Unmarshal([]byte(text), &policy); err != nil {
			t.Fatalf("%s content is not valid JSON: %s (err=%v)", key, text, err)
		}
	}
	checkString("aws_ecr_repository.name", "app-image-v13")
	checkJSON("aws_iam_role.assume_role_policy")
	checkJSON("aws_iam_role_policy.policy")
	checkString("aws_iam_role_policy_attachment.role", "ci-runner-v13")
	checkString("aws_iam_role_policy_attachment.policy_arn", "arn:aws:iam::aws:policy/ReadOnlyAccess")
}
