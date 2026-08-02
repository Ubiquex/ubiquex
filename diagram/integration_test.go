package diagram

import (
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
