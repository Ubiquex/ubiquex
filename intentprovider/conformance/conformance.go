// Package conformance is UBI-41's own fixture-runner for intent-provider
// adapters -- see docs/intent-provider.md's own "Conformance suite"
// section for why this is a per-fixture assertion-function discipline,
// not the byte-exact golden-diff discipline the top-level conformance/
// package already established for provider resource types: an LLM's
// output isn't deterministic the way a tfplugin provider's own behavior
// is (two runs of the same adapter against the same fixture can
// legitimately produce different, equally-valid concrete values), so
// "supported" here means "produces a draft with the right structural/
// semantic properties," never "produces this exact JSON."
//
// This is project-internal tooling, not shipped product code -- same
// posture as the top-level conformance/ package's own doc comment.
package conformance

import (
	"context"
	"embed"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/intentprovider"
)

//go:embed fixtures/*.md
var fixturesFS embed.FS

// Fixture is one golden md-doc -> draft check.
type Fixture struct {
	// Name identifies the fixture in test output and per-adapter results.
	Name string
	// DocFile names the embedded fixture doc (fixtures/<DocFile>).
	DocFile string
	// Stack is the target stack this fixture resolves against.
	Stack string
	// Check asserts the structural/semantic properties this fixture cares
	// about -- never an exact-JSON comparison (see the package doc
	// comment). Called with the validated draft DraftWithRetry produced.
	Check func(t *testing.T, draft *resolver.IntentFile)
}

// Doc returns fixture f's own embedded document content.
func (f Fixture) Doc(t *testing.T) []byte {
	t.Helper()
	b, err := fixturesFS.ReadFile("fixtures/" + f.DocFile)
	if err != nil {
		t.Fatalf("read fixture doc %s: %v", f.DocFile, err)
	}
	return b
}

// Fixtures is the golden suite. Fixture #1 is the payments doc from this
// arc's own design transcript (docs/intent-provider.md's own
// "Conformance suite" section) -- chosen because its own "like staging
// but smaller" ambiguity is the exact scenario the ambiguity-as-visible-
// content design center exists to prove out, not an incidental example
// chosen for convenience.
var Fixtures = []Fixture{
	{
		Name:    "payments-like-staging-but-smaller",
		DocFile: "payments.md",
		Stack:   "payments",
		Check:   checkPaymentsLikeStagingButSmaller,
	},
}

func checkPaymentsLikeStagingButSmaller(t *testing.T, draft *resolver.IntentFile) {
	t.Helper()

	if len(draft.Resources) == 0 {
		t.Fatal("expected at least one resource, got none")
	}
	found := false
	for _, r := range draft.Resources {
		if r.Type == "aws_db_instance" && r.Op == resolver.OpCreate {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a create for an aws_db_instance, got %+v", draft.Resources)
	}

	if len(draft.Intent.Assumptions) == 0 && len(draft.Intent.Questions) == 0 {
		t.Error(`expected "like staging but smaller" to surface as an assumption or a question -- got neither, meaning the sizing choice was made silently`)
	}
}

// Run drives a through every fixture in Fixtures via
// intentprovider.DraftWithRetry, running each fixture's own Check against
// the result -- adapter-agnostic, so a fake Adapter proves this harness
// itself works hermetically (harness_test.go) using the identical code
// path a real adapter's own conformance run takes, just without network
// involved.
func Run(t *testing.T, a intentprovider.Adapter) {
	t.Helper()
	for _, f := range Fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			draft, _, err := intentprovider.DraftWithRetry(context.Background(), a, f.Stack, f.Doc(t))
			if err != nil {
				t.Fatalf("DraftWithRetry: %v", err)
			}
			f.Check(t, draft)
		})
	}
}
