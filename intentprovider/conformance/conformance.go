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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
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
	{
		Name:    "platform-cross-ref-and-json-embedded-ref",
		DocFile: "platform.md",
		Stack:   "platform",
		Check:   checkPlatformCrossRefAndJSONEmbeddedRef,
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

// checkPlatformCrossRefAndJSONEmbeddedRef is UBI-63 bug 1(c)'s own fix:
// fixture #1 never exercised a real cross-resource reference at all, so
// an adapter that transcribed "@<address>" mentions as the literal
// string "$ref:<address>.<path>" instead of the real wire-shape object
// (exactly what intentprovider/claude's own system prompt was found,
// live, to instruct until this session) sailed through undetected. This
// fixture's own doc names a role and an inline policy that must
// reference it, including one level inside the policy's own
// JSON-encoded string content (docs/schema.md's "JSON-embedded refs"
// amendment) -- this check fails loudly on either of the two ways that
// reference could still be wrong: a literal "$ref:" string anywhere in
// the draft (the exact bug found live), or no real {"$ref": {"to":
// "..."}} object anywhere at all (the reference silently dropped
// instead of mis-encoded).
func checkPlatformCrossRefAndJSONEmbeddedRef(t *testing.T, draft *resolver.IntentFile) {
	t.Helper()

	if len(draft.Resources) < 2 {
		t.Fatalf("expected at least 2 resources (the role and something referencing it), got %d: %+v", len(draft.Resources), draft.Resources)
	}

	sawRealRefObject, sawBrokenRefString, err := scanForRefShapes(draft.Resources)
	if err != nil {
		t.Fatal(err)
	}
	if sawBrokenRefString {
		t.Error(`found a literal "$ref:..." string in the draft -- references must be the real {"$ref": {"to": "..."}} object, never a string (UBI-63 bug 1)`)
	}
	if !sawRealRefObject {
		t.Error(`expected a real {"$ref": {"to": "..."}} object referencing the queue somewhere in the draft (directly, or JSON-embedded inside a policy-shaped config string) -- found none`)
	}
}

// scanForRefShapes is checkPlatformCrossRefAndJSONEmbeddedRef's own
// plain, *testing.T-free scanning logic -- factored out so
// harness_test.go's own TestCheck_RejectsLiteralRefString can assert
// against its return values directly, rather than needing a fake/
// manually-constructed *testing.T (which testing.T is not designed to
// support outside the real test framework) just to observe whether a
// check would have failed. Walks every resource's own decoded config
// looking for the real {"$ref": {"to": "..."}} marker object (at any
// depth, including one level inside a config-string attribute that
// itself decodes as JSON -- the "JSON-embedded refs" case, walked
// exactly like core/resolver's own scanRefEdges does) versus the broken
// "$ref:<address>.<path>" string literal this ticket found live.
func scanForRefShapes(resources []resolver.ResourceIntent) (sawRealRefObject, sawBrokenRefString bool, err error) {
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			if inner, ok := t["$ref"]; ok && len(t) == 1 {
				if _, ok := inner.(map[string]interface{}); ok {
					sawRealRefObject = true
				}
				return
			}
			for _, vv := range t {
				walk(vv)
			}
		case []interface{}:
			for _, vv := range t {
				walk(vv)
			}
		case string:
			if strings.HasPrefix(t, "$ref:") {
				sawBrokenRefString = true
				return
			}
			var decoded interface{}
			if derr := json.Unmarshal([]byte(t), &decoded); derr == nil {
				walk(decoded)
			}
		}
	}
	for _, r := range resources {
		var cfg interface{}
		if uerr := json.Unmarshal(r.Config, &cfg); uerr != nil {
			return false, false, fmt.Errorf("resource %s.%s: config isn't valid JSON: %w", r.Type, r.Name, uerr)
		}
		walk(cfg)
	}
	return sawRealRefObject, sawBrokenRefString, nil
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
