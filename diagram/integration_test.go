package diagram

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
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
