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
	{
		Name:    "platform-iam-policy-attach-shape",
		DocFile: "platform-iam-attach.md",
		Stack:   "platform-iam-attach",
		Check:   checkPlatformIAMPolicyAttachShape,
	},
	{
		Name:    "platform-blueprint-output-named-call",
		DocFile: "platform-blueprint-output.md",
		Stack:   "platform-blueprint-output",
		Check:   checkPlatformBlueprintOutputNamedCall,
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

// checkPlatformIAMPolicyAttachShape is UBI-65's own permanent conformance
// fixture. fixtures/platform-iam-attach.md is NOT a paraphrase -- it is
// copied byte-for-byte from the founder's own real repro
// (~/ubx-playground-3/platform.md, ~/ubx-playground-4/platform.md), the
// exact document a real live Claude Haiku run misread as an inline
// policy (aws_iam_role_policy, 4 resources) where Opus, on the same
// document, correctly produced a standalone managed policy plus a
// separate attachment (aws_iam_policy + aws_iam_role_policy_attachment,
// 5 resources) -- both are valid, real-world AWS patterns, but the
// doc's own governing sentence ("A custom IAM policy called
// \"ci-runner-access\" that allows... Attach this policy to the
// ci-runner role") names the create-then-attach pattern specifically,
// never the word "inline" -- so only the standalone shape is a correct
// transcription here. This is fixture #3 in file order (payments is #1,
// the cross-ref/JSON-embedded-ref platform.md is #2, claimed by UBI-63
// before this ticket's own "fixture #2" framing was written) -- the
// founder's own acceptance bar (every wired model, including the
// cheapest, must get this right) is unchanged by the number.
func checkPlatformIAMPolicyAttachShape(t *testing.T, draft *resolver.IntentFile) {
	t.Helper()

	sawStandalonePolicy, sawAttachment, sawInlinePolicy := scanForIAMPolicyShape(draft.Resources)
	if sawInlinePolicy {
		t.Error(`found an inline aws_iam_role_policy resource -- expected a standalone aws_iam_policy + aws_iam_role_policy_attachment pair instead (UBI-65: the doc's "a custom IAM policy... attach this policy to the role" phrasing names the create-then-attach pattern, never inline)`)
	}
	if !sawStandalonePolicy {
		t.Error("expected a standalone aws_iam_policy resource (the custom \"ci-runner-access\" policy), found none")
	}
	if !sawAttachment {
		t.Error("expected a separate aws_iam_role_policy_attachment resource attaching the policy to the role, found none")
	}
}

// scanForIAMPolicyShape is checkPlatformIAMPolicyAttachShape's own
// *testing.T-free scanning logic -- factored out for the same reason
// scanForRefShapes is (below): harness_test.go's own
// TestScanForIAMPolicyShape_RejectsInlinePolicy asserts against its
// return values directly, rather than needing a fake *testing.T just to
// observe whether a check would have failed.
func scanForIAMPolicyShape(resources []resolver.ResourceIntent) (sawStandalonePolicy, sawAttachment, sawInlinePolicy bool) {
	for _, r := range resources {
		switch r.Type {
		case "aws_iam_policy":
			sawStandalonePolicy = true
		case "aws_iam_role_policy_attachment":
			sawAttachment = true
		case "aws_iam_role_policy":
			sawInlinePolicy = true
		}
	}
	return sawStandalonePolicy, sawAttachment, sawInlinePolicy
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
// checkPlatformBlueprintOutputNamedCall is UBI-128's own permanent
// conformance fixture: fixtures/platform-blueprint-output.md names a
// blueprint call with an explicit "as 'platform'" alias, then references
// that alias's own repo_arn output from a later, unrelated sentence --
// this check fails loudly if either half of that grammar is missing:
// the blueprint_calls[] entry's own CallName never populated (the alias
// silently dropped), or no real {"$ref":{"to":"$blueprint_output:..."}}
// object anywhere in the draft (the later reference silently dropped or
// mis-encoded, e.g. as a plain string or a guessed <stack>.<type>.<name>
// address this package has no business inventing).
func checkPlatformBlueprintOutputNamedCall(t *testing.T, draft *resolver.IntentFile) {
	t.Helper()

	if len(draft.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly one blueprint call, got %d: %+v", len(draft.BlueprintCalls), draft.BlueprintCalls)
	}
	if draft.BlueprintCalls[0].CallName == "" {
		t.Error(`expected blueprint_calls[0].call_name to be populated from the document's own "as 'platform'" alias -- got ""`)
	}

	sawOutputRef, err := scanForBlueprintOutputRef(draft.Resources, draft.BlueprintCalls[0].CallName)
	if err != nil {
		t.Fatal(err)
	}
	if !sawOutputRef {
		t.Errorf(`expected a real {"$ref":{"to":"$blueprint_output:%s:repo_arn"}} object referencing the blueprint call's own repo_arn output somewhere in the draft -- found none`, draft.BlueprintCalls[0].CallName)
	}
}

// scanForBlueprintOutputRef is scanForRefShapes' own narrower sibling:
// walks every resource's own config (recursively, including one level
// into a JSON-embedded string -- the identical walk scanForRefShapes
// already does) looking specifically for a real {"$ref":{"to":"..."}}
// object whose own "to" value starts with
// resolver.BlueprintOutputRefPrefix + callName + ":" -- confirming the
// output reference is real AND names the right call, not just that SOME
// $ref object exists somewhere.
func scanForBlueprintOutputRef(resources []resolver.ResourceIntent, callName string) (found bool, err error) {
	want := resolver.BlueprintOutputRefPrefix + callName + ":"
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			if inner, ok := t["$ref"]; ok && len(t) == 1 {
				if innerMap, ok := inner.(map[string]interface{}); ok {
					if to, ok := innerMap["to"].(string); ok && strings.HasPrefix(to, want) {
						found = true
					}
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
			var decoded interface{}
			if derr := json.Unmarshal([]byte(t), &decoded); derr == nil {
				walk(decoded)
			}
		}
	}
	for _, r := range resources {
		var cfg interface{}
		if uerr := json.Unmarshal(r.Config, &cfg); uerr != nil {
			return false, fmt.Errorf("resource %s.%s: config isn't valid JSON: %w", r.Type, r.Name, uerr)
		}
		walk(cfg)
	}
	return found, nil
}

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
			// UBI-85: nil knownResources -- every conformance fixture
			// drafts against a fresh, never-seen stack.
			draft, _, err := intentprovider.DraftWithRetry(context.Background(), a, f.Stack, f.Doc(t), nil)
			if err != nil {
				t.Fatalf("DraftWithRetry: %v", err)
			}
			f.Check(t, draft)
		})
	}
}
